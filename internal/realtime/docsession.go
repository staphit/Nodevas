// Live documents: the part of the hub that keeps a CRDT session alive while
// people type into the same node, and hands one of them the job of writing the
// file. The server never parses an update — it stores the bytes, relays them,
// and asks the leader for a fresh snapshot when the pile gets big.
//
// One thing this server cannot check is nonetheless part of the wire format:
// the document's text lives at doc.getText("text"). Two clients that disagree
// on that key exchange updates all day and each read an empty document, with
// no error anywhere to say why.

package realtime

import (
	"errors"
	"log"
	"strconv"
	"strings"
	"time"
)

const (
	// The frame limit is 64 KB and both of these have to fit under it with room
	// for the envelope. An update gets the same ceiling as a snapshot because a
	// Yjs update cannot be split: one paste produces one update, and merging
	// two of them only makes a bigger one.
	maxDocUpdateBytes        = 48 << 10
	maxDocSnapshotBytes      = 48 << 10
	maxDocSnapshotTotalBytes = 24 << 20
	maxDocSnapshotChunks     = 512
	docSnapshotTimeout       = 120 * time.Second
	maxDocRevBytes           = 64

	maxDocSessions     = 64
	maxDocParticipants = 16

	// The log is what a late joiner replays, so its two bounds have different
	// jobs: the compaction thresholds are where the leader is asked for a
	// snapshot, and maxDocLogBytes is the hard ceiling that holds even when the
	// leader never answers. The byte threshold sits well under the ceiling on
	// purpose — asking for a snapshot at the exact point the log has to go
	// would mean the session is always torn down before it can arrive.
	docCompactUpdates   = 256
	docCompactBytes     = 512 << 10
	maxDocLogBytes      = 1 << 20
	maxDocDeltaEntries  = 4096
	maxHubSnapshotBytes = 128 << 20

	// An empty session is kept alive for a moment because a reload is a leave
	// and a join a second apart, and dropping it in between would make the
	// reloading client seed from a file the session has not written yet.
	docSessionGrace = 60 * time.Second
)

// docSession is one document being edited by one room. The stored log is the
// document while the session lives; the file is the document once it dies,
// which is why the leader's job is to keep the two from drifting far apart.
type docSession struct {
	members map[*wsClient]struct{}
	leader  *wsClient

	log      []string
	logBytes int
	// generation changes whenever the document state changes. A flush reads
	// the file without holding the hub lock and may only commit its result if
	// it still describes the same in-memory state it started with.
	generation uint64
	// persistenceDegraded means the latest sidecar attempt failed. The next
	// successful attempt tells active members that crash recovery is healthy
	// again without exposing a filesystem error over the wire.
	persistenceDegraded bool
	// frozen stops ordinary incremental updates after the log crosses its hard
	// ceiling. The crossing update remains recoverable; a full snapshot resumes
	// the session.
	frozen bool
	// compacted stops doc-compact from being sent on every update once the log
	// is over the threshold: the leader needs time to build a snapshot, and a
	// request per keystroke would be the flood the snapshot is meant to end.
	compacted bool
	// When the last participant left, so the sweep can tell a session that is
	// between reloads from one that is over.
	emptyAt time.Time
	// The file revision this state is consistent with: what the opener found on
	// disk, or what the leader last reported flushing. It is what a stored
	// session is measured against after a restart, and it is the entire
	// staleness rule — see docstore.go.
	fileRev    string
	compaction *docCompaction
	// snapshot is the materialized base at log[0]. Its independently ref-counted
	// payload is shared by the session and every queued peer replay.
	snapshot *docSnapshotBlob
}

type docCompaction struct {
	token     string
	client    *wsClient
	prefixLen int
	until     time.Time
	assembly  *docSnapshotAssembly
	timer     *time.Timer
}

type docSnapshotAssembly struct {
	size   int
	chunks int
	next   int
	data   strings.Builder
}

func (h *Hub) clearCompactionLocked(session *docSession) {
	if session.compaction == nil {
		session.compacted = false
		return
	}
	compaction := session.compaction
	if compaction.timer != nil {
		compaction.timer.Stop()
		compaction.timer = nil
	}
	if compaction.assembly != nil {
		h.snapshotBytes -= compaction.assembly.size
		if h.snapshotBytes < 0 {
			panic("realtime snapshot accounting underflow")
		}
	}
	if compaction.client != nil && compaction.client.snapshotActive == compaction.token {
		compaction.client.snapshotActive = ""
		compaction.client.snapshotKey = ""
		compaction.client.snapshotNext = 0
	}
	session.compaction = nil
	session.compacted = false
}

func (h *Hub) expireCompactionLocked(session *docSession, now time.Time) {
	if session.compaction != nil && !session.compaction.until.After(now) {
		h.clearCompactionLocked(session)
	}
}

func (h *Hub) nextDocTokenLocked() string {
	h.nextDocToken++
	if h.nextDocToken == 0 {
		h.nextDocToken++
	}
	return strconv.FormatUint(h.nextDocToken, 10)
}

func (session *docSession) deltaStartLocked() int {
	if session.snapshot != nil && len(session.log) > 0 {
		return 1
	}
	return 0
}

func (session *docSession) deltaStatsLocked() (count, bytes int) {
	start := session.deltaStartLocked()
	count = len(session.log) - start
	bytes = session.logBytes
	if start == 1 {
		bytes -= len(session.log[0])
	}
	return count, bytes
}

func (h *Hub) beginCompactionLocked(session *docSession, client *wsClient, now time.Time) string {
	token := h.nextDocTokenLocked()
	session.compaction = &docCompaction{
		token: token, client: client, prefixLen: len(session.log), until: now.Add(docSnapshotTimeout),
	}
	session.compacted = true
	return token
}

func writableMemberLocked(session *docSession, client *wsClient) bool {
	if session == nil {
		return false
	}
	if _, present := session.members[client]; !present {
		return false
	}
	client.mu.Lock()
	writable := client.actor.CanWrite()
	client.mu.Unlock()
	return writable
}

