package realtime

import (
	"encoding/json"
	"fmt"
	"math"
	"nodevas/internal/identity"
	"nodevas/internal/store"
	"strings"
	"testing"
	"time"
)

func TestConnectionReservationsEnforceActorAndGlobalCaps(t *testing.T) {
	hub := NewHub()
	actor := identity.Actor{ID: "actor", Name: "actor", Role: identity.RoleMember}
	for index := 0; index < maxWSConnectionsPerActor; index++ {
		if !hub.reserveConnection(actor, fmt.Sprintf("192.0.2.%d", index+1)) {
			t.Fatalf("actor reservation %d rejected early", index)
		}
	}
	if hub.reserveConnection(actor, "198.51.100.1") {
		t.Fatal("per-actor websocket cap was bypassed")
	}
	for index := 0; index < maxWSConnectionsPerActor; index++ {
		hub.releaseReservation(actor, fmt.Sprintf("192.0.2.%d", index+1))
	}
	if hub.reserved != 0 || len(hub.actorConns) != 0 || len(hub.ipConns) != 0 {
		t.Fatalf("reservation counters leaked: total=%d actors=%v ips=%v", hub.reserved, hub.actorConns, hub.ipConns)
	}

	for index := 0; index < maxWSConnectionsPerIP; index++ {
		current := identity.Actor{ID: fmt.Sprintf("ip-actor-%d", index)}
		if !hub.reserveConnection(current, "192.0.2.50") {
			t.Fatalf("IP reservation %d rejected early", index)
		}
	}
	if hub.reserveConnection(identity.Actor{ID: "ip-overflow"}, "192.0.2.50") {
		t.Fatal("per-IP websocket cap was bypassed")
	}
	for index := 0; index < maxWSConnectionsPerIP; index++ {
		hub.releaseReservation(identity.Actor{ID: fmt.Sprintf("ip-actor-%d", index)}, "192.0.2.50")
	}

	for index := 0; index < maxWSConnections; index++ {
		current := identity.Actor{ID: fmt.Sprintf("actor-%d", index)}
		if !hub.reserveConnection(current, fmt.Sprintf("2001:db8::%x", index+1)) {
			t.Fatalf("global reservation %d rejected early", index)
		}
	}
	if hub.reserveConnection(identity.Actor{ID: "overflow"}, "203.0.113.1") {
		t.Fatal("global websocket cap was bypassed")
	}
}

func TestMessageTokenBucketLimitsBurstAndRefills(t *testing.T) {
	start := time.Unix(100, 0)
	client := &wsClient{messageTokens: wsMessageBurst, rateAt: start}
	for index := 0; index < wsMessageBurst; index++ {
		if !client.allowMessage(start) {
			t.Fatalf("message %d rejected inside burst", index)
		}
	}
	if client.allowMessage(start) {
		t.Fatal("message burst cap was bypassed")
	}
	if !client.allowMessage(start.Add(time.Second)) || !client.allowMessage(start.Add(time.Second)) {
		t.Fatal("token bucket did not refill at configured rate")
	}
	if client.allowMessage(start.Add(time.Second)) {
		t.Fatal("token bucket refilled too many tokens")
	}
}

func TestStreamBudgetIsSeparateFromTheControlOne(t *testing.T) {
	start := time.Unix(100, 0)
	client := &wsClient{messageTokens: wsMessageBurst, rateAt: start}
	// Spending the control budget must not cost a cursor its allowance: two
	// messages a second is a sane cap for locks and a useless one for pointers.
	for index := 0; index < wsMessageBurst; index++ {
		client.allowMessage(start)
	}
	if !client.allowPayload(start, 64) {
		t.Fatal("a cursor update was refused on the control budget")
	}
	for index := 0; index < wsPayloadBurst-1; index++ {
		if !client.allowPayload(start, 64) {
			t.Fatalf("payload %d rejected inside burst", index)
		}
	}
	if client.allowPayload(start, 64) {
		t.Fatal("payload burst cap was bypassed")
	}
}

func TestStreamBudgetIsBoundedByVolumeAsWellAsCount(t *testing.T) {
	start := time.Unix(100, 0)
	client := &wsClient{}
	// Well inside the message count, but the whole byte burst in one go.
	if !client.allowPayload(start, wsPayloadByteBurst) {
		t.Fatal("the first full-size payload was refused")
	}
	if client.allowPayload(start, 1024) {
		t.Fatal("the byte budget was bypassed")
	}
	// A second later the refill covers a kilobyte many times over.
	if !client.allowPayload(start.Add(time.Second), 1024) {
		t.Fatal("the byte budget did not refill")
	}
}

