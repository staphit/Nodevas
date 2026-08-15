package realtime

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"nodevas/internal/identity"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// Hub broadcasts change events to connected websocket clients and keeps the
// live collaboration state: who is connected, what they are looking at, and
// which documents they hold open for editing.
//
// Rooms are keyed by project directory rather than project name: a name is
// what a client says, a directory is what the server actually writes, and two
// names can resolve to the same directory.
type Hub struct {
	mu         sync.Mutex
	conns      map[*wsClient]struct{}
	locks      map[string]map[string]*nodeLock // room -> node id -> holder
	docs       map[string]map[string]*docSession
	actorConns map[string]int
	ipConns    map[string]int
	reserved   int
	closed     bool
	// removals counts connection cleanups after a client has left conns but
	// before leaveAllDocs has durably handed its last document to the sidecar.
	// Close waits for it so a workspace is never torn down underneath that I/O.
	removals sync.WaitGroup
	// docPersist gives each room/document key an ordering lock that survives a
	// session being removed and recreated. The small ref-counted registry means
	// unrelated documents never queue behind one another and old keys do not
	// accumulate for the life of the process.
	docPersistGuard sync.Mutex
	docPersist      map[docPersistenceKey]*docPersistence
	nextDocToken    uint64
	// snapshotBytes accounts each unique tokenized snapshot payload exactly once
	// from upload reservation until neither a session nor an outbound job holds
	// it. It deliberately includes queued jobs after a session replacement.
	snapshotBytes int
	// Fixed-cost cadence is shared across an actor's connections and every
	// participant of one document. The byte bucket bounds transfer volume; these
	// buckets bound sidecar encodes/fsyncs even for one-byte snapshots.
	snapshotActorCadence map[string]*snapshotCadence
	snapshotDocCadence   map[docPersistenceKey]*snapshotCadence
	// Large sidecar encoding duplicates the immutable strings into one output
	// buffer. Serializing those copies prevents several 24 MiB documents from
	// multiplying process memory at the same instant.
	docEncode chan struct{}

	persistDocBeforeEncode func()
	recoverDocBeforeDecode func()
	persistDocBeforeWrite  func()

	// openDocBeforeJoin is a deterministic test barrier for the lifecycle gap
	// between disk recovery and the final membership commit. It is nil in
	// production and deliberately runs without h.mu.
	openDocBeforeJoin func()

	// resolve maps a project name to its room key. Nil until the server wires
	// it up, which is why an unresolved client simply stays in the "all
	// events" room instead of failing.
	resolve func(name string) (string, error)
}

type wsClient struct {
	hub  *Hub
	conn *websocket.Conn
	// One queue is the wire order. Keeping ordinary frames and logical snapshot
	// jobs on separate channels lets select deliver a later update before the
	// snapshot it depends on.
	outbound chan clientOutbound
	done     chan struct{}
	once     sync.Once
	id       string
	ip       string
	queueMu  sync.Mutex
	removed  bool

	mu         sync.Mutex
	actor      identity.Actor
	room       string // project directory; "" means "every event"
	project    string // the name the client used, for echoing back
	nodeID     string
	editing    bool
	authorized func() bool

	messageTokens float64
	rateAt        time.Time
	// The stream budget: cursors and drag ghosts, counted separately from the
	// control messages above and bounded by volume as well as by count.
	payloadTokens float64
	payloadBytes  float64
	payloadAt     time.Time
	// A full snapshot is authorized once at start, then its bounded chunks do
	// not consume the ordinary 1 MiB stream burst a second time.
	snapshotTokens float64
	snapshotAt     time.Time
	snapshotActive string // token, guarded by Hub.mu
	snapshotKey    string
	snapshotNext   int
}

type docOutboundSnapshot struct {
	id, token, from string
	blob            *docSnapshotBlob
	tail            []string
	tailBytes       int
}

type clientOutbound struct {
	payload  []byte
	snapshot *docOutboundSnapshot
}

type docSnapshotBlob struct {
	payload string
	refs    int // guarded by Hub.mu; snapshotBytes counts while refs > 0
}

type snapshotCadence struct {
	tokens float64
	at     time.Time
}

type docPersistence struct {
	mu   sync.Mutex
	refs int
}

type docPersistenceKey struct {
	room string
	key  string
}

// nodeLock is a soft lock: it stops two people from typing into one document
// by accident, and it can be taken over on purpose. The optimistic Rev check
// stays underneath as the hard guarantee.
type nodeLock struct {
	client *wsClient
	actor  identity.Actor
	since  time.Time
}