// docKey names the document a message is about. This is wire format, not an
// internal detail: it comes back as the id on every doc-* event and is what the
// browser routes on, so changing the shape stops routing in every tab that is
// already open, silently and only for those tabs.
//
//	<nodeId>            the node's own document
//	<nodeId>/<pageId>   one of its subpages
//
// A node's document and each of its subpages are separate sessions. Sharing one
// would apply each document's updates to the other, and neither text survives
// that.
func docKey(message clientMessage) string {
	if message.PageID == "" {
		return message.NodeID
	}
	return message.NodeID + "/" + message.PageID
}

// splitDocKey takes a key back apart to find the file behind it. The first
// separator is the boundary: a node id cannot contain one, and a page id is not
// trusted not to.
func splitDocKey(key string) (node, page string) {
	node, page, _ = strings.Cut(key, "/")
	return node, page
}

// leaderNotice is one client and the answer it is about to be given, collected
// under the hub lock and delivered outside it.
type leaderNotice struct {
	client *wsClient
	leader bool
}

// elect picks the leader: the lowest client id among the participants that may
// write. Lowest id rather than oldest join because every participant computes
// the same answer from the same set, and a set has no arrival order.
func (session *docSession) elect() {
	var chosen *wsClient
	for member := range session.members {
		member.mu.Lock()
		actor := member.actor
		member.mu.Unlock()
		if !actor.CanWrite() {
			continue
		}
		if chosen == nil || member.id < chosen.id {
			chosen = member
		}
	}
	session.leader = chosen
}

// leaderNotices names the clients whose answer to "am I the leader" changed.
// skip is the client that is being told in a doc-state anyway, which would
// otherwise get the same answer twice and treat the second as a change.
func (session *docSession) leaderNotices(previous, skip *wsClient) []leaderNotice {
	if previous == session.leader {
		return nil
	}
	var notices []leaderNotice
	if previous != nil && previous != skip {
		if _, present := session.members[previous]; present {
			notices = append(notices, leaderNotice{client: previous, leader: false})
		}
	}
	if session.leader != nil && session.leader != skip {
		notices = append(notices, leaderNotice{client: session.leader, leader: true})
	}
	return notices
}

func (h *Hub) docSessionCountLocked() int {
	total := 0
	for _, sessions := range h.docs {
		total += len(sessions)
	}
	return total
}

// lockDocPersistence orders every state change and sidecar mutation for one
// room/document key. The registry entry outlives any individual docSession
// while a holder or waiter exists, so an old session cannot clear or overwrite
// a sidecar after a replacement session has started using the same key.
//
// Callers must never wait here while holding h.mu. Different documents use
// different locks, and the reference count removes idle keys from the Hub.
func (h *Hub) lockDocPersistence(room, key string) func() {
	registryKey := docPersistenceKey{room: room, key: key}
	h.docPersistGuard.Lock()
	persistence := h.docPersist[registryKey]
	if persistence == nil {
		persistence = &docPersistence{}
		h.docPersist[registryKey] = persistence
	}
	persistence.refs++
	h.docPersistGuard.Unlock()

	persistence.mu.Lock()
	return func() {
		persistence.mu.Unlock()

		h.docPersistGuard.Lock()
		persistence.refs--
		if persistence.refs == 0 && h.docPersist[registryKey] == persistence {
			delete(h.docPersist, registryKey)
		}
		h.docPersistGuard.Unlock()
	}
}

// sweepDocSessions drops the sessions nobody has come back to. Takes the clock
// as an argument so the lifetime can be tested without waiting a minute for it.
func (h *Hub) sweepDocSessions(now time.Time) {
	type candidate struct {
		room    string
		key     string
		session *docSession
	}

	h.mu.Lock()
	var candidates []candidate
	for room, sessions := range h.docs {
		for key, session := range sessions {
			h.expireCompactionLocked(session, now)
			if len(session.members) > 0 || session.emptyAt.IsZero() {
				continue
			}
			if now.Sub(session.emptyAt) >= docSessionGrace {
				candidates = append(candidates, candidate{room: room, key: key, session: session})
			}
		}
	}
	h.mu.Unlock()

	for _, candidate := range candidates {
		unlockPersistence := h.lockDocPersistence(candidate.room, candidate.key)
		h.mu.Lock()
		sessions := h.docs[candidate.room]
		session := sessions[candidate.key]
		ready := session == candidate.session && len(session.members) == 0 && !session.emptyAt.IsZero() && now.Sub(session.emptyAt) >= docSessionGrace
		var card docSidecar
		if ready {
			card = session.sidecarLocked()
		}
		h.mu.Unlock()
		if ready {
			if err := h.persistDoc(candidate.room, candidate.key, card); err != nil {
				h.resetEmptyDoc(candidate.room, candidate.key, session, now)
				h.persistenceStatus(candidate.room, candidate.key, session, err)
			} else {
				h.deleteEmptyDoc(candidate.room, candidate.key, session)
			}
		}
		unlockPersistence()
	}
}

// retryEmptyDocSessions is the shutdown form of sweep: every retained empty
// session gets one more persistence attempt immediately, so a second Close
// after a repaired filesystem can finish the cleanup.
func (h *Hub) retryEmptyDocSessions() error {
	h.mu.Lock()
	type candidate struct {
		room    string
		key     string
		session *docSession
	}
	var candidates []candidate
	for room, sessions := range h.docs {
		for key, session := range sessions {
			if len(session.members) == 0 {
				candidates = append(candidates, candidate{room, key, session})
			}
		}
	}
	h.mu.Unlock()

	var errs []error
	for _, candidate := range candidates {
		unlockPersistence := h.lockDocPersistence(candidate.room, candidate.key)
		h.mu.Lock()
		session := h.docs[candidate.room][candidate.key]
		ready := session == candidate.session && len(session.members) == 0
		var card docSidecar
		if ready {
			card = session.sidecarLocked()
		}
		h.mu.Unlock()
		if ready {
			if err := h.persistDoc(candidate.room, candidate.key, card); err != nil {
				errs = append(errs, err)
				h.resetEmptyDoc(candidate.room, candidate.key, session, time.Now())
				h.persistenceStatus(candidate.room, candidate.key, session, err)
			} else {
				h.deleteEmptyDoc(candidate.room, candidate.key, session)
			}
		}
		unlockPersistence()
	}
	return errors.Join(errs...)
}