func TestAStreamMessageIsRefusedToAVisitor(t *testing.T) {
	// A visitor may watch a room and show a cursor, but a ghost card being
	// dragged is a change other people see, so it is refused on the same terms
	// as a lock.
	for _, messageType := range []string{"lock", "unlock", "graph-drag"} {
		if !mutatesSharedState(messageType) {
			t.Fatalf("%q is not refused to a read-only session", messageType)
		}
	}
	for _, messageType := range []string{"subscribe", "presence", "awareness"} {
		if mutatesSharedState(messageType) {
			t.Fatalf("%q is refused to a read-only session", messageType)
		}
	}
}

func TestValidClientMessageRejectsOversizedAndUnknownInput(t *testing.T) {
	valid := []clientMessage{
		{Type: "subscribe", Project: "project"},
		{Type: "presence"},
		{Type: "lock", NodeID: "node"},
		{Type: "unlock", NodeID: "node"},
		{Type: "awareness", Payload: "AQID"},
		{Type: "graph-drag", IDs: []string{"alpha"}, DX: 1, DY: -1},
	}
	for _, message := range valid {
		if !validClientMessage(message) {
			t.Fatalf("valid message rejected: %+v", message)
		}
	}
	invalid := []clientMessage{
		{Type: "subscribe"},
		{Type: "lock"},
		{Type: "unknown"},
		{Type: "presence", NodeID: string(make([]byte, 257))},
		{Type: "subscribe", Project: "bad\nname"},
		{Type: "awareness"},
		{Type: "awareness", Payload: strings.Repeat("A", maxAwarenessBytes+1)},
		{Type: "graph-drag"},
		{Type: "graph-drag", IDs: make([]string, maxDragIDs+1)},
		{Type: "graph-drag", IDs: []string{"alpha"}, DX: math.NaN()},
		{Type: "graph-drag", IDs: []string{"alpha"}, DY: math.Inf(1)},
		{Type: "graph-drag", IDs: []string{"bad\nid"}},
	}
	for _, message := range invalid {
		if validClientMessage(message) {
			t.Fatalf("invalid message accepted: %+v", message)
		}
	}
}

func TestValidClientMessageBoundsTheLiveDocumentMessages(t *testing.T) {
	valid := []clientMessage{
		{Type: "doc-open", NodeID: "alpha"},
		{Type: "doc-open", NodeID: "alpha", PageID: "page"},
		{Type: "doc-close", NodeID: "alpha"},
		{Type: "doc-update", NodeID: "alpha", Payload: "AQID"},
		{Type: "doc-update", NodeID: "alpha", Payload: strings.Repeat("A", maxDocUpdateBytes)},
		{Type: "doc-snapshot", NodeID: "alpha", Payload: strings.Repeat("A", maxDocSnapshotBytes)},
		{Type: "doc-compact-request", NodeID: "alpha"},
		{Type: "doc-snapshot-start", NodeID: "alpha", Token: "1", Size: maxDocSnapshotTotalBytes, Chunks: maxDocSnapshotChunks},
		{Type: "doc-snapshot-chunk", NodeID: "alpha", Token: "1", Seq: maxDocSnapshotChunks - 1, Payload: strings.Repeat("A", maxDocSnapshotBytes)},
		{Type: "doc-snapshot-commit", NodeID: "alpha", Token: "1"},
		{Type: "doc-snapshot-abort", NodeID: "alpha", Token: "1"},
		{Type: "doc-flushed", NodeID: "alpha", Rev: "0a1b2c3d4e5f"},
	}
	for _, message := range valid {
		if !validClientMessage(message) {
			t.Fatalf("valid message rejected: %.80v", message)
		}
	}
	invalid := []clientMessage{
		{Type: "doc-open"},
		{Type: "doc-close"},
		{Type: "doc-open", NodeID: "alpha", PageID: string(make([]byte, 257))},
		{Type: "doc-open", NodeID: "alpha", PageID: "bad\npage"},
		{Type: "doc-update", NodeID: "alpha"},
		{Type: "doc-update", Payload: "AQID"},
		{Type: "doc-update", NodeID: "alpha", Payload: strings.Repeat("A", maxDocUpdateBytes+1)},
		{Type: "doc-snapshot", NodeID: "alpha", Payload: strings.Repeat("A", maxDocSnapshotBytes+1)},
		{Type: "doc-snapshot", NodeID: "alpha"},
		{Type: "doc-compact-request"},
		{Type: "doc-snapshot-start", NodeID: "alpha", Token: "1", Size: maxDocSnapshotTotalBytes + 1, Chunks: maxDocSnapshotChunks},
		{Type: "doc-snapshot-start", NodeID: "alpha", Token: "1", Size: maxDocSnapshotTotalBytes, Chunks: maxDocSnapshotChunks + 1},
		{Type: "doc-snapshot-start", NodeID: "alpha", Token: "01", Size: 1, Chunks: 1},
		{Type: "doc-snapshot-start", NodeID: "alpha", Token: "0", Size: 1, Chunks: 1},
		{Type: "doc-snapshot-start", NodeID: "alpha", Token: "1", Size: maxDocSnapshotBytes + 1, Chunks: 1},
		{Type: "doc-snapshot-chunk", NodeID: "alpha", Token: "1", Seq: maxDocSnapshotChunks, Payload: "x"},
		{Type: "doc-snapshot-chunk", NodeID: "alpha", Token: "1", Seq: 0, Payload: strings.Repeat("A", maxDocSnapshotBytes+1)},
		{Type: "doc-snapshot-commit", NodeID: "alpha", Token: "01"},
		{Type: "doc-snapshot-abort", NodeID: "alpha"},
		{Type: "doc-flushed", NodeID: "alpha"},
		{Type: "doc-flushed", NodeID: "alpha", Rev: "not-a-revision"},
		{Type: "doc-flushed", NodeID: "alpha", Rev: strings.Repeat("a", maxDocRevBytes+1)},
	}
	for _, message := range invalid {
		if validClientMessage(message) {
			t.Fatalf("invalid message accepted: %.80v", message)
		}
	}
}