func NewHub() *Hub {
	return &Hub{
		conns:                map[*wsClient]struct{}{},
		locks:                map[string]map[string]*nodeLock{},
		docs:                 map[string]map[string]*docSession{},
		actorConns:           map[string]int{},
		ipConns:              map[string]int{},
		docPersist:           map[docPersistenceKey]*docPersistence{},
		docEncode:            make(chan struct{}, 1),
		snapshotActorCadence: map[string]*snapshotCadence{},
		snapshotDocCadence:   map[docPersistenceKey]*snapshotCadence{},
	}
}

const (
	maxWSConnections         = 128
	maxWSConnectionsPerActor = 8
	maxWSConnectionsPerIP    = 16
	wsMessageBurst           = 20
	wsMessagesPerSecond      = 2
	wsPingInterval           = 30 * time.Second
	wsPingTimeout            = 10 * time.Second

	// Live cursors and a card being dragged are a stream, not an event: two
	// messages a second is the right budget for "I opened this document" and
	// nowhere near enough for "my pointer moved". They are charged to a second
	// bucket so raising it cannot loosen the first, which is what stops a
	// client from hammering the lock table.
	//
	// The byte budget is the one that actually bounds the traffic: a client
	// batches its updates, so it is the volume rather than the count that says
	// whether this is collaboration or a flood.
	wsPayloadBurst           = 120
	wsPayloadPerSecond       = 60
	wsPayloadByteBurst       = 1 << 20
	wsPayloadBytesPerSec     = 256 << 10
	wsSnapshotByteBurst      = maxDocSnapshotTotalBytes
	wsSnapshotBytesPerSec    = 256 << 10
	wsSnapshotCadenceBurst   = 2
	wsSnapshotActorPerSec    = 1.0
	wsSnapshotDocPerSec      = 0.5
	maxSnapshotActorCadences = 1024
	maxAwarenessBytes        = 16 << 10
	maxDragIDs               = 200
	// One frame's ceiling. Big enough for an awareness update carrying a
	// selection in a long document, small enough that 128 connections cannot
	// each park a megabyte in the read buffer.
	wsReadLimit = 64 << 10
)

func actorConnectionKey(actor identity.Actor) string {
	if actor.ID != "" {
		return actor.ID
	}
	return actor.Name
}

// reserveConnection closes the race between an HTTP capacity check and the
// WebSocket upgrade. A successful reservation belongs to remove after the
// client is installed, or to releaseReservation if setup fails.
func (h *Hub) reserveConnection(actor identity.Actor, ip string) bool {
	actorKey := actorConnectionKey(actor)
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed || h.reserved >= maxWSConnections ||
		h.actorConns[actorKey] >= maxWSConnectionsPerActor ||
		h.ipConns[ip] >= maxWSConnectionsPerIP {
		return false
	}
	h.reserved++
	h.actorConns[actorKey]++
	h.ipConns[ip]++
	return true
}

func (h *Hub) releaseReservation(actor identity.Actor, ip string) {
	h.mu.Lock()
	h.releaseReservationLocked(actorConnectionKey(actor), ip)
	h.mu.Unlock()
}

func (h *Hub) releaseReservationLocked(actorKey, ip string) {
	if h.reserved > 0 {
		h.reserved--
	}
	if h.actorConns[actorKey] <= 1 {
		delete(h.actorConns, actorKey)
	} else {
		h.actorConns[actorKey]--
	}
	if h.ipConns[ip] <= 1 {
		delete(h.ipConns, ip)
	} else {
		h.ipConns[ip]--
	}
}

// SetProjectResolver teaches the hub how to turn a project name into a room.
func (h *Hub) SetProjectResolver(resolve func(name string) (string, error)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.resolve = resolve
}