func (h *Hub) resetEmptyDoc(room, key string, expected *docSession, now time.Time) {
	h.mu.Lock()
	if session := h.docs[room][key]; session == expected && len(session.members) == 0 {
		session.emptyAt = now
	}
	h.mu.Unlock()
}

func (h *Hub) deleteEmptyDoc(room, key string, expected *docSession) {
	h.mu.Lock()
	sessions := h.docs[room]
	if session := sessions[key]; session == expected && len(session.members) == 0 {
		h.clearCompactionLocked(session)
		h.releaseSnapshotBlobLocked(session.snapshot)
		session.snapshot = nil
		delete(sessions, key)
		delete(h.snapshotDocCadence, docPersistenceKey{room: room, key: key})
		if len(sessions) == 0 {
			delete(h.docs, room)
		}
	}
	h.mu.Unlock()
}

func (h *Hub) openDoc(client *wsClient, key string) {
	if key == "" {
		return
	}
	client.mu.Lock()
	room := client.room
	client.mu.Unlock()

	h.sweepDocSessions(time.Now())
	unlockPersistence := h.lockDocPersistence(room, key)

	h.mu.Lock()
	absent := h.docs[room][key] == nil
	h.mu.Unlock()

	// Read what the last session left before taking the lock for real: it is a
	// file read of up to a megabyte, and every broadcast in the process queues
	// behind this mutex. The answer is only used if the document is still
	// unopened when the lock comes back, because a session that appeared in the
	// meantime already is the document.
	var recovered []string
	var fileRev string
	var recoveredSnapshot bool
	var recoveredReservation int
	var recoveryBlocked bool
	if absent {
		recovered, fileRev, recoveredSnapshot, recoveredReservation, recoveryBlocked = h.recoverDoc(room, key)
	}
	if h.openDocBeforeJoin != nil {
		h.openDocBeforeJoin()
	}

	h.mu.Lock()
	releaseRecoveryLocked := func() {
		if recoveredReservation == 0 {
			return
		}
		h.snapshotBytes -= recoveredReservation
		recoveredReservation = 0
	}
	// A reader can be removed while it is recovering a sidecar or waiting for
	// this document turn. Its once-only removal cleanup has already scanned
	// sessions, so accepting it after that point would make an unremovable
	// ghost member/leader. Commit the join only while it remains live.
	if h.closed {
		releaseRecoveryLocked()
		h.mu.Unlock()
		unlockPersistence()
		return
	}
	if _, live := h.conns[client]; !live {
		releaseRecoveryLocked()
		h.mu.Unlock()
		unlockPersistence()
		return
	}
	session := h.docs[room][key]
	if session != nil {
		releaseRecoveryLocked()
	}
	if session == nil {
		if recoveryBlocked {
			h.mu.Unlock()
			unlockPersistence()
			h.sendTo(client, Event{Type: "doc-persistence-error", ID: key})
			return
		}
		if h.docSessionCountLocked() >= maxDocSessions {
			recoverable := len(recovered) > 0
			releaseRecoveryLocked()
			h.mu.Unlock()
			unlockPersistence()
			if recoverable {
				// Never tell this client to seed from an older file while a valid
				// sidecar contains newer state. Keeping it unready is inconvenient;
				// overwriting recoverable work would be irreversible.
				h.sendTo(client, Event{Type: "doc-persistence-error", ID: key})
				return
			}
			// No session to join means no relay and no election, so the client
			// is told to seed and to lead: alone with the file is worse than
			// collaborating, and far better than a document nobody saves.
			h.sendTo(client, Event{Type: "doc-state", ID: key, Seed: true, Leader: true})
			return
		}
		if h.docs[room] == nil {
			h.docs[room] = map[string]*docSession{}
		}
		session = &docSession{members: map[*wsClient]struct{}{}, fileRev: fileRev}
		// A recovered log makes this client a joiner rather than a seeder: the
		// stored updates are the document, and the file is older than they are.
		for _, update := range recovered {
			session.log = append(session.log, update)
			session.logBytes += len(update)
		}
		if recoveredSnapshot && len(recovered) > 0 {
			if recoveredReservation == 0 && h.snapshotBytes+len(recovered[0]) > maxHubSnapshotBytes {
				h.mu.Unlock()
				unlockPersistence()
				h.sendTo(client, Event{Type: "doc-persistence-error", ID: key})
				return
			}
			session.snapshot = &docSnapshotBlob{payload: recovered[0]}
			if recoveredReservation != 0 {
				session.snapshot.refs = 1
				recoveredReservation = 0 // transferred to the session blob
			} else {
				h.retainSnapshotBlobLocked(session.snapshot)
			}
		}
		deltaCount, deltaBytes := session.deltaStatsLocked()
		session.frozen = deltaBytes > maxDocLogBytes || deltaCount >= maxDocDeltaEntries
		session.persistenceDegraded = session.frozen
		h.docs[room][key] = session
	}
	_, joined := session.members[client]
	if !joined && session.snapshot != nil {
		tailBytes := 0
		for _, update := range session.log[1:] {
			tailBytes += len(update)
		}
		if h.snapshotBytes+tailBytes > maxHubSnapshotBytes {
			h.mu.Unlock()
			unlockPersistence()
			h.sendTo(client, Event{Type: "doc-persistence-error", ID: key})
			return
		}
	}
	if !joined && len(session.members) >= maxDocParticipants {
		h.mu.Unlock()
		unlockPersistence()
		// The session already has a leader saving the file, so the one who
		// could not get in reads from the file and is told not to write.
		h.sendTo(client, Event{Type: "doc-state", ID: key, Seed: true})
		return
	}
	// Only the client that finds the document empty of both people and history
	// may load it from the file. Everyone else is told the updates that follow
	// are the whole document, including the case of a session whose first
	// opener has seeded but not yet sent anything: the file would arrive twice.
	seed := len(session.members) == 0 && len(session.log) == 0
	session.members[client] = struct{}{}
	session.emptyAt = time.Time{}
	previous := session.leader
	session.elect()
	leads := session.leader == client
	degraded := session.persistenceDegraded
	frozen := session.frozen
	notices := session.leaderNotices(previous, client)
	var slow []*wsClient
	if !queueEvent(client, Event{Type: "doc-state", ID: key, Seed: seed, Leader: leads}) {
		slow = append(slow, client)
	}
	// The state frame and the logical snapshot job enter one FIFO while this
	// document turn is still exclusive. A later update therefore cannot overtake
	// a late joiner's base or its retained tail.
	if session.snapshot != nil {
		token := h.nextDocTokenLocked()
		tail := append([]string(nil), session.log[1:]...)
		if !h.queueSnapshotLocked(client, docOutboundSnapshot{id: key, token: token, blob: session.snapshot, tail: tail}) {
			slow = append(slow, client)
		}
	} else {
		for _, update := range session.log {
			if !queueEvent(client, Event{Type: "doc-update", ID: key, Payload: update}) {
				slow = append(slow, client)
				break
			}
		}
	}
	if degraded || frozen {
		if !queueEvent(client, Event{Type: "doc-persistence-error", ID: key}) {
			slow = append(slow, client)
		}
	}
	if frozen && session.leader != nil {
		token := ""
		if session.compaction == nil {
			token = h.beginCompactionLocked(session, session.leader, time.Now())
		} else {
			token = session.compaction.token
		}
		if !queueEvent(session.leader, Event{Type: "doc-compact", ID: key, Token: token}) {
			slow = append(slow, session.leader)
		}
	}
	for _, notice := range notices {
		if !queueEvent(notice.client, Event{Type: "doc-leader", ID: key, Leader: notice.leader}) {
			slow = append(slow, notice.client)
		}
	}
	h.mu.Unlock()
	unlockPersistence()
	for _, target := range slow {
		h.remove(target)
	}
}