func TestALiveDocumentIsReadableByAVisitorButNotWritable(t *testing.T) {
	mutations := []string{"doc-update", "doc-snapshot", "doc-flushed", "doc-compact-request", "doc-snapshot-start", "doc-snapshot-chunk", "doc-snapshot-commit", "doc-snapshot-abort"}
	for _, messageType := range mutations {
		if !mutatesSharedState(messageType) {
			t.Fatalf("%q is not refused to a read-only session", messageType)
		}
	}
	for _, messageType := range []string{"doc-open", "doc-close"} {
		if mutatesSharedState(messageType) {
			t.Fatalf("%q is refused to a read-only session", messageType)
		}
	}
	for _, messageType := range append([]string{"doc-open", "doc-close"}, mutations...) {
		if !isPayload(messageType) {
			t.Fatalf("%q is charged to the control budget", messageType)
		}
	}
}

func TestDocumentUpdateAndFollowingBarrierShareOneOutboundFIFO(t *testing.T) {
	hub := NewHub()
	owner := docClient(hub, "aaa", identity.RoleMember)
	peer := docClient(hub, "bbb", identity.RoleMember)
	hub.openDoc(owner, "alpha")
	hub.openDoc(peer, "alpha")
	docEvents(t, owner)
	docEvents(t, peer)

	hub.docUpdate(peer, "alpha", "PREFIX")
	hub.docCompactRequest(owner, "alpha")
	events := docEvents(t, owner)
	if len(events) < 2 || events[0].Type != "doc-update" || events[0].Payload != "PREFIX" || events[1].Type != "doc-compact" || !validDocToken(events[1].Token) {
		t.Fatalf("document FIFO = %+v", events)
	}
}

// docClient installs a connected client the session code can be driven with.
func docClient(hub *Hub, id string, role identity.Role) *wsClient {
	client := &wsClient{
		hub:      hub,
		outbound: make(chan clientOutbound, 512),
		done:     make(chan struct{}),
		id:       id,
		actor:    identity.Actor{ID: id, Name: id, Role: role},
		room:     "room",
	}
	hub.mu.Lock()
	hub.conns[client] = struct{}{}
	hub.mu.Unlock()
	return client
}

// commitDocSnapshot drives the current server-issued barrier protocol without
// bypassing ownership, accounting, chunk ordering, or persistence.
func commitDocSnapshot(hub *Hub, client *wsClient, key, payload string) string {
	hub.docCompactRequest(client, key)
	hub.mu.Lock()
	session := hub.docs[client.room][key]
	token := ""
	if session != nil && session.compaction != nil && session.compaction.client == client {
		token = session.compaction.token
	}
	hub.mu.Unlock()
	if token == "" {
		return ""
	}
	chunks := (len(payload) + maxDocSnapshotBytes - 1) / maxDocSnapshotBytes
	hub.docSnapshotStart(client, key, clientMessage{Token: token, Size: len(payload), Chunks: chunks})
	for seq, offset := 0, 0; offset < len(payload); seq, offset = seq+1, offset+maxDocSnapshotBytes {
		end := min(offset+maxDocSnapshotBytes, len(payload))
		hub.docSnapshotChunk(client, key, clientMessage{Token: token, Seq: seq, Payload: payload[offset:end]})
	}
	hub.docSnapshotCommit(client, key, clientMessage{Token: token})
	return token
}

// docEvents drains what a client has been queued so far.
func docEvents(t *testing.T, client *wsClient) []Event {
	t.Helper()
	var events []Event
	for {
		select {
		case out := <-client.outbound:
			if out.snapshot != nil {
				for _, event := range snapshotEvents(*out.snapshot) {
					events = append(events, event)
				}
				client.hub.releaseSnapshotJob(out.snapshot)
				continue
			}
			var event Event
			if err := json.Unmarshal(out.payload, &event); err != nil {
				t.Fatalf("unmarshal event: %v", err)
			}
			events = append(events, event)
		default:
			return events
		}
	}
}