type Event struct {
	Type string `json:"type"` // graph-changed | node-changed | state-changed | presence | locks | awareness | graph-drag | graph-ops
	ID   string `json:"id,omitempty"`

	Project string          `json:"project,omitempty"`
	Peers   []Peer          `json:"peers,omitempty"`
	Locks   []Lock          `json:"locks,omitempty"`
	Actor   *identity.Actor `json:"actor,omitempty"`
	SelfID  string          `json:"selfId,omitempty"`

	// Which connection this came from, so the sender can ignore its own echo
	// instead of applying a change it already made.
	From string `json:"from,omitempty"`
	// An opaque awareness update, base64. The server never reads it: cursors
	// and selections are the client's business, and relaying bytes it does not
	// parse is what keeps this hub out of the editor's data model.
	Payload string `json:"payload,omitempty"`
	// A card being dragged right now. Never written anywhere — the position
	// that lasts arrives later, as a graph op.
	Drag *DragGhost `json:"drag,omitempty"`
	// The ops a mutation applied, so a peer can move one card rather than
	// reloading the whole graph. Passed through as written.
	Ops json.RawMessage `json:"ops,omitempty"`

	// Live document fields. Omitted when unset so every event above keeps the
	// wire shape it already had.
	//
	// Seed is the answer to the one question a joining client cannot work out
	// for itself: whether to load the document from the file. A client that
	// seeds into a document the stored updates already describe ends up with
	// the text twice.
	Leader bool   `json:"leader,omitempty"`
	Seed   bool   `json:"seed,omitempty"`
	Rev    string `json:"rev,omitempty"`
	Token  string `json:"token,omitempty"`
	Seq    int    `json:"seq,omitempty"`
	Size   int    `json:"size,omitempty"`
	Chunks int    `json:"chunks,omitempty"`
}

// DragGhost is where somebody's selection currently sits under their pointer,
// as an offset in board pixels from where those cards actually are. Pixels
// rather than cells because that is what the gesture produces, and every client
// derives the same board layout from the same graph.
type DragGhost struct {
	IDs []string `json:"ids"`
	DX  float64  `json:"dx"`
	DY  float64  `json:"dy"`
	// False when the gesture ended, which is the peer's cue to drop the ghost.
	Active bool `json:"active"`
}

type Peer struct {
	ID      string         `json:"id"`
	Actor   identity.Actor `json:"actor"`
	NodeID  string         `json:"nodeId,omitempty"`
	Editing bool           `json:"editing,omitempty"`
	// Decided here rather than in each browser so everyone sees the same person
	// in the same colour.
	Color string `json:"color,omitempty"`
}

// peerColor picks a stable hue per account. Saturation and lightness are fixed
// so no one is handed a colour that vanishes against the board.
func peerColor(actor identity.Actor) string {
	key := actor.ID
	if key == "" {
		key = actor.Name
	}
	if key == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(key))
	hue := (int(sum[0])<<8 | int(sum[1])) % 360
	return fmt.Sprintf("hsl(%d 70%% 45%%)", hue)
}

type Lock struct {
	NodeID string         `json:"nodeId"`
	Actor  identity.Actor `json:"actor"`
	Since  string         `json:"since"`
	Mine   bool           `json:"mine,omitempty"`
}

// Broadcast queues an event for every client, whatever project they watch. Use
// it for workspace-level news; project changes belong in BroadcastRoom.
func (h *Hub) Broadcast(evType, id string) {
	h.send(Event{Type: evType, ID: id}, func(*wsClient) bool { return true })
}

// BroadcastRoom queues an event for the clients watching one project.
//
// A client that never subscribed still receives everything: older clients (and
// the popout editor) do not send a subscription, and going silent on them
// would look like the server stopped noticing their edits.
func (h *Hub) BroadcastRoom(room, evType, id string) {
	if room == "" {
		h.Broadcast(evType, id)
		return
	}
	h.send(Event{Type: evType, ID: id}, func(client *wsClient) bool {
		client.mu.Lock()
		defer client.mu.Unlock()
		return client.room == "" || client.room == room
	})
}

// relay passes one client's stream message to the rest of its room.
//
// The sender is left out rather than filtering on `From` in the browser: it
// already has what it sent, and the round trip is the one thing that would
// make a live cursor lag behind the pointer drawing it.
func (h *Hub) relay(from *wsClient, event Event) {
	from.mu.Lock()
	room := from.room
	from.mu.Unlock()
	event.From = from.id
	h.send(event, func(client *wsClient) bool {
		if client == from {
			return false
		}
		client.mu.Lock()
		defer client.mu.Unlock()
		return client.room == room
	})
}

// BroadcastOps tells a room which ops just landed, so a client can apply them
// where it stands instead of reloading the graph. `from` is the peer that made
// the change, echoed back so it can ignore its own.
func (h *Hub) BroadcastOps(room, from string, ops json.RawMessage) {
	if len(ops) == 0 {
		h.BroadcastRoom(room, "graph-changed", "")
		return
	}
	event := Event{Type: "graph-ops", Ops: ops, From: from}
	if room == "" {
		h.send(event, func(*wsClient) bool { return true })
		return
	}
	h.send(event, func(client *wsClient) bool {
		client.mu.Lock()
		defer client.mu.Unlock()
		return client.room == "" || client.room == room
	})
}