func (h *Hub) closeDoc(client *wsClient, key string) {
	if key == "" {
		return
	}
	client.mu.Lock()
	room := client.room
	client.mu.Unlock()

	unlockPersistence := h.lockDocPersistence(room, key)
	h.mu.Lock()
	session := h.docs[room][key]
	if session == nil {
		h.mu.Unlock()
		unlockPersistence()
		return
	}
	notices, emptied := session.dropLocked(client, time.Now())
	if emptied || (session.compaction != nil && session.compaction.client == client) {
		h.clearCompactionLocked(session)
	}
	var compactClient *wsClient
	var compactToken string
	if !emptied && session.frozen && session.compaction == nil && session.leader != nil {
		compactClient = session.leader
		compactToken = h.beginCompactionLocked(session, compactClient, time.Now())
	}
	var card docSidecar
	if emptied {
		card = session.sidecarLocked()
	}
	h.mu.Unlock()

	if emptied {
		if err := h.persistDoc(room, key, card); err != nil {
			h.resetEmptyDoc(room, key, session, time.Now())
			h.persistenceStatus(room, key, session, err)
			unlockPersistence()
			h.sendTo(client, Event{Type: "doc-persistence-error", ID: key})
			h.announceLeader(key, notices)
			if compactClient != nil {
				h.sendTo(compactClient, Event{Type: "doc-compact", ID: key, Token: compactToken})
			}
			return
		}
		_, restored := h.persistenceStatus(room, key, session, nil)
		if restored != "" {
			unlockPersistence()
			h.sendTo(client, Event{Type: restored, ID: key})
			h.announceLeader(key, notices)
			if compactClient != nil {
				h.sendTo(compactClient, Event{Type: "doc-compact", ID: key, Token: compactToken})
			}
			return
		}
	}
	unlockPersistence()
	h.announceLeader(key, notices)
	if compactClient != nil {
		h.sendTo(compactClient, Event{Type: "doc-compact", ID: key, Token: compactToken})
	}
}

// dropLocked removes one participant and re-elects. The session itself stays:
// its log is the document until the grace period says otherwise. The second
// result is whether that participant was the last one, which is the moment the
// state has to reach disk — after this nothing changes it, and nothing but the
// grace period stands between it and being forgotten.
func (session *docSession) dropLocked(client *wsClient, now time.Time) ([]leaderNotice, bool) {
	if _, present := session.members[client]; !present {
		return nil, false
	}
	delete(session.members, client)
	previous := session.leader
	session.elect()
	emptied := len(session.members) == 0
	if emptied {
		session.emptyAt = now
	}
	return session.leaderNotices(previous, client), emptied
}

// sidecarLocked copies out what this session would need to come back.
func (session *docSession) sidecarLocked() docSidecar {
	return docSidecar{rev: session.fileRev, updates: append([]string(nil), session.log...), snapshot: session.snapshot != nil}
}

// freezeDoc is the sticky warning used when the log needs a full snapshot.
// A persistence error is logged locally, never included in the websocket
// event.
func (h *Hub) freezeDoc(room, key string, expected *docSession, err error) []*wsClient {
	h.mu.Lock()
	session := h.docs[room][key]
	if session != expected {
		h.mu.Unlock()
		return nil
	}
	if err != nil {
		log.Printf("crdt persistence %s: %v", key, err)
	}
	session.frozen = true
	session.persistenceDegraded = true
	targets := session.allLocked()
	h.mu.Unlock()
	return targets
}