func firstEvent(t *testing.T, client *wsClient, kind string) Event {
	t.Helper()
	for _, event := range docEvents(t, client) {
		if event.Type == kind {
			return event
		}
	}
	t.Fatalf("no %q event was queued for %s", kind, client.id)
	return Event{}
}

func TestRemovingClientPreventsAnInFlightDocumentJoin(t *testing.T) {
	room := t.TempDir()
	hub := NewHub()
	client := docClientIn(hub, "aaa", room)
	entered := make(chan struct{})
	resume := make(chan struct{})
	hub.openDocBeforeJoin = func() {
		close(entered)
		<-resume
	}
	done := make(chan struct{})
	go func() {
		hub.openDoc(client, "alpha")
		close(done)
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("open did not reach its final-join barrier")
	}
	// remove completes its session scan while open is still between recovery
	// and commit. once would prevent a later cleanup of any ghost it admitted.
	hub.remove(client)
	close(resume)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("open did not resume after removal")
	}
	hub.mu.Lock()
	session := hub.docs[room]["alpha"]
	_, connected := hub.conns[client]
	hub.mu.Unlock()
	if connected || session != nil {
		t.Fatalf("removed client rejoined as a ghost: connected=%v session=%+v", connected, session)
	}
}

func TestRemovedClientCannotAcquireALock(t *testing.T) {
	hub := NewHub()
	client := docClient(hub, "aaa", identity.RoleMember)
	hub.remove(client)
	hub.acquireLock(client, "alpha", false)
	hub.mu.Lock()
	lock := hub.locks[client.room]["alpha"]
	hub.mu.Unlock()
	if lock != nil {
		t.Fatalf("removed client acquired ghost lock: %+v", lock)
	}
}

func TestDocumentLeadershipGoesToTheLowestWriterAndMovesOnLeave(t *testing.T) {
	hub := NewHub()
	visitor := docClient(hub, "aaa", identity.RoleVisitor)
	late := docClient(hub, "ccc", identity.RoleMember)
	early := docClient(hub, "bbb", identity.RoleMember)

	hub.openDoc(late, "alpha")
	if state := firstEvent(t, late, "doc-state"); !state.Leader || !state.Seed {
		t.Fatalf("the first opener was not told to seed and lead: %+v", state)
	}
	// The visitor cannot be handed the file to write even though its id sorts
	// first, and its arrival changes nobody's answer.
	hub.openDoc(visitor, "alpha")
	if state := firstEvent(t, visitor, "doc-state"); state.Leader || state.Seed {
		t.Fatalf("a visitor was made leader or told to seed: %+v", state)
	}
	if events := docEvents(t, late); len(events) != 0 {
		t.Fatalf("leadership was re-announced without changing: %+v", events)
	}

	hub.openDoc(early, "alpha")
	if state := firstEvent(t, early, "doc-state"); !state.Leader || state.Seed {
		t.Fatalf("the lower writer did not take leadership: %+v", state)
	}
	if leader := firstEvent(t, late, "doc-leader"); leader.Leader {
		t.Fatal("the displaced leader was not told it had lost the file")
	}

	hub.closeDoc(early, "alpha")
	if leader := firstEvent(t, late, "doc-leader"); !leader.Leader {
		t.Fatal("leadership was not handed back when the leader left")
	}
	if events := docEvents(t, visitor); len(events) != 0 {
		t.Fatalf("a visitor was told about an election it can never win: %+v", events)
	}
}

func TestASnapshotReplacesTheStoredLog(t *testing.T) {
	hub := NewHub()
	client := docClient(hub, "aaa", identity.RoleMember)
	hub.openDoc(client, "alpha")
	for index := 0; index < 8; index++ {
		hub.docUpdate(client, "alpha", "AAAA")
	}
	commitDocSnapshot(hub, client, "alpha", "SNAPSHOT")

	hub.mu.Lock()
	session := hub.docs["room"]["alpha"]
	hub.mu.Unlock()
	if len(session.log) != 1 || session.log[0] != "SNAPSHOT" || session.logBytes != len("SNAPSHOT") {
		t.Fatalf("the log was not replaced: %+v (%d bytes)", session.log, session.logBytes)
	}

	// A late joiner replays the snapshot and nothing before it, and is told not
	// to seed: the snapshot already is the document.
	joiner := docClient(hub, "bbb", identity.RoleMember)
	hub.openDoc(joiner, "alpha")
	events := docEvents(t, joiner)
	if len(events) != 4 || events[0].Type != "doc-state" || events[0].Seed ||
		events[1].Type != "doc-snapshot-start" || !validDocToken(events[1].Token) ||
		events[2].Type != "doc-snapshot-chunk" || events[2].Payload != "SNAPSHOT" ||
		events[3].Type != "doc-snapshot-commit" || events[3].Token != events[1].Token {
		t.Fatalf("a joiner did not replay the snapshot alone: %+v", events)
	}

	// A snapshot is stored and relayed: peers that never see it hold a document
	// the log no longer describes.
	docEvents(t, client)
	token := commitDocSnapshot(hub, joiner, "alpha", "SECOND")
	peerEvents := docEvents(t, client)
	var payload string
	var committed bool
	for _, event := range peerEvents {
		if event.Type == "doc-snapshot-chunk" && event.Token == token {
			payload += event.Payload
		}
		committed = committed || event.Type == "doc-snapshot-commit" && event.Token == token && event.From == joiner.id
	}
	if payload != "SECOND" || !committed {
		t.Fatalf("the snapshot was not relayed to the peers: %+v", peerEvents)
	}
	for _, event := range docEvents(t, joiner) {
		if event.Type == "doc-snapshot-start" || event.Type == "doc-snapshot-chunk" || event.Type == "doc-snapshot-commit" {
			t.Fatalf("a snapshot came back to its sender: %+v", event)
		}
	}
}