func (h *Hub) send(event Event, want func(*wsClient) bool) {
	payload, err := json.Marshal(event)
	if err != nil {
		log.Printf("ws marshal: %v", err)
		return
	}
	h.mu.Lock()
	var slow []*wsClient
	for client := range h.conns {
		if !want(client) {
			continue
		}
		if !client.enqueue(clientOutbound{payload: payload}) {
			slow = append(slow, client)
		}
	}
	h.mu.Unlock()

	for _, client := range slow {
		h.remove(client)
	}
}

// sendTo delivers one event to a single client.
func (h *Hub) sendTo(client *wsClient, event Event) {
	payload, err := json.Marshal(event)
	if err != nil {
		log.Printf("ws marshal: %v", err)
		return
	}
	if !client.enqueue(clientOutbound{payload: payload}) {
		h.remove(client)
	}
}

// queueEvent is the non-removing half of sendTo. Document code uses it while
// holding the per-document ordering turn, then removes any slow connection
// after that turn is released. That preserves wire order without letting
// connection cleanup recursively acquire the same document lock.
func queueEvent(client *wsClient, event Event) bool {
	payload, err := json.Marshal(event)
	if err != nil {
		log.Printf("ws marshal: %v", err)
		return true
	}
	return client.enqueue(clientOutbound{payload: payload})
}

// retainSnapshotBlobLocked adds one session or outbound-job reference. The
// payload is charged only once no matter how many peers share it.
func (h *Hub) retainSnapshotBlobLocked(blob *docSnapshotBlob) {
	if blob == nil {
		return
	}
	if blob.refs == 0 {
		h.snapshotBytes += len(blob.payload)
	}
	blob.refs++
}

func (h *Hub) releaseSnapshotBlobLocked(blob *docSnapshotBlob) {
	if blob == nil || blob.refs == 0 {
		return
	}
	blob.refs--
	if blob.refs == 0 {
		h.snapshotBytes -= len(blob.payload)
		if h.snapshotBytes < 0 {
			panic("realtime snapshot accounting underflow")
		}
	}
}

func (h *Hub) releaseSnapshotJobLocked(snapshot *docOutboundSnapshot) {
	if snapshot == nil {
		return
	}
	h.releaseSnapshotBlobLocked(snapshot.blob)
	h.snapshotBytes -= snapshot.tailBytes
	if h.snapshotBytes < 0 {
		panic("realtime snapshot accounting underflow")
	}
}

func (h *Hub) releaseSnapshotJob(snapshot *docOutboundSnapshot) {
	h.mu.Lock()
	h.releaseSnapshotJobLocked(snapshot)
	h.mu.Unlock()
}

// queueSnapshotLocked enqueues one logical snapshot job. It must be called
// with h.mu held so retaining the shared blob and publishing the queue entry
// are atomic with session replacement.
func (h *Hub) queueSnapshotLocked(client *wsClient, snapshot docOutboundSnapshot) bool {
	if snapshot.blob == nil || snapshot.token == "" {
		return false
	}
	for _, update := range snapshot.tail {
		snapshot.tailBytes += len(update)
	}
	additional := snapshot.tailBytes
	if snapshot.blob.refs == 0 {
		additional += len(snapshot.blob.payload)
	}
	if h.snapshotBytes+additional > maxHubSnapshotBytes {
		return false
	}
	h.retainSnapshotBlobLocked(snapshot.blob)
	h.snapshotBytes += snapshot.tailBytes
	if client.enqueue(clientOutbound{snapshot: &snapshot}) {
		return true
	}
	h.releaseSnapshotJobLocked(&snapshot)
	return false
}

// enqueue never removes a slow client. Callers may therefore use it while a
// document ordering lock is held, then perform the removal after releasing the
// lock. queueMu also makes removal's drain final: no job can appear afterwards.
func (client *wsClient) enqueue(out clientOutbound) bool {
	client.queueMu.Lock()
	defer client.queueMu.Unlock()
	if client.removed {
		return false
	}
	select {
	case client.outbound <- out:
		return true
	default:
		return false
	}
}