// leaveAllDocs takes a disconnecting client out of every session it was in.
// Nothing else knows which documents a connection had open, so a leader that
// goes away without saying so would keep the leadership it can no longer use.
func (h *Hub) leaveAllDocs(client *wsClient) error {
	now := time.Now()
	type candidate struct {
		room string
		key  string
	}

	h.mu.Lock()
	var candidates []candidate
	for room, sessions := range h.docs {
		for key, session := range sessions {
			if _, present := session.members[client]; present {
				candidates = append(candidates, candidate{room: room, key: key})
			}
		}
	}
	h.mu.Unlock()

	pending := map[string][]leaderNotice{}
	type compactNotice struct {
		key, token string
		client     *wsClient
	}
	var compacts []compactNotice
	var errs []error
	for _, candidate := range candidates {
		unlockPersistence := h.lockDocPersistence(candidate.room, candidate.key)
		h.mu.Lock()
		session := h.docs[candidate.room][candidate.key]
		var card docSidecar
		var emptied bool
		if session != nil {
			var notices []leaderNotice
			notices, emptied = session.dropLocked(client, now)
			if emptied || (session.compaction != nil && session.compaction.client == client) {
				h.clearCompactionLocked(session)
			}
			if !emptied && session.frozen && session.compaction == nil && session.leader != nil {
				compacts = append(compacts, compactNotice{
					key: candidate.key, client: session.leader,
					token: h.beginCompactionLocked(session, session.leader, now),
				})
			}
			if len(notices) > 0 {
				pending[candidate.key] = append(pending[candidate.key], notices...)
			}
			if emptied {
				card = session.sidecarLocked()
			}
		}
		h.mu.Unlock()

		// A connection that dropped is the commonest way a session ends, and
		// the one nobody arranged: whatever the leader had not flushed is only
		// here. The key lock keeps a reopen behind this write.
		if emptied {
			if err := h.persistDoc(candidate.room, candidate.key, card); err != nil {
				errs = append(errs, err)
				h.resetEmptyDoc(candidate.room, candidate.key, session, now)
				h.persistenceStatus(candidate.room, candidate.key, session, err)
			} else {
				// A disconnect has no live recipient for a restoration event, but
				// clearing the sticky state here makes the next opener accurate.
				h.persistenceStatus(candidate.room, candidate.key, session, nil)
			}
		}
		unlockPersistence()
	}
	for key, notices := range pending {
		h.announceLeader(key, notices)
	}
	for _, notice := range compacts {
		h.sendTo(notice.client, Event{Type: "doc-compact", ID: notice.key, Token: notice.token})
	}
	return errors.Join(errs...)
}

func (h *Hub) announceLeader(key string, notices []leaderNotice) {
	for _, notice := range notices {
		h.sendTo(notice.client, Event{Type: "doc-leader", ID: key, Leader: notice.leader})
	}
}

// persistenceStatus changes the sticky recovery warning for a live session.
// The wire stays deliberately generic: filesystem details are only for logs
// and shutdown errors, never for websocket clients.
func (h *Hub) persistenceStatus(room, key string, expected *docSession, err error) ([]*wsClient, string) {
	h.mu.Lock()
	session := h.docs[room][key]
	if session != expected {
		h.mu.Unlock()
		return nil, ""
	}
	if err != nil {
		log.Printf("crdt persistence %s: %v", key, err)
		session.persistenceDegraded = true
		targets := session.allLocked()
		h.mu.Unlock()
		return targets, "doc-persistence-error"
	}
	// A frozen log is intentionally quarantined even when its sidecar write
	// succeeded. Only a snapshot or verified flush clears that warning.
	if session.frozen {
		h.mu.Unlock()
		return nil, ""
	}
	if !session.persistenceDegraded {
		h.mu.Unlock()
		return nil, ""
	}
	session.persistenceDegraded = false
	targets := session.allLocked()
	h.mu.Unlock()
	return targets, "doc-persistence-restored"
}

func (h *Hub) announcePersistence(key, eventType string, targets []*wsClient) {
	for _, target := range targets {
		h.sendTo(target, Event{Type: eventType, ID: key})
	}
}

func (h *Hub) docUpdate(client *wsClient, key, payload string) {
	client.mu.Lock()
	room := client.room
	client.mu.Unlock()

	unlockPersistence := h.lockDocPersistence(room, key)
	h.mu.Lock()
	session := h.docs[room][key]
	if session == nil {
		h.mu.Unlock()
		unlockPersistence()
		return
	}
	if !writableMemberLocked(session, client) {
		// An update from outside the session has nowhere to be stored and no
		// audience: doc-open is what buys a place in both.
		h.mu.Unlock()
		unlockPersistence()
		return
	}
	if session.frozen {
		token := ""
		if session.compaction == nil {
			token = h.beginCompactionLocked(session, client, time.Now())
		} else if session.compaction.client == client {
			token = session.compaction.token
		}
		slow := !queueEvent(client, Event{Type: "doc-persistence-error", ID: key})
		if token != "" && !queueEvent(client, Event{Type: "doc-compact", ID: key, Token: token}) {
			slow = true
		}
		h.mu.Unlock()
		unlockPersistence()
		if slow {
			h.remove(client)
		}
		return
	}
	session.log = append(session.log, payload)
	session.logBytes += len(payload)
	session.generation++
	deltaCount, deltaBytes := session.deltaStatsLocked()
	if deltaBytes > maxDocLogBytes || deltaCount >= maxDocDeltaEntries {
		// Preserve the one legal message that crossed the line. It is already
		// within maxDocUpdateBytes, and the sidecar envelope allows exactly that
		// bounded excess. Further updates wait for a leader snapshot.
		card := session.sidecarLocked()
		targets := session.othersLocked(client)
		token := ""
		if session.compaction == nil {
			token = h.beginCompactionLocked(session, client, time.Now())
		} else if session.compaction.client == client {
			token = session.compaction.token
		}
		h.mu.Unlock()
		err := h.persistDoc(room, key, card)
		h.mu.Lock()
		var slow []*wsClient
		if h.docs[room][key] == session {
			session.frozen = true
			session.persistenceDegraded = true
			if err != nil {
				log.Printf("crdt persistence %s: %v", key, err)
			}
			for _, target := range targets {
				if !queueEvent(target, Event{Type: "doc-update", ID: key, Payload: payload, From: client.id}) {
					slow = append(slow, target)
				}
			}
			if token != "" && !queueEvent(client, Event{Type: "doc-compact", ID: key, Token: token}) {
				slow = append(slow, client)
			}
			for _, target := range session.allLocked() {
				if !queueEvent(target, Event{Type: "doc-persistence-error", ID: key}) {
					slow = append(slow, target)
				}
			}
		}
		h.mu.Unlock()
		unlockPersistence()
		for _, target := range slow {
			h.remove(target)
		}
		return
	}
	compactToken := ""
	if session.compaction == nil && (deltaCount >= docCompactUpdates || deltaBytes >= docCompactBytes) {
		compactToken = h.beginCompactionLocked(session, client, time.Now())
	}
	targets := session.othersLocked(client)
	var slow []*wsClient
	for _, target := range targets {
		if !queueEvent(target, Event{Type: "doc-update", ID: key, Payload: payload, From: client.id}) {
			slow = append(slow, target)
		}
	}
	if compactToken != "" && !queueEvent(client, Event{Type: "doc-compact", ID: key, Token: compactToken}) {
		slow = append(slow, client)
	}
	h.mu.Unlock()
	unlockPersistence()
	for _, target := range slow {
		h.remove(target)
	}
}