func TestAFlushedRevisionSkipsTheClientThatWroteIt(t *testing.T) {
	room := t.TempDir()
	writeNodeFile(t, room, "alpha", "flushed")
	hub := NewHub()
	writer := docClientIn(hub, "aaa", room)
	peer := docClientIn(hub, "bbb", room)
	hub.openDoc(writer, "alpha")
	hub.openDoc(peer, "alpha")
	docEvents(t, writer)
	docEvents(t, peer)

	rev := store.Rev([]byte("flushed"))
	hub.docFlushed(writer, "alpha", rev)
	flushed := firstEvent(t, peer, "doc-flushed")
	if flushed.Rev != rev || flushed.From != writer.id {
		t.Fatalf("doc-flushed = %+v", flushed)
	}
	// The writer already knows which revision it wrote.
	if events := docEvents(t, writer); len(events) != 0 {
		t.Fatalf("the writer was told about its own flush: %+v", events)
	}
}

func TestCompactionIsAskedForOncePerThreshold(t *testing.T) {
	hub := NewHub()
	leader := docClient(hub, "aaa", identity.RoleMember)
	hub.openDoc(leader, "alpha")
	for index := 0; index < docCompactUpdates+20; index++ {
		hub.docUpdate(leader, "alpha", "AAAA")
	}
	compacts := 0
	for _, event := range docEvents(t, leader) {
		if event.Type == "doc-compact" {
			compacts++
		}
	}
	if compacts != 1 {
		t.Fatalf("doc-compact was sent %d times, want once", compacts)
	}

	// The snapshot the leader owes ends the threshold, so crossing it again is
	// a fresh request rather than silence.
	commitDocSnapshot(hub, leader, "alpha", "SNAPSHOT")
	for index := 0; index < docCompactUpdates; index++ {
		hub.docUpdate(leader, "alpha", "AAAA")
	}
	if compact := firstEvent(t, leader, "doc-compact"); compact.ID != "alpha" {
		t.Fatalf("doc-compact = %+v", compact)
	}
}

func TestAnOverflowingLogFreezesAfterKeepingTheCrossingUpdate(t *testing.T) {
	room := t.TempDir()
	writeNodeFile(t, room, "alpha", "base")
	hub := NewHub()
	client := docClientIn(hub, "aaa", room)
	peer := docClientIn(hub, "bbb", room)
	hub.openDoc(client, "alpha")
	hub.openDoc(peer, "alpha")

	chunk := strings.Repeat("A", maxDocUpdateBytes)
	for index := 0; index <= maxDocLogBytes/maxDocUpdateBytes; index++ {
		hub.docUpdate(client, "alpha", chunk)
	}

	hub.mu.Lock()
	session := hub.docs[room]["alpha"]
	hub.mu.Unlock()
	if session == nil || !session.frozen || session.logBytes <= maxDocLogBytes {
		t.Fatalf("overflow was not retained as a frozen session: %+v", session)
	}
	// Everyone is warned, but nobody is globally reopened: the update that
	// crossed the limit was accepted and must remain available for a snapshot.
	leaderEvents := docEvents(t, client)
	var leaderWarning, leaderCompact bool
	for _, event := range leaderEvents {
		leaderWarning = leaderWarning || event.Type == "doc-persistence-error" && event.ID == "alpha"
		leaderCompact = leaderCompact || event.Type == "doc-compact" && event.ID == "alpha"
	}
	if !leaderWarning {
		t.Fatalf("leader persistence warning = %+v", leaderEvents)
	}
	if !leaderCompact {
		t.Fatalf("leader compact request = %+v", leaderEvents)
	}
	peerEvents := docEvents(t, peer)
	var peerWarning bool
	for _, event := range peerEvents {
		peerWarning = peerWarning || event.Type == "doc-persistence-error" && event.ID == "alpha"
	}
	if !peerWarning {
		t.Fatalf("peer persistence warning = %+v", peerEvents)
	}
	// A later incremental update stays local for export/copy and is not relayed
	// while the leader owes a snapshot.
	docEvents(t, peer)
	hub.docUpdate(peer, "alpha", "not relayed")
	for _, event := range docEvents(t, client) {
		if event.Type == "doc-update" && event.Payload == "not relayed" {
			t.Fatalf("frozen update reached another member: %+v", event)
		}
	}
	if warning := firstEvent(t, peer, "doc-persistence-error"); warning.ID != "alpha" {
		t.Fatalf("frozen sender was not warned: %+v", warning)
	}
	// A persisted full snapshot is the explicit recovery point.
	commitDocSnapshot(hub, client, "alpha", "SNAPSHOT")
	hub.mu.Lock()
	frozen := hub.docs[room]["alpha"].frozen
	hub.mu.Unlock()
	if frozen {
		t.Fatal("successful snapshot left the session frozen")
	}
	if restored := firstEvent(t, peer, "doc-persistence-restored"); restored.ID != "alpha" {
		t.Fatalf("snapshot did not restore persistence status: %+v", restored)
	}
}