func (h *Hub) writeLoop(client *wsClient) {
	ping := time.NewTicker(wsPingInterval)
	defer ping.Stop()
	for {
		select {
		case out := <-client.outbound:
			if out.snapshot != nil {
				failed := false
				for _, event := range snapshotEvents(*out.snapshot) {
					payload, _ := json.Marshal(event)
					ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
					err := client.conn.Write(ctx, websocket.MessageText, payload)
					cancel()
					if err != nil {
						failed = true
						break
					}
				}
				h.releaseSnapshotJob(out.snapshot)
				if failed {
					h.remove(client)
					return
				}
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			err := client.conn.Write(ctx, websocket.MessageText, out.payload)
			cancel()
			if err != nil {
				h.remove(client)
				return
			}
		case <-ping.C:
			if client.authorized != nil && !client.authorized() {
				h.remove(client)
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), wsPingTimeout)
			err := client.conn.Ping(ctx)
			cancel()
			if err != nil {
				h.remove(client)
				return
			}
		case <-client.done:
			return
		}
	}
}

func snapshotEvents(snapshot docOutboundSnapshot) []Event {
	payload := snapshot.blob.payload
	chunks := (len(payload) + maxDocSnapshotBytes - 1) / maxDocSnapshotBytes
	events := []Event{{Type: "doc-snapshot-start", ID: snapshot.id, Token: snapshot.token, Size: len(payload), Chunks: chunks, From: snapshot.from}}
	for seq, offset := 0, 0; offset < len(payload); seq, offset = seq+1, offset+maxDocSnapshotBytes {
		end := min(offset+maxDocSnapshotBytes, len(payload))
		events = append(events, Event{Type: "doc-snapshot-chunk", ID: snapshot.id, Token: snapshot.token, Seq: seq, Payload: payload[offset:end], From: snapshot.from})
	}
	events = append(events, Event{Type: "doc-snapshot-commit", ID: snapshot.id, Token: snapshot.token, From: snapshot.from})
	for _, update := range snapshot.tail {
		events = append(events, Event{Type: "doc-update", ID: snapshot.id, Payload: update, From: snapshot.from})
	}
	return events
}

func (h *Hub) remove(client *wsClient) {
	client.once.Do(func() {
		h.mu.Lock()
		delete(h.conns, client)
		h.releaseReservationLocked(actorConnectionKey(client.actor), client.ip)
		h.removals.Add(1)
		if client.snapshotActive != "" {
			// The owning document cleanup below releases the actual reservation.
			client.snapshotActive = ""
		}
		h.mu.Unlock()
		defer h.removals.Done()
		client.queueMu.Lock()
		client.removed = true
		close(client.done)
		var abandoned []*docOutboundSnapshot
		for {
			select {
			case out := <-client.outbound:
				if out.snapshot != nil {
					abandoned = append(abandoned, out.snapshot)
				}
			default:
				client.queueMu.Unlock()
				for _, snapshot := range abandoned {
					h.releaseSnapshotJob(snapshot)
				}
				goto drained
			}
		}
	drained:
		if client.conn != nil {
			_ = client.conn.CloseNow()
		}

		client.mu.Lock()
		room := client.room
		client.mu.Unlock()
		// A dropped connection must not leave a document locked forever, nor a
		// session led by someone who is no longer there to save the file.
		h.releaseAll(client, room)
		// leaveAllDocs logs and marks the affected document degraded on failure.
		// Shutdown reports the final retry result below, not this historical
		// attempt: a later sweep may already have durably repaired it.
		_ = h.leaveAllDocs(client)
		h.publishPresence(room)
	})
}

// allowPayload charges the stream budget: a message count and a byte volume,
// both of which have to have room. Refilled from the same clock so a client
// that goes quiet gets its allowance back.
func (client *wsClient) allowPayload(now time.Time, size int) bool {
	if client.payloadAt.IsZero() {
		client.payloadAt = now
		client.payloadTokens = wsPayloadBurst
		client.payloadBytes = wsPayloadByteBurst
	}
	if elapsed := now.Sub(client.payloadAt).Seconds(); elapsed > 0 {
		client.payloadTokens = min(float64(wsPayloadBurst), client.payloadTokens+elapsed*wsPayloadPerSecond)
		client.payloadBytes = min(float64(wsPayloadByteBurst), client.payloadBytes+elapsed*wsPayloadBytesPerSec)
		client.payloadAt = now
	}
	if client.payloadTokens < 1 || client.payloadBytes < float64(size) {
		return false
	}
	client.payloadTokens--
	client.payloadBytes -= float64(size)
	return true
}