func (h *Hub) docSnapshot(client *wsClient, key, payload string) {
	// Legacy snapshots have no server-issued prefix barrier, so they are only
	// safe as opaque CRDT deltas. Reuse the ordinary append/relay/threshold path
	// exactly: in particular, do not turn every legacy frame into an fsync.
	h.docUpdate(client, key, payload)
}

func (h *Hub) docFlushed(client *wsClient, key, rev string) {
	client.mu.Lock()
	room := client.room
	client.mu.Unlock()

	// Only the elected writer may claim that its in-memory document reached
	// disk. Check that before doing filesystem work, then check it again after:
	// reading the file must not let a leadership change authorize a stale
	// client by accident.
	h.mu.Lock()
	session := h.docs[room][key]
	if session == nil || session.leader != client {
		h.mu.Unlock()
		return
	}
	if _, present := session.members[client]; !present {
		h.mu.Unlock()
		return
	}
	generation := session.generation
	h.mu.Unlock()

	// A revision from the wire is only a claim. Missing files are deliberately
	// rejected here: treating one as Rev(nil) would let a leader erase the sole
	// recoverable sidecar without ever writing the document.
	actual, ok := existingDocFileRev(room, key)
	if !ok || actual != rev {
		return
	}

	unlockPersistence := h.lockDocPersistence(room, key)
	h.mu.Lock()
	if h.docs[room][key] != session || session.leader != client || session.generation != generation {
		// A newer state won the document turn. The file revision was still
		// independently verified, so make that current card recover against it
		// rather than leaving a snapshot tied to the older file revision.
		current := h.docs[room][key]
		if current == session {
			current.fileRev = actual
			card := current.sidecarLocked()
			h.mu.Unlock()
			err := h.persistDoc(room, key, card)
			statusTargets, statusEvent := h.persistenceStatus(room, key, current, err)
			unlockPersistence()
			h.announcePersistence(key, statusEvent, statusTargets)
			return
		}
		h.mu.Unlock()
		unlockPersistence()
		return
	}
	if _, present := session.members[client]; !present {
		h.mu.Unlock()
		unlockPersistence()
		return
	}
	// The revision the stored state is now consistent with: the leader built
	// that file out of this session and the server independently observed the
	// same bytes, so a file still at this revision after a restart is one the
	// session may be put back on top of.
	session.fileRev = actual
	card := session.sidecarLocked()
	targets := session.othersLocked(client)
	h.mu.Unlock()

	// Clear while holding the same per-document ordering lock snapshots use.
	// A newer snapshot may now wait, but once it changes the session its write
	// is guaranteed to happen after this cleanup.
	clearErr := h.persistDoc(room, key, docSidecar{})
	var statusTargets []*wsClient
	var statusEvent string
	persisted := clearErr == nil
	if clearErr != nil {
		// The file is verified, so a same-revision sidecar is safe recovery
		// state when clearing the old one failed. Never discard it silently.
		fallbackErr := h.persistDoc(room, key, card)
		if fallbackErr != nil {
			clearErr = errors.Join(clearErr, fallbackErr)
			statusTargets, statusEvent = h.persistenceStatus(room, key, session, clearErr)
		} else {
			persisted = true
		}
	}
	// The verified file proves this generation reached durable document storage,
	// so it releases the capacity quarantine even if stale-sidecar cleanup also
	// failed. In that failure case persistenceStatus above still leaves the
	// recovery-health warning active.
	h.mu.Lock()
	if h.docs[room][key] == session {
		session.frozen = false
	}
	h.mu.Unlock()
	if persisted {
		statusTargets, statusEvent = h.persistenceStatus(room, key, session, nil)
	}
	unlockPersistence()

	// The writer is left out for the same reason a sender never sees its own
	// update: it already holds the revision it just wrote.
	for _, target := range targets {
		h.sendTo(target, Event{Type: "doc-flushed", ID: key, Rev: actual, From: client.id})
	}
	h.announcePersistence(key, statusEvent, statusTargets)

}

func (h *Hub) docCompactRequest(client *wsClient, key string) {
	client.mu.Lock()
	room := client.room
	client.mu.Unlock()
	unlock := h.lockDocPersistence(room, key)
	h.mu.Lock()
	s := h.docs[room][key]
	now := time.Now()
	if s != nil {
		h.expireCompactionLocked(s, now)
	}
	if !writableMemberLocked(s, client) {
		h.mu.Unlock()
		unlock()
		return
	}
	var token string
	if s.compaction == nil {
		token = h.beginCompactionLocked(s, client, now)
	} else if s.compaction.client == client {
		token = s.compaction.token
	} else {
		slow := !queueEvent(client, Event{Type: "doc-snapshot-rejected", ID: key})
		slow = !queueEvent(client, Event{Type: "doc-persistence-error", ID: key}) || slow
		h.mu.Unlock()
		unlock()
		if slow {
			h.remove(client)
		}
		return
	}
	slow := !queueEvent(client, Event{Type: "doc-compact", ID: key, Token: token})
	h.mu.Unlock()
	unlock()
	if slow {
		h.remove(client)
	}
}