func TestAnyWritableMemberCanCommitAndLargeBaseDoesNotConsumeDeltaBudget(t *testing.T) {
	hub := NewHub()
	leader := docClient(hub, "aaa", identity.RoleMember)
	member := docClient(hub, "bbb", identity.RoleMember)
	hub.openDoc(leader, "alpha")
	hub.openDoc(member, "alpha")
	docEvents(t, leader)
	docEvents(t, member)

	payload := strings.Repeat("S", maxDocLogBytes+1)
	if token := commitDocSnapshot(hub, member, "alpha", payload); token == "" {
		t.Fatal("non-leader writer was not issued a compaction token")
	}
	hub.docUpdate(member, "alpha", "tail")

	hub.mu.Lock()
	session := hub.docs["room"]["alpha"]
	count, bytes := session.deltaStatsLocked()
	frozen := session.frozen
	hub.mu.Unlock()
	if frozen || count != 1 || bytes != len("tail") {
		t.Fatalf("large base consumed tail capacity: frozen=%v count=%d bytes=%d", frozen, count, bytes)
	}
}

func TestSnapshotOwnerDisconnectReassignsFrozenCompactionAndReleasesUpload(t *testing.T) {
	hub := NewHub()
	owner := docClient(hub, "aaa", identity.RoleMember)
	next := docClient(hub, "bbb", identity.RoleMember)
	hub.openDoc(owner, "alpha")
	hub.openDoc(next, "alpha")
	docEvents(t, owner)
	docEvents(t, next)

	hub.mu.Lock()
	session := hub.docs["room"]["alpha"]
	session.frozen = true
	oldToken := hub.beginCompactionLocked(session, owner, time.Now())
	hub.mu.Unlock()
	hub.docSnapshotStart(owner, "alpha", clientMessage{Token: oldToken, Size: 64, Chunks: 1})
	hub.mu.Lock()
	timer := session.compaction.timer
	hub.mu.Unlock()
	hub.closeDoc(owner, "alpha")

	hub.mu.Lock()
	active := owner.snapshotActive
	bytes := hub.snapshotBytes
	compaction := session.compaction
	hub.mu.Unlock()
	if active != "" || bytes != 0 || compaction == nil || compaction.client != next || compaction.token == oldToken {
		t.Fatalf("owner handoff leaked or reused transfer: active=%q bytes=%d compaction=%+v", active, bytes, compaction)
	}
	if timer == nil || timer.Stop() {
		t.Fatal("abandoned snapshot timeout was not stopped during handoff")
	}
	event := firstEvent(t, next, "doc-compact")
	if event.Token != compaction.token || !validDocToken(event.Token) {
		t.Fatalf("replacement compaction = %+v", event)
	}
}

func TestSnapshotAccountingKeepsReplacedBlobUntilQueuedReplayDrains(t *testing.T) {
	hub := NewHub()
	sender := docClient(hub, "aaa", identity.RoleMember)
	peer := docClient(hub, "bbb", identity.RoleMember)
	hub.openDoc(sender, "alpha")
	hub.openDoc(peer, "alpha")
	docEvents(t, sender)
	docEvents(t, peer)

	first := strings.Repeat("A", 1024)
	second := strings.Repeat("B", 2048)
	commitDocSnapshot(hub, sender, "alpha", first)
	commitDocSnapshot(hub, sender, "alpha", second)
	hub.mu.Lock()
	beforeDrain := hub.snapshotBytes
	hub.mu.Unlock()
	if beforeDrain != len(first)+len(second) {
		t.Fatalf("queued replacement accounting = %d, want %d", beforeDrain, len(first)+len(second))
	}
	docEvents(t, peer)
	hub.mu.Lock()
	afterDrain := hub.snapshotBytes
	hub.mu.Unlock()
	if afterDrain != len(second) {
		t.Fatalf("drained accounting = %d, want current %d", afterDrain, len(second))
	}
}