// canSnapshotStart checks the connection-local byte budget without charging
// it. The start path charges only after every structural, capacity, actor, and
// document check succeeds, so rejected attempts cannot consume retry budget.
func (client *wsClient) canSnapshotStart(now time.Time, size int) bool {
	if client.snapshotAt.IsZero() {
		return size > 0 && size <= wsSnapshotByteBurst
	}
	tokens := client.snapshotTokens
	if elapsed := now.Sub(client.snapshotAt).Seconds(); elapsed > 0 {
		tokens = min(float64(wsSnapshotByteBurst), tokens+elapsed*wsSnapshotBytesPerSec)
	}
	return size > 0 && tokens >= float64(size)
}

func (client *wsClient) chargeSnapshotStart(now time.Time, size int) {
	if client.snapshotAt.IsZero() {
		client.snapshotTokens = wsSnapshotByteBurst
	} else if elapsed := now.Sub(client.snapshotAt).Seconds(); elapsed > 0 {
		client.snapshotTokens = min(float64(wsSnapshotByteBurst), client.snapshotTokens+elapsed*wsSnapshotBytesPerSec)
	}
	client.snapshotAt = now
	client.snapshotTokens -= float64(size)
}

func snapshotCadenceTokens(cadence *snapshotCadence, now time.Time, rate float64) float64 {
	if cadence == nil {
		return wsSnapshotCadenceBurst
	}
	tokens := cadence.tokens
	if elapsed := now.Sub(cadence.at).Seconds(); elapsed > 0 {
		tokens = min(float64(wsSnapshotCadenceBurst), tokens+elapsed*rate)
	}
	return tokens
}

// canSnapshotCadenceLocked applies a fixed-cost limit shared across all of an
// actor's sockets and all participants of one document. This is separate from
// the byte budget because encoding/fsync cost is material even for a one-byte
// snapshot. Caller holds h.mu.
func (h *Hub) canSnapshotCadenceLocked(actorKey string, docKey docPersistenceKey, now time.Time) bool {
	if h.snapshotActorCadence[actorKey] == nil && len(h.snapshotActorCadence) >= maxSnapshotActorCadences {
		// Completed cooldowns carry no state worth retaining. Prune them only at
		// the bound, keeping the common path constant-time and the map bounded.
		for key, cadence := range h.snapshotActorCadence {
			if snapshotCadenceTokens(cadence, now, wsSnapshotActorPerSec) >= wsSnapshotCadenceBurst {
				delete(h.snapshotActorCadence, key)
			}
		}
		if len(h.snapshotActorCadence) >= maxSnapshotActorCadences {
			return false
		}
	}
	return snapshotCadenceTokens(h.snapshotActorCadence[actorKey], now, wsSnapshotActorPerSec) >= 1 &&
		snapshotCadenceTokens(h.snapshotDocCadence[docKey], now, wsSnapshotDocPerSec) >= 1
}

// chargeSnapshotCadenceLocked is called exactly once for an accepted start.
// Caller holds h.mu and has already called canSnapshotCadenceLocked.
func (h *Hub) chargeSnapshotCadenceLocked(actorKey string, docKey docPersistenceKey, now time.Time) {
	actor := h.snapshotActorCadence[actorKey]
	actorTokens := snapshotCadenceTokens(actor, now, wsSnapshotActorPerSec)
	if actor == nil {
		actor = &snapshotCadence{}
		h.snapshotActorCadence[actorKey] = actor
	}
	actor.tokens = actorTokens - 1
	actor.at = now

	doc := h.snapshotDocCadence[docKey]
	docTokens := snapshotCadenceTokens(doc, now, wsSnapshotDocPerSec)
	if doc == nil {
		doc = &snapshotCadence{}
		h.snapshotDocCadence[docKey] = doc
	}
	doc.tokens = docTokens - 1
	doc.at = now
}

func (h *Hub) allowSnapshotChunk(client *wsClient, key, token string, seq int) bool {
	h.mu.Lock()
	allowed := client.snapshotActive == token && token != "" && client.snapshotKey == key && client.snapshotNext == seq
	h.mu.Unlock()
	return allowed
}

func (client *wsClient) allowMessage(now time.Time) bool {
	if client.rateAt.IsZero() {
		client.rateAt = now
		client.messageTokens = wsMessageBurst
	}
	elapsed := now.Sub(client.rateAt).Seconds()
	if elapsed > 0 {
		client.messageTokens = min(float64(wsMessageBurst), client.messageTokens+elapsed*wsMessagesPerSecond)
		client.rateAt = now
	}
	if client.messageTokens < 1 {
		return false
	}
	client.messageTokens--
	return true
}