func (h *Hub) docSnapshotStart(client *wsClient, key string, message clientMessage) {
	client.mu.Lock()
	room := client.room
	actorKey := actorConnectionKey(client.actor)
	client.mu.Unlock()
	unlock := h.lockDocPersistence(room, key)
	h.mu.Lock()
	s := h.docs[room][key]
	now := time.Now()
	docCadenceKey := docPersistenceKey{room: room, key: key}
	if s != nil {
		h.expireCompactionLocked(s, now)
	}
	valid := writableMemberLocked(s, client) && s.compaction != nil && s.compaction.client == client &&
		s.compaction.token == message.Token && s.compaction.until.After(now) && s.compaction.assembly == nil &&
		client.snapshotActive == "" && h.snapshotBytes+message.Size <= maxHubSnapshotBytes &&
		client.canSnapshotStart(now, message.Size) && h.canSnapshotCadenceLocked(actorKey, docCadenceKey, now)
	if !valid {
		slow := !queueEvent(client, Event{Type: "doc-snapshot-rejected", ID: key, Token: message.Token})
		slow = !queueEvent(client, Event{Type: "doc-persistence-error", ID: key}) || slow
		h.mu.Unlock()
		unlock()
		if slow {
			h.remove(client)
		}
		return
	}
	client.chargeSnapshotStart(now, message.Size)
	h.chargeSnapshotCadenceLocked(actorKey, docCadenceKey, now)
	h.snapshotBytes += message.Size
	s.compaction.assembly = &docSnapshotAssembly{size: message.Size, chunks: message.Chunks}
	remaining := s.compaction.until.Sub(now)
	client.snapshotActive = message.Token
	client.snapshotKey = key
	client.snapshotNext = 0
	slow := !queueEvent(client, Event{Type: "doc-snapshot-ready", ID: key, Token: message.Token})
	h.mu.Unlock()
	unlock()
	if slow {
		h.remove(client)
		return
	}
	// A quiet client must not reserve memory indefinitely. The timer only
	// clears the matching token, so a later compaction for this key is safe.
	timer := time.AfterFunc(remaining, func() {
		unlock := h.lockDocPersistence(room, key)
		h.mu.Lock()
		var slow bool
		if current := h.docs[room][key]; current != nil && current.compaction != nil && current.compaction.token == message.Token && !current.compaction.until.After(time.Now()) {
			h.clearCompactionLocked(current)
			slow = !queueEvent(client, Event{Type: "doc-snapshot-rejected", ID: key, Token: message.Token})
			slow = !queueEvent(client, Event{Type: "doc-persistence-error", ID: key}) || slow
		}
		h.mu.Unlock()
		unlock()
		if slow {
			h.remove(client)
		}
	})
	h.mu.Lock()
	if current := h.docs[room][key]; current != nil && current.compaction != nil && current.compaction.token == message.Token {
		current.compaction.timer = timer
	} else {
		timer.Stop()
	}
	h.mu.Unlock()
}

func (h *Hub) docSnapshotChunk(client *wsClient, key string, message clientMessage) {
	client.mu.Lock()
	room := client.room
	client.mu.Unlock()
	unlock := h.lockDocPersistence(room, key)
	h.mu.Lock()
	s := h.docs[room][key]
	now := time.Now()
	if s == nil || s.compaction == nil || s.compaction.client != client || s.compaction.token != message.Token || s.compaction.assembly == nil || !s.compaction.until.After(now) {
		if s != nil && s.compaction != nil && s.compaction.client == client && s.compaction.token == message.Token {
			h.clearCompactionLocked(s)
		}
		slow := !queueEvent(client, Event{Type: "doc-snapshot-rejected", ID: key, Token: message.Token})
		slow = !queueEvent(client, Event{Type: "doc-persistence-error", ID: key}) || slow
		h.mu.Unlock()
		unlock()
		if slow {
			h.remove(client)
		}
		return
	}
	a := s.compaction.assembly
	if message.Seq != a.next || len(message.Payload) > maxDocSnapshotBytes || a.data.Len()+len(message.Payload) > a.size {
		h.clearCompactionLocked(s)
		slow := !queueEvent(client, Event{Type: "doc-snapshot-rejected", ID: key, Token: message.Token})
		slow = !queueEvent(client, Event{Type: "doc-persistence-error", ID: key}) || slow
		h.mu.Unlock()
		unlock()
		if slow {
			h.remove(client)
		}
		return
	}
	a.data.WriteString(message.Payload)
	a.next++
	client.snapshotNext = a.next
	h.mu.Unlock()
	unlock()
}

func (h *Hub) docSnapshotAbort(client *wsClient, key string, message clientMessage) {
	client.mu.Lock()
	room := client.room
	client.mu.Unlock()
	unlock := h.lockDocPersistence(room, key)
	h.mu.Lock()
	s := h.docs[room][key]
	var slow bool
	if s != nil && s.compaction != nil && s.compaction.client == client && s.compaction.token == message.Token {
		h.clearCompactionLocked(s)
		slow = !queueEvent(client, Event{Type: "doc-snapshot-rejected", ID: key, Token: message.Token})
		slow = !queueEvent(client, Event{Type: "doc-persistence-error", ID: key}) || slow
	}
	h.mu.Unlock()
	unlock()
	if slow {
		h.remove(client)
	}
}