func TestSnapshotChunksOnlyBypassRateLimitForActiveExpectedSequence(t *testing.T) {
	hub := NewHub()
	client := docClient(hub, "aaa", identity.RoleMember)
	hub.openDoc(client, "alpha")
	token := commitTokenOnly(hub, client, "alpha")
	hub.docSnapshotStart(client, "alpha", clientMessage{Token: token, Size: 2, Chunks: 1})
	if !hub.allowSnapshotChunk(client, "alpha", token, 0) {
		t.Fatal("active first chunk was not authorized")
	}
	if hub.allowSnapshotChunk(client, "alpha", token, 1) || hub.allowSnapshotChunk(client, "beta", token, 0) || hub.allowSnapshotChunk(client, "alpha", "999", 0) {
		t.Fatal("foreign or out-of-order chunk bypassed the stream rate limit")
	}
	hub.docSnapshotAbort(client, "alpha", clientMessage{Token: token})
	if hub.allowSnapshotChunk(client, "alpha", token, 0) {
		t.Fatal("aborted token remained authorized")
	}
}

func TestSnapshotStartUsesTheOriginalTokenDeadline(t *testing.T) {
	hub := NewHub()
	client := docClient(hub, "aaa", identity.RoleMember)
	hub.openDoc(client, "alpha")
	token := commitTokenOnly(hub, client, "alpha")
	hub.mu.Lock()
	hub.docs["room"]["alpha"].compaction.until = time.Now().Add(25 * time.Millisecond)
	hub.mu.Unlock()
	hub.docSnapshotStart(client, "alpha", clientMessage{Token: token, Size: 64, Chunks: 1})
	if ready := firstEvent(t, client, "doc-snapshot-ready"); ready.Token != token {
		t.Fatalf("snapshot ready = %+v", ready)
	}
	deadline := time.Now().Add(time.Second)
	for {
		hub.mu.Lock()
		bytes := hub.snapshotBytes
		active := client.snapshotActive
		compaction := hub.docs["room"]["alpha"].compaction
		hub.mu.Unlock()
		if bytes == 0 && active == "" && compaction == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("near-expiry reservation survived: bytes=%d active=%q compaction=%+v", bytes, active, compaction)
		}
		time.Sleep(time.Millisecond)
	}
	if rejected := firstEvent(t, client, "doc-snapshot-rejected"); rejected.Token != token {
		t.Fatalf("timeout rejection = %+v", rejected)
	}
}

func TestSnapshotBudgetRejectKeepsTokenAndConnectionRetryable(t *testing.T) {
	hub := NewHub()
	client := docClient(hub, "aaa", identity.RoleMember)
	hub.openDoc(client, "alpha")
	token := commitTokenOnly(hub, client, "alpha")
	docEvents(t, client)
	// A structurally invalid direct call is rejected before any byte or fixed
	// cadence is charged. The wire validator normally prevents this call, but
	// the handler remains fail-safe on its own.
	hub.docSnapshotStart(client, "alpha", clientMessage{Token: token})
	docEvents(t, client)
	hub.mu.Lock()
	invalidActorCadences := len(hub.snapshotActorCadence)
	invalidDocCadences := len(hub.snapshotDocCadence)
	hub.mu.Unlock()
	if invalidActorCadences != 0 || invalidDocCadences != 0 || !client.snapshotAt.IsZero() {
		t.Fatalf("invalid rejection charged budget: actorCadences=%d docCadences=%d byteBudgetAt=%v", invalidActorCadences, invalidDocCadences, client.snapshotAt)
	}
	hub.mu.Lock()
	hub.snapshotBytes = maxHubSnapshotBytes
	hub.mu.Unlock()
	start := clientMessage{Token: token, Size: 64, Chunks: 1}
	hub.docSnapshotStart(client, "alpha", start)
	events := docEvents(t, client)
	var rejected, warned bool
	for _, event := range events {
		rejected = rejected || event.Type == "doc-snapshot-rejected" && event.Token == token
		warned = warned || event.Type == "doc-persistence-error"
	}
	hub.mu.Lock()
	_, connected := hub.conns[client]
	active := client.snapshotActive
	actorCadences := len(hub.snapshotActorCadence)
	docCadences := len(hub.snapshotDocCadence)
	hub.snapshotBytes = 0
	hub.mu.Unlock()
	if !rejected || !warned || !connected || active != "" || actorCadences != 0 || docCadences != 0 || !client.snapshotAt.IsZero() {
		t.Fatalf("budget rejection = events=%+v connected=%v active=%q actorCadences=%d docCadences=%d byteBudgetAt=%v", events, connected, active, actorCadences, docCadences, client.snapshotAt)
	}
	// Same token and socket can start once capacity returns; no reconnect or new
	// document session is required.
	hub.docSnapshotStart(client, "alpha", start)
	if ready := firstEvent(t, client, "doc-snapshot-ready"); ready.Token != token {
		t.Fatalf("retry ready = %+v", ready)
	}
	hub.docSnapshotAbort(client, "alpha", clientMessage{Token: token})
}