// Close disconnects every live client during server shutdown and returns the
// final durability result after retrying every retained empty session. Earlier
// disconnect failures are intentionally diagnostic-only: a successful sweep
// may already have made them durable by the time shutdown begins.
func (h *Hub) Close() error {
	h.mu.Lock()
	h.closed = true
	clients := make([]*wsClient, 0, len(h.conns))
	for client := range h.conns {
		clients = append(clients, client)
	}
	h.mu.Unlock()

	for _, client := range clients {
		h.remove(client)
	}
	// A disconnect may already have removed itself from conns before Close took
	// its snapshot. Its cleanup still owns sidecar I/O, so wait for both those
	// in-progress removals and the clients just removed above.
	h.removals.Wait()

	return h.retryEmptyDocSessions()
}

// clientMessage is what a browser sends up the socket.
type clientMessage struct {
	Type    string `json:"type"`    // subscribe | presence | lock | unlock | awareness | graph-drag | doc-*
	Project string `json:"project"` // project name, for subscribe
	NodeID  string `json:"nodeId"`
	Editing bool   `json:"editing"`
	Steal   bool   `json:"steal"` // take a lock someone else holds

	// A subpage of the node, for the live document messages. Carried but not
	// yet keyed on, so subpages become a session of their own without the
	// browser having to speak a different protocol.
	PageID string `json:"pageId"`
	// The file revision a leader just wrote, so its peers can adopt it instead
	// of reading their own session's write back as somebody else's edit.
	Rev    string `json:"rev"`
	Token  string `json:"token"`
	Seq    int    `json:"seq"`
	Size   int    `json:"size"`
	Chunks int    `json:"chunks"`

	// Stream messages. Payload is an opaque awareness update; the rest is one
	// selection being dragged across the board right now.
	Payload string   `json:"payload"`
	IDs     []string `json:"ids"`
	DX      float64  `json:"dx"`
	DY      float64  `json:"dy"`
	Active  bool     `json:"active"`
}

// isPayload reports whether a message is charged to the stream budget rather
// than the control one.
func isPayload(messageType string) bool {
	switch messageType {
	case "awareness", "graph-drag":
		return true
	case "doc-open", "doc-close", "doc-update", "doc-snapshot", "doc-flushed", "doc-compact-request", "doc-snapshot-start", "doc-snapshot-chunk", "doc-snapshot-commit", "doc-snapshot-abort":
		// Typing is a stream too: a CRDT update per keystroke batch would eat
		// the control budget in the first sentence.
		return true
	default:
		return false
	}
}

func (h *Hub) handleMessage(client *wsClient, message clientMessage) {
	switch message.Type {
	case "subscribe":
		h.subscribe(client, message.Project)
	case "presence":
		client.mu.Lock()
		client.nodeID = message.NodeID
		client.editing = message.Editing
		room := client.room
		client.mu.Unlock()
		h.publishPresence(room)
	case "lock":
		h.acquireLock(client, message.NodeID, message.Steal)
	case "unlock":
		h.releaseLock(client, message.NodeID)
	case "awareness":
		h.relay(client, Event{Type: "awareness", Payload: message.Payload})
	case "graph-drag":
		h.relay(client, Event{Type: "graph-drag", Drag: &DragGhost{
			IDs: message.IDs, DX: message.DX, DY: message.DY, Active: message.Active,
		}})
	case "doc-open":
		h.openDoc(client, docKey(message))
	case "doc-close":
		h.closeDoc(client, docKey(message))
	case "doc-update":
		h.docUpdate(client, docKey(message), message.Payload)
	case "doc-snapshot":
		h.docSnapshot(client, docKey(message), message.Payload)
	case "doc-flushed":
		h.docFlushed(client, docKey(message), message.Rev)
	case "doc-compact-request":
		h.docCompactRequest(client, docKey(message))
	case "doc-snapshot-start":
		h.docSnapshotStart(client, docKey(message), message)
	case "doc-snapshot-chunk":
		h.docSnapshotChunk(client, docKey(message), message)
	case "doc-snapshot-commit":
		h.docSnapshotCommit(client, docKey(message), message)
	case "doc-snapshot-abort":
		h.docSnapshotAbort(client, docKey(message), message)
	}
}