func (h *Hub) docSnapshotCommit(client *wsClient, key string, message clientMessage) {
	client.mu.Lock()
	room := client.room
	client.mu.Unlock()
	unlock := h.lockDocPersistence(room, key)
	h.mu.Lock()
	s := h.docs[room][key]
	now := time.Now()
	if !writableMemberLocked(s, client) || s.compaction == nil || s.compaction.client != client || s.compaction.token != message.Token || s.compaction.assembly == nil || !s.compaction.until.After(now) {
		if s != nil && s.compaction != nil && s.compaction.client == client && s.compaction.token == message.Token {
			h.clearCompactionLocked(s)
		}
		slow := !queueEvent(client, Event{Type: "doc-snapshot-rejected", ID: key, Token: message.Token})
		slow = !queueEvent(client, Event{Type: "doc-persistence-error", ID: key}) || slow
		h.mu.Unlock()
		unlock()
		if slow {
			h.remove(client)
		}
		return
	}
	c := s.compaction
	a := c.assembly
	if a.next != a.chunks || a.data.Len() != a.size {
		h.clearCompactionLocked(s)
		slow := !queueEvent(client, Event{Type: "doc-snapshot-rejected", ID: key, Token: message.Token})
		slow = !queueEvent(client, Event{Type: "doc-persistence-error", ID: key}) || slow
		h.mu.Unlock()
		unlock()
		if slow {
			h.remove(client)
		}
		return
	}
	payload := a.data.String()
	tail := append([]string(nil), s.log[c.prefixLen:]...)
	bytes := len(payload)
	tailBytes := 0
	for _, update := range tail {
		bytes += len(update)
		tailBytes += len(update)
	}
	if tailBytes > maxDocLogBytes+maxDocUpdateBytes || len(tail) > maxDocDeltaEntries {
		h.clearCompactionLocked(s)
		slow := !queueEvent(client, Event{Type: "doc-snapshot-rejected", ID: key, Token: message.Token})
		slow = !queueEvent(client, Event{Type: "doc-persistence-error", ID: key}) || slow
		h.mu.Unlock()
		unlock()
		if slow {
			h.remove(client)
		}
		return
	}
	// The upload reservation becomes the session's blob reference without
	// changing snapshotBytes. The old blob may remain charged while a slow peer
	// still owns a queued replay job.
	newBlob := &docSnapshotBlob{payload: payload, refs: 1}
	oldBlob := s.snapshot
	if c.timer != nil {
		c.timer.Stop()
		c.timer = nil
	}
	c.assembly = nil
	if client.snapshotActive == message.Token {
		client.snapshotActive = ""
		client.snapshotKey = ""
		client.snapshotNext = 0
	}
	s.log = append([]string{payload}, tail...)
	s.logBytes = bytes
	s.snapshot = newBlob
	h.releaseSnapshotBlobLocked(oldBlob)
	s.generation++
	s.compacted = false
	s.compaction = nil
	card := s.sidecarLocked()
	targets := s.othersLocked(client)
	h.mu.Unlock()
	err := h.persistDoc(room, key, card)
	h.mu.Lock()
	var slow []*wsClient
	if h.docs[room][key] == s {
		if err != nil {
			log.Printf("crdt persistence %s: %v", key, err)
			s.frozen = true
			s.persistenceDegraded = true
			if !queueEvent(client, Event{Type: "doc-snapshot-rejected", ID: key, Token: message.Token}) {
				slow = append(slow, client)
			}
			// Active peers still need the exact CRDT state to converge. Persistence
			// failure freezes future deltas and warns everyone, but withholding the
			// base would make even a later successful retry depend on structs those
			// peers never received.
			for _, target := range targets {
				if !h.queueSnapshotLocked(target, docOutboundSnapshot{id: key, token: message.Token, blob: newBlob, from: client.id}) {
					slow = append(slow, target)
				}
			}
			for _, target := range s.allLocked() {
				if !queueEvent(target, Event{Type: "doc-persistence-error", ID: key}) {
					slow = append(slow, target)
				}
			}
		} else {
			wasDegraded := s.persistenceDegraded
			s.frozen = false
			s.persistenceDegraded = false
			if !queueEvent(client, Event{Type: "doc-snapshot-accepted", ID: key, Token: message.Token}) {
				slow = append(slow, client)
			}
			// Always restore the requester: a transfer reject is a client-local
			// warning even when the document never entered global degraded state.
			if !queueEvent(client, Event{Type: "doc-persistence-restored", ID: key}) {
				slow = append(slow, client)
			}
			for _, target := range targets {
				if !h.queueSnapshotLocked(target, docOutboundSnapshot{id: key, token: message.Token, blob: newBlob, from: client.id}) {
					slow = append(slow, target)
				}
				if wasDegraded && !queueEvent(target, Event{Type: "doc-persistence-restored", ID: key}) {
					slow = append(slow, target)
				}
			}
		}
	}
	h.mu.Unlock()
	unlock()
	for _, target := range slow {
		h.remove(target)
	}
}

func (session *docSession) allLocked() []*wsClient {
	targets := make([]*wsClient, 0, len(session.members))
	for member := range session.members {
		targets = append(targets, member)
	}
	return targets
}

func (session *docSession) othersLocked(except *wsClient) []*wsClient {
	targets := make([]*wsClient, 0, len(session.members))
	for member := range session.members {
		if member == except {
			continue
		}
		targets = append(targets, member)
	}
	return targets
}

// validDocRev bounds the revision a leader reports. It is a content
// fingerprint, so anything that is not short and hexadecimal was not produced
// by this server and has no business being echoed to a room.
func validDocRev(rev string) bool {
	if rev == "" || len(rev) > maxDocRevBytes {
		return false
	}
	return strings.IndexFunc(rev, func(r rune) bool {
		return !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F'))
	}) < 0
}

func validDocToken(token string) bool {
	if token == "" || len(token) > 20 || (len(token) > 1 && token[0] == '0') {
		return false
	}
	value, err := strconv.ParseUint(token, 10, 64)
	return err == nil && value != 0 && strconv.FormatUint(value, 10) == token
}