func TestSnapshotCadenceIsSharedByActorAndDocument(t *testing.T) {
	now := time.Unix(100, 0)
	docA := docPersistenceKey{room: "room", key: "alpha"}
	docB := docPersistenceKey{room: "room", key: "beta"}

	actorHub := NewHub()
	for range wsSnapshotCadenceBurst {
		if !actorHub.canSnapshotCadenceLocked("actor-a", docA, now) {
			t.Fatal("actor cadence rejected its initial burst")
		}
		actorHub.chargeSnapshotCadenceLocked("actor-a", docA, now)
	}
	if actorHub.canSnapshotCadenceLocked("actor-a", docB, now) {
		t.Fatal("a fresh document bypassed the actor-wide cadence")
	}

	docHub := NewHub()
	for index := range wsSnapshotCadenceBurst {
		actor := fmt.Sprintf("actor-%d", index)
		if !docHub.canSnapshotCadenceLocked(actor, docA, now) {
			t.Fatal("document cadence rejected its initial burst")
		}
		docHub.chargeSnapshotCadenceLocked(actor, docA, now)
	}
	if docHub.canSnapshotCadenceLocked("fresh-actor", docA, now) {
		t.Fatal("a fresh actor bypassed the document-wide cadence")
	}
	if !actorHub.canSnapshotCadenceLocked("actor-a", docB, now.Add(time.Second)) {
		t.Fatal("actor cadence did not refill at one accepted transfer per second")
	}
	if !docHub.canSnapshotCadenceLocked("fresh-actor", docA, now.Add(2*time.Second)) {
		t.Fatal("document cadence did not refill at one accepted transfer per two seconds")
	}
}

func commitTokenOnly(hub *Hub, client *wsClient, key string) string {
	hub.docCompactRequest(client, key)
	hub.mu.Lock()
	defer hub.mu.Unlock()
	return hub.docs[client.room][key].compaction.token
}

func TestSessionLimitsHoldForParticipantsAndSessions(t *testing.T) {
	hub := NewHub()
	for index := 0; index < maxDocParticipants; index++ {
		member := docClient(hub, fmt.Sprintf("m%03d", index), identity.RoleMember)
		hub.openDoc(member, "alpha")
	}
	overflow := docClient(hub, "zzz", identity.RoleMember)
	hub.openDoc(overflow, "alpha")
	state := firstEvent(t, overflow, "doc-state")
	if !state.Seed || state.Leader {
		t.Fatalf("the participant that did not fit was not sent to the file read-only: %+v", state)
	}
	hub.mu.Lock()
	members := len(hub.docs["room"]["alpha"].members)
	hub.mu.Unlock()
	if members != maxDocParticipants {
		t.Fatalf("participants = %d, want %d", members, maxDocParticipants)
	}

	opener := docClient(hub, "aaa", identity.RoleMember)
	// One session already exists above, so this fills the rest of the table.
	for index := 0; index < maxDocSessions-1; index++ {
		hub.openDoc(opener, fmt.Sprintf("doc-%03d", index))
	}
	docEvents(t, opener)
	hub.openDoc(opener, "one-too-many")
	if state := firstEvent(t, opener, "doc-state"); !state.Seed || !state.Leader {
		t.Fatalf("the client that found no room was not left alone with the file: %+v", state)
	}
	hub.mu.Lock()
	sessions := hub.docSessionCountLocked()
	hub.mu.Unlock()
	if sessions != maxDocSessions {
		t.Fatalf("sessions = %d, want %d", sessions, maxDocSessions)
	}
}

func TestAnEmptySessionSurvivesAReloadAndNotAnAbandonment(t *testing.T) {
	hub := NewHub()
	client := docClient(hub, "aaa", identity.RoleMember)
	hub.openDoc(client, "alpha")
	hub.docUpdate(client, "alpha", "AAAA")
	hub.closeDoc(client, "alpha")

	start := time.Now()
	hub.sweepDocSessions(start.Add(docSessionGrace - time.Second))
	hub.mu.Lock()
	survived := hub.docs["room"]["alpha"] != nil
	hub.mu.Unlock()
	if !survived {
		t.Fatal("a session was dropped inside the reload window")
	}

	hub.sweepDocSessions(start.Add(docSessionGrace + time.Second))
	hub.mu.Lock()
	remaining := hub.docSessionCountLocked()
	hub.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("an abandoned session was kept: %d remain", remaining)
	}
}