func (h *Hub) subscribe(client *wsClient, project string) {
	h.mu.Lock()
	resolve := h.resolve
	h.mu.Unlock()

	room := ""
	if resolve != nil && project != "" {
		resolved, err := resolve(project)
		if err != nil {
			// An unknown project leaves the client in the catch-all room
			// rather than silently muted.
			log.Printf("ws subscribe %q: %v", project, err)
		} else {
			room = resolved
		}
	}

	client.mu.Lock()
	previous := client.room
	client.room = room
	client.project = project
	client.nodeID = ""
	client.editing = false
	client.mu.Unlock()

	if previous != room {
		h.releaseAll(client, previous)
		// Sessions are keyed by room, so a client that walked to another
		// project would otherwise keep receiving the first one's updates.
		// The document retains and logs its own failure state. Do not save a
		// historical error for Close: a subsequent sweep can repair it.
		_ = h.leaveAllDocs(client)
		h.publishPresence(previous)
	}
	h.publishPresence(room)
	h.publishLocks(room)
}

// acquireLock takes the soft lock on a document, or reports who holds it.
func (h *Hub) acquireLock(client *wsClient, nodeID string, steal bool) {
	if nodeID == "" {
		return
	}
	client.mu.Lock()
	room := client.room
	actor := client.actor
	client.mu.Unlock()

	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	if _, live := h.conns[client]; !live {
		h.mu.Unlock()
		return
	}
	if h.locks[room] == nil {
		h.locks[room] = map[string]*nodeLock{}
	}
	held, taken := h.locks[room][nodeID]
	switch {
	case !taken || held.client == client || steal:
		h.locks[room][nodeID] = &nodeLock{client: client, actor: actor, since: time.Now()}
		h.mu.Unlock()
		h.publishLocks(room)
	default:
		holder := held.actor
		h.mu.Unlock()
		h.sendTo(client, Event{Type: "lock-denied", ID: nodeID, Actor: &holder})
	}
}

func (h *Hub) releaseLock(client *wsClient, nodeID string) {
	client.mu.Lock()
	room := client.room
	client.mu.Unlock()

	h.mu.Lock()
	if held, ok := h.locks[room][nodeID]; ok && held.client == client {
		delete(h.locks[room], nodeID)
	}
	h.mu.Unlock()
	h.publishLocks(room)
}

func (h *Hub) releaseAll(client *wsClient, room string) {
	h.mu.Lock()
	changed := false
	for nodeID, held := range h.locks[room] {
		if held.client == client {
			delete(h.locks[room], nodeID)
			changed = true
		}
	}
	h.mu.Unlock()
	if changed {
		h.publishLocks(room)
	}
}

// publishPresence tells a room who is in it and what they are looking at.
func (h *Hub) publishPresence(room string) {
	h.mu.Lock()
	peers := make([]Peer, 0, len(h.conns))
	targets := make([]*wsClient, 0, len(h.conns))
	for client := range h.conns {
		client.mu.Lock()
		inRoom := client.room == room
		peer := Peer{
			ID:      client.id,
			Actor:   client.actor,
			NodeID:  client.nodeID,
			Editing: client.editing,
			Color:   peerColor(client.actor),
		}
		project := client.project
		client.mu.Unlock()
		if !inRoom {
			continue
		}
		_ = project
		peers = append(peers, peer)
		targets = append(targets, client)
	}
	h.mu.Unlock()

	for _, client := range targets {
		client.mu.Lock()
		project := client.project
		client.mu.Unlock()
		h.sendTo(client, Event{Type: "presence", Project: project, Peers: peers})
	}
}

// publishLocks tells a room which documents are held, and by whom.
func (h *Hub) publishLocks(room string) {
	h.mu.Lock()
	held := make([]Lock, 0, len(h.locks[room]))
	owners := make([]*wsClient, 0, len(h.locks[room]))
	for nodeID, lock := range h.locks[room] {
		held = append(held, Lock{
			NodeID: nodeID,
			Actor:  lock.actor,
			Since:  lock.since.UTC().Format(time.RFC3339),
		})
		owners = append(owners, lock.client)
	}
	targets := make([]*wsClient, 0, len(h.conns))
	for client := range h.conns {
		client.mu.Lock()
		inRoom := client.room == room
		client.mu.Unlock()
		if inRoom {
			targets = append(targets, client)
		}
	}
	h.mu.Unlock()

	for _, client := range targets {
		mine := make([]Lock, len(held))
		copy(mine, held)
		for index := range mine {
			mine[index].Mine = owners[index] == client
		}
		client.mu.Lock()
		project := client.project
		client.mu.Unlock()
		h.sendTo(client, Event{Type: "locks", Project: project, Locks: mine})
	}
}
