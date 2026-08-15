package realtime

import (
	"fmt"
	"nodevas/internal/identity"
	"nodevas/internal/store"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSidecarRejectsZeroLengthAndTooManyDeltas(t *testing.T) {
	rev := store.Rev(nil)
	zero := []byte(docSidecarMagic + " " + rev + " false\n0\n\n")
	if _, err := decodeDocSidecar(zero); err == nil {
		t.Fatal("zero-length delta was accepted")
	}
	updates := make([]string, maxDocDeltaEntries+1)
	for index := range updates {
		updates[index] = "x"
	}
	if _, err := decodeDocSidecar(encodeDocSidecar(docSidecar{rev: rev, updates: updates})); err == nil {
		t.Fatal("sidecar above the delta-entry cap was accepted")
	}
}

func TestLargeSidecarEncodingIsSerialized(t *testing.T) {
	room := t.TempDir()
	hub := NewHub()
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	var concurrent atomic.Int32
	var maximum atomic.Int32
	hub.persistDocBeforeEncode = func() {
		current := concurrent.Add(1)
		for {
			seen := maximum.Load()
			if current <= seen || maximum.CompareAndSwap(seen, current) {
				break
			}
		}
		entered <- struct{}{}
		<-release
		concurrent.Add(-1)
	}
	card := docSidecar{rev: store.Rev(nil), snapshot: true, updates: []string{strings.Repeat("A", maxDocLogBytes+1)}}
	done := make(chan error, 2)
	go func() { done <- hub.persistDoc(room, "alpha", card) }()
	go func() { done <- hub.persistDoc(room, "beta", card) }()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("first encoder did not enter")
	}
	select {
	case <-entered:
		t.Fatal("two large sidecars encoded concurrently")
	case <-time.After(50 * time.Millisecond):
	}
	release <- struct{}{}
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("second encoder did not resume")
	}
	release <- struct{}{}
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if maximum.Load() != 1 {
		t.Fatalf("maximum concurrent encoders = %d", maximum.Load())
	}
}

func TestTinySnapshotsCannotAmplifyPersistenceAcrossActorConnections(t *testing.T) {
	room := t.TempDir()
	writeNodeFile(t, room, "alpha", "base")
	hub := NewHub()
	clients := make([]*wsClient, maxWSConnectionsPerActor)
	for index := range clients {
		client := docClientIn(hub, fmt.Sprintf("connection-%d", index), room)
		client.mu.Lock()
		client.actor = identity.Actor{ID: "shared-actor", Name: "shared-actor", Role: identity.RoleMember}
		client.mu.Unlock()
		hub.openDoc(client, "alpha")
		clients[index] = client
	}
	for _, client := range clients {
		docEvents(t, client)
	}

	var writes atomic.Int32
	hub.persistDocBeforeWrite = func() { writes.Add(1) }
	// Legacy snapshots are barrier-less opaque updates. Even across all eight
	// sockets they relay normally but do not cause one sidecar fsync per frame.
	for _, client := range clients {
		for range 4 {
			hub.docSnapshot(client, "alpha", "L")
		}
	}
	if writes.Load() != 0 {
		t.Fatalf("legacy tiny snapshots caused %d sidecar writes", writes.Load())
	}
	legacyEvents := docEvents(t, clients[0])
	var sawLegacy bool
	for _, event := range legacyEvents {
		sawLegacy = sawLegacy || event.Type == "doc-update" && event.Payload == "L"
	}
	if !sawLegacy {
		t.Fatalf("legacy snapshot did not use ordinary relay: %+v", legacyEvents)
	}
	for _, client := range clients[1:] {
		docEvents(t, client)
	}

	accepted := 0
	for index, client := range clients {
		hub.docCompactRequest(client, "alpha")
		hub.mu.Lock()
		session := hub.docs[room]["alpha"]
		token := ""
		if session.compaction != nil && session.compaction.client == client {
			token = session.compaction.token
		}
		hub.mu.Unlock()
		if token == "" {
			t.Fatalf("connection %d did not receive a compaction token", index)
		}
		hub.docSnapshotStart(client, "alpha", clientMessage{Token: token, Size: 1, Chunks: 1})
		events := docEvents(t, client)
		ready := false
		for _, event := range events {
			ready = ready || event.Type == "doc-snapshot-ready" && event.Token == token
		}
		if !ready {
			// A rejected start keeps the token retryable; explicitly abort so the
			// next connection can exercise the same shared document cadence.
			hub.docSnapshotAbort(client, "alpha", clientMessage{Token: token})
			continue
		}
		accepted++
		hub.docSnapshotChunk(client, "alpha", clientMessage{Token: token, Seq: 0, Payload: "S"})
		hub.docSnapshotCommit(client, "alpha", clientMessage{Token: token})
		for _, member := range clients {
			docEvents(t, member)
		}
	}
	if accepted != wsSnapshotCadenceBurst || writes.Load() != int32(wsSnapshotCadenceBurst) {
		t.Fatalf("tiny snapshot cadence accepted=%d writes=%d, want %d", accepted, writes.Load(), wsSnapshotCadenceBurst)
	}

	// Fixed-cost snapshot throttling is independent of ordinary collaboration;
	// a subsequent delta must still enter the session and relay.
	hub.docUpdate(clients[len(clients)-1], "alpha", "ordinary")
	hub.mu.Lock()
	session := hub.docs[room]["alpha"]
	last := session.log[len(session.log)-1]
	hub.mu.Unlock()
	if last != "ordinary" {
		t.Fatalf("ordinary update after snapshot throttling = %q", last)
	}
	events := docEvents(t, clients[0])
	var relayed bool
	for _, event := range events {
		relayed = relayed || event.Type == "doc-update" && event.Payload == "ordinary"
	}
	if !relayed {
		t.Fatalf("ordinary update was not relayed after snapshot throttling: %+v", events)
	}
}

func TestRecoveredSnapshotUsesBaseMetadataAndDoesNotSeedWhenCapacityIsFull(t *testing.T) {
	room := t.TempDir()
	writeNodeFile(t, room, "alpha", "base")
	hub := NewHub()
	writer := docClientIn(hub, "aaa", room)
	hub.openDoc(writer, "alpha")
	docEvents(t, writer)
	payload := strings.Repeat("S", 2*maxDocLogBytes)
	commitDocSnapshot(hub, writer, "alpha", payload)
	hub.closeDoc(writer, "alpha")

	restarted := NewHub()
	restarted.snapshotBytes = maxHubSnapshotBytes
	var decoded atomic.Int32
	restarted.recoverDocBeforeDecode = func() { decoded.Add(1) }
	opener := docClientIn(restarted, "bbb", room)
	restarted.openDoc(opener, "alpha")
	events := docEvents(t, opener)
	for _, event := range events {
		if event.Type == "doc-state" && event.Seed {
			t.Fatalf("capacity failure seeded over a recoverable sidecar: %+v", events)
		}
	}
	if len(events) != 1 || events[0].Type != "doc-persistence-error" {
		t.Fatalf("capacity failure events = %+v", events)
	}
	if restarted.docs[room] != nil && restarted.docs[room]["alpha"] != nil {
		t.Fatal("capacity-refused recovery installed an unaccounted session")
	}
	if decoded.Load() != 0 {
		t.Fatal("capacity-refused recovery decoded the large sidecar body")
	}

	restarted.snapshotBytes = 0
	restarted.openDoc(opener, "alpha")
	docEvents(t, opener)
	restarted.docUpdate(opener, "alpha", "tail")
	restarted.mu.Lock()
	session := restarted.docs[room]["alpha"]
	count, bytes := session.deltaStatsLocked()
	frozen := session.frozen
	restarted.mu.Unlock()
	if frozen || count != 1 || bytes != len("tail") {
		t.Fatalf("recovered base consumed delta budget: frozen=%v count=%d bytes=%d", frozen, count, bytes)
	}
}

func TestConcurrentLargeRecoveriesAreSerializedAndPreReserved(t *testing.T) {
	room := t.TempDir()
	writer := NewHub()
	const documents = 8
	payload := strings.Repeat("S", 2*maxDocLogBytes)
	for index := range documents {
		key := fmt.Sprintf("doc-%d", index)
		writeNodeFile(t, room, key, "base")
		if err := writer.persistDoc(room, key, docSidecar{
			rev: store.Rev([]byte("base")), snapshot: true, updates: []string{payload},
		}); err != nil {
			t.Fatal(err)
		}
	}

	hub := NewHub()
	entered := make(chan struct{}, documents)
	release := make(chan struct{})
	var concurrent atomic.Int32
	var maximum atomic.Int32
	hub.recoverDocBeforeDecode = func() {
		current := concurrent.Add(1)
		for {
			seen := maximum.Load()
			if current <= seen || maximum.CompareAndSwap(seen, current) {
				break
			}
		}
		entered <- struct{}{}
		<-release
		concurrent.Add(-1)
	}
	done := make(chan struct{}, documents)
	for index := range documents {
		client := docClientIn(hub, fmt.Sprintf("client-%d", index), room)
		key := fmt.Sprintf("doc-%d", index)
		go func() {
			hub.openDoc(client, key)
			done <- struct{}{}
		}()
	}
	for index := range documents {
		select {
		case <-entered:
		case <-time.After(2 * time.Second):
			t.Fatalf("recovery %d did not enter", index)
		}
		select {
		case <-entered:
			t.Fatal("large recovery semaphore admitted two decoders")
		case <-time.After(20 * time.Millisecond):
		}
		release <- struct{}{}
	}
	for range documents {
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("large recovery did not finish")
		}
	}
	if maximum.Load() != 1 {
		t.Fatalf("maximum concurrent large recoveries = %d", maximum.Load())
	}
	hub.mu.Lock()
	accounted := hub.snapshotBytes
	hub.mu.Unlock()
	if accounted != documents*len(payload) {
		t.Fatalf("recovered snapshot accounting = %d, want %d", accounted, documents*len(payload))
	}
}

func TestBarrierCommitRetainsConcurrentTailInSidecarAndLateReplay(t *testing.T) {
	room := t.TempDir()
	writeNodeFile(t, room, "alpha", "base")
	hub := NewHub()
	owner := docClientIn(hub, "aaa", room)
	peer := docClientIn(hub, "bbb", room)
	hub.openDoc(owner, "alpha")
	hub.openDoc(peer, "alpha")
	docEvents(t, owner)
	docEvents(t, peer)
	hub.docUpdate(owner, "alpha", "PREFIX")

	token := commitTokenOnly(hub, owner, "alpha")
	hub.docUpdate(peer, "alpha", "TAIL")
	hub.docSnapshotStart(owner, "alpha", clientMessage{Token: token, Size: len("SNAPSHOT"), Chunks: 1})
	hub.docSnapshotChunk(owner, "alpha", clientMessage{Token: token, Seq: 0, Payload: "SNAPSHOT"})
	hub.docSnapshotCommit(owner, "alpha", clientMessage{Token: token})

	hub.mu.Lock()
	session := hub.docs[room]["alpha"]
	logCopy := append([]string(nil), session.log...)
	hub.mu.Unlock()
	if len(logCopy) != 2 || logCopy[0] != "SNAPSHOT" || logCopy[1] != "TAIL" {
		t.Fatalf("barrier discarded concurrent tail: %q", logCopy)
	}
	data, err := os.ReadFile(sidecarFile(t, room, "alpha"))
	if err != nil {
		t.Fatal(err)
	}
	card, err := decodeDocSidecar(data)
	if err != nil {
		t.Fatal(err)
	}
	if !card.snapshot || len(card.updates) != 2 || card.updates[0] != "SNAPSHOT" || card.updates[1] != "TAIL" {
		t.Fatalf("sidecar barrier = %+v", card)
	}

	hub.closeDoc(owner, "alpha")
	hub.closeDoc(peer, "alpha")
	restarted := NewHub()
	late := docClientIn(restarted, "ccc", room)
	restarted.openDoc(late, "alpha")
	events := docEvents(t, late)
	var sequence []string
	for _, event := range events {
		switch event.Type {
		case "doc-snapshot-start":
			sequence = append(sequence, "start")
		case "doc-snapshot-chunk":
			sequence = append(sequence, event.Payload)
		case "doc-snapshot-commit":
			sequence = append(sequence, "commit")
		case "doc-update":
			sequence = append(sequence, event.Payload)
		}
	}
	want := []string{"start", "SNAPSHOT", "commit", "TAIL"}
	if strings.Join(sequence, "|") != strings.Join(want, "|") {
		t.Fatalf("late replay order = %q, want %q", sequence, want)
	}
}

// docClientIn is docClient in a room that is a real project directory, which is
// the only kind of room anything reaches the disk for.
func docClientIn(hub *Hub, id, room string) *wsClient {
	client := docClient(hub, id, identity.RoleMember)
	client.mu.Lock()
	client.room = room
	client.mu.Unlock()
	return client
}

// writeNodeFile puts a node's document where the store keeps it, so the hub can
// fingerprint the same bytes the store would.
func writeNodeFile(t *testing.T, room, id, content string) {
	t.Helper()
	path := store.NodeDocPath(room, id)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func sidecarFile(t *testing.T, room, key string) string {
	t.Helper()
	return filepath.Join(room, store.DataDir, docSidecarDir, escapeDocKey(key)+docSidecarSuffix)
}

func waitForDocPersistenceRefs(t *testing.T, hub *Hub, room, key string, want int) {
	t.Helper()
	registryKey := docPersistenceKey{room: room, key: key}
	deadline := time.Now().Add(2 * time.Second)
	for {
		hub.docPersistGuard.Lock()
		persistence := hub.docPersist[registryKey]
		refs := 0
		if persistence != nil {
			refs = persistence.refs
		}
		hub.docPersistGuard.Unlock()
		if refs >= want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("document persistence wait did not register: got %d refs, want %d", refs, want)
		}
		runtime.Gosched()
	}
}

func TestASnapshotIsWrittenDownAgainstTheRevisionItBelongsTo(t *testing.T) {
	room := t.TempDir()
	writeNodeFile(t, room, "alpha", "on disk")
	hub := NewHub()
	client := docClientIn(hub, "aaa", room)

	hub.openDoc(client, "alpha")
	hub.docUpdate(client, "alpha", "AAAA")
	commitDocSnapshot(hub, client, "alpha", "SNAPSHOT")

	path := sidecarFile(t, room, "alpha")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("no sidecar was written: %v", err)
	}
	// Windows has no mode bits to speak of, and reports whatever it likes.
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("sidecar mode = %v, want 0600", info.Mode().Perm())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := decodeDocSidecar(data)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	// The snapshot replaced the log, so it is the only thing worth keeping.
	if len(stored.updates) != 1 || stored.updates[0] != "SNAPSHOT" {
		t.Fatalf("stored updates = %v", stored.updates)
	}
	if stored.rev != store.Rev([]byte("on disk")) {
		t.Fatalf("stored revision = %q, want the file's", stored.rev)
	}
}

func TestAFlushedFileLeavesNoSidecarBehind(t *testing.T) {
	room := t.TempDir()
	writeNodeFile(t, room, "alpha", "on disk")
	hub := NewHub()
	client := docClientIn(hub, "aaa", room)

	hub.openDoc(client, "alpha")
	hub.docUpdate(client, "alpha", "AAAA")
	commitDocSnapshot(hub, client, "alpha", "SNAPSHOT")
	path := sidecarFile(t, room, "alpha")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("no sidecar to begin with: %v", err)
	}

	// The leader wrote the file, so it now says what the session says. A
	// sidecar that survived this would speak for the document again if that
	// file were ever restored to an earlier state.
	writeNodeFile(t, room, "alpha", "flushed text")
	hub.docFlushed(client, "alpha", store.Rev([]byte("flushed text")))
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("sidecar survived the flush: %v", err)
	}
}

func TestAFlushWaitsForTheDocumentPersistenceOrder(t *testing.T) {
	room := t.TempDir()
	writeNodeFile(t, room, "alpha", "on disk")
	hub := NewHub()
	leader := docClientIn(hub, "aaa", room)
	hub.openDoc(leader, "alpha")
	commitDocSnapshot(hub, leader, "alpha", "RECOVERABLE")
	sidecar := sidecarFile(t, room, "alpha")
	writeNodeFile(t, room, "alpha", "flushed")

	releaseOlder := hub.lockDocPersistence(room, "alpha")
	released := false
	defer func() {
		if !released {
			releaseOlder()
		}
	}()
	done := make(chan struct{})
	go func() {
		hub.docFlushed(leader, "alpha", store.Rev([]byte("flushed")))
		close(done)
	}()
	waitForDocPersistenceRefs(t, hub, room, "alpha", 2)
	if _, err := os.Stat(sidecar); err != nil {
		t.Fatalf("flush cleared the sidecar before its document turn: %v", err)
	}

	releaseOlder()
	released = true
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("flush did not resume after the document turn was released")
	}
	if _, err := os.Stat(sidecar); !os.IsNotExist(err) {
		t.Fatalf("ordered flush left the sidecar behind: %v", err)
	}
}

func TestANewerSnapshotCannotBeErasedByAnOlderCleanup(t *testing.T) {
	room := t.TempDir()
	writeNodeFile(t, room, "alpha", "on disk")
	hub := NewHub()
	leader := docClientIn(hub, "aaa", room)
	hub.openDoc(leader, "alpha")
	commitDocSnapshot(hub, leader, "alpha", "OLD")
	writeNodeFile(t, room, "alpha", "flushed")

	releaseOlder := hub.lockDocPersistence(room, "alpha")
	released := false
	defer func() {
		if !released {
			releaseOlder()
		}
	}()
	flushDone := make(chan struct{})
	go func() {
		hub.docFlushed(leader, "alpha", store.Rev([]byte("flushed")))
		close(flushDone)
	}()
	waitForDocPersistenceRefs(t, hub, room, "alpha", 2)
	snapshotDone := make(chan struct{})
	go func() {
		commitDocSnapshot(hub, leader, "alpha", "NEW")
		close(snapshotDone)
	}()
	waitForDocPersistenceRefs(t, hub, room, "alpha", 3)

	// Whichever waiter is scheduled first is safe: a flush first clears before
	// the snapshot writes, while a snapshot first changes the generation and
	// makes the older flush fail its final revalidation.
	releaseOlder()
	released = true
	for name, done := range map[string]<-chan struct{}{
		"flush": flushDone, "snapshot": snapshotDone,
	} {
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatalf("%s did not resume after document ordering lock", name)
		}
	}

	updates, _, _, _, _ := hub.recoverDoc(room, "alpha")
	if len(updates) != 1 || updates[0] != "NEW" {
		t.Fatalf("older cleanup erased or replaced the newer snapshot: %q", updates)
	}
	hub.docPersistGuard.Lock()
	remaining := len(hub.docPersist)
	hub.docPersistGuard.Unlock()
	if remaining != 0 {
		t.Fatalf("document persistence registry leaked %d idle entries", remaining)
	}
}

func TestASnapshotDoesNotHoldTheDocumentTurnWhileDroppingASlowPeer(t *testing.T) {
	room := t.TempDir()
	writeNodeFile(t, room, "alpha", "on disk")
	hub := NewHub()
	leader := docClientIn(hub, "aaa", room)
	peer := docClientIn(hub, "bbb", room)
	hub.openDoc(leader, "alpha")
	hub.openDoc(peer, "alpha")

	// A full queue makes sendTo synchronously remove the peer. Removal calls
	// leaveAllDocs, which needs the same document turn snapshot persistence
	// just used; broadcasting before releasing that turn deadlocks here.
	for len(peer.outbound) < cap(peer.outbound) {
		peer.outbound <- clientOutbound{payload: []byte("blocked")}
	}
	done := make(chan struct{})
	go func() {
		commitDocSnapshot(hub, leader, "alpha", "RECOVERABLE")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("snapshot deadlocked while removing a slow peer")
	}

	hub.mu.Lock()
	_, peerPresent := hub.docs[room]["alpha"].members[peer]
	hub.mu.Unlock()
	if peerPresent {
		t.Fatal("slow peer remained in the live document")
	}
	updates, _, _, _, _ := hub.recoverDoc(room, "alpha")
	if len(updates) != 1 || updates[0] != "RECOVERABLE" {
		t.Fatalf("snapshot was not durable before the slow peer was removed: %q", updates)
	}
}

func TestDocumentPersistenceRegistryDrainsAfterConcurrentEarlyReturns(t *testing.T) {
	room := t.TempDir()
	hub := NewHub()
	client := docClientIn(hub, "aaa", room)
	releaseFirst := hub.lockDocPersistence(room, "missing")
	released := false
	defer func() {
		if !released {
			releaseFirst()
		}
	}()

	const waiters = 24
	done := make(chan struct{}, waiters)
	for range waiters {
		go func() {
			hub.docSnapshot(client, "missing", "ignored")
			done <- struct{}{}
		}()
	}
	waitForDocPersistenceRefs(t, hub, room, "missing", waiters+1)
	releaseFirst()
	released = true
	for range waiters {
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("early-return persistence waiter did not finish")
		}
	}

	hub.docPersistGuard.Lock()
	remaining := len(hub.docPersist)
	hub.docPersistGuard.Unlock()
	if remaining != 0 {
		t.Fatalf("document persistence registry leaked %d entries after early returns", remaining)
	}
}

func TestANonLeaderCannotClaimAFlushOrRemoveTheSidecar(t *testing.T) {
	room := t.TempDir()
	writeNodeFile(t, room, "alpha", "on disk")
	hub := NewHub()
	leader := docClientIn(hub, "aaa", room)
	peer := docClientIn(hub, "bbb", room)

	hub.openDoc(leader, "alpha")
	hub.openDoc(peer, "alpha")
	commitDocSnapshot(hub, leader, "alpha", "SNAPSHOT")
	docEvents(t, leader)
	docEvents(t, peer)
	path := sidecarFile(t, room, "alpha")
	writeNodeFile(t, room, "alpha", "peer's claim")

	hub.docFlushed(peer, "alpha", store.Rev([]byte("peer's claim")))
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("non-leader removed the recovery sidecar: %v", err)
	}
	if events := docEvents(t, leader); len(events) != 0 {
		t.Fatalf("non-leader broadcast a flush: %+v", events)
	}
	if got := hub.docs[room]["alpha"].fileRev; got != store.Rev([]byte("on disk")) {
		t.Fatalf("non-leader changed file revision to %q", got)
	}
}

func TestALeaderCannotClaimARevisionTheServerDidNotRead(t *testing.T) {
	room := t.TempDir()
	writeNodeFile(t, room, "alpha", "on disk")
	hub := NewHub()
	leader := docClientIn(hub, "aaa", room)
	peer := docClientIn(hub, "bbb", room)

	hub.openDoc(leader, "alpha")
	hub.openDoc(peer, "alpha")
	commitDocSnapshot(hub, leader, "alpha", "SNAPSHOT")
	docEvents(t, leader)
	docEvents(t, peer)
	path := sidecarFile(t, room, "alpha")

	hub.docFlushed(leader, "alpha", store.Rev([]byte("not on disk")))
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("mismatched revision removed the recovery sidecar: %v", err)
	}
	if events := docEvents(t, peer); len(events) != 0 {
		t.Fatalf("mismatched revision was broadcast: %+v", events)
	}
	if got := hub.docs[room]["alpha"].fileRev; got != store.Rev([]byte("on disk")) {
		t.Fatalf("mismatched revision changed file revision to %q", got)
	}
}

func TestALeaderCannotFlushAMissingFileAsEmpty(t *testing.T) {
	room := t.TempDir()
	hub := NewHub()
	leader := docClientIn(hub, "aaa", room)

	hub.openDoc(leader, "alpha")
	commitDocSnapshot(hub, leader, "alpha", "UNSAVED")
	path := sidecarFile(t, room, "alpha")
	hub.docFlushed(leader, "alpha", store.Rev(nil))
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("missing file was accepted as a flush: %v", err)
	}
	if got := hub.docs[room]["alpha"].fileRev; got != store.Rev(nil) {
		t.Fatalf("missing-file claim changed file revision to %q", got)
	}
}

func TestALeaderCannotFlushThroughASymlinkedDocument(t *testing.T) {
	room := t.TempDir()
	writeNodeFile(t, room, "alpha", "on disk")
	outside := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}

	hub := NewHub()
	leader := docClientIn(hub, "aaa", room)
	hub.openDoc(leader, "alpha")
	commitDocSnapshot(hub, leader, "alpha", "UNTRUSTED")
	sidecar := sidecarFile(t, room, "alpha")
	path := store.NodeDocPath(room, "alpha")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, path); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	hub.docFlushed(leader, "alpha", store.Rev([]byte("outside")))
	if _, err := os.Stat(sidecar); err != nil {
		t.Fatalf("symlinked file was accepted as a flush: %v", err)
	}
}

func TestASidecarWriteDoesNotFollowAProjectSymlink(t *testing.T) {
	room := t.TempDir()
	writeNodeFile(t, room, "alpha", "on disk")
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(room, store.DataDir)); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	hub := NewHub()
	leader := docClientIn(hub, "aaa", room)
	hub.openDoc(leader, "alpha")
	commitDocSnapshot(hub, leader, "alpha", "PRIVATE")
	externalSidecar := filepath.Join(outside, docSidecarDir, escapeDocKey("alpha")+docSidecarSuffix)
	if _, err := os.Lstat(externalSidecar); !os.IsNotExist(err) {
		t.Fatalf("sidecar write escaped the project through a symlink: %v", err)
	}
}

func TestASidecarClearDoesNotFollowALeafSymlink(t *testing.T) {
	room := t.TempDir()
	writeNodeFile(t, room, "alpha", "on disk")
	hub := NewHub()
	leader := docClientIn(hub, "aaa", room)
	hub.openDoc(leader, "alpha")
	commitDocSnapshot(hub, leader, "alpha", "PRIVATE")

	sidecar := sidecarFile(t, room, "alpha")
	if err := os.Remove(sidecar); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "sentinel")
	const sentinel = "must survive"
	if err := os.WriteFile(outside, []byte(sentinel), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, sidecar); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	writeNodeFile(t, room, "alpha", "flushed")
	hub.docFlushed(leader, "alpha", store.Rev([]byte("flushed")))

	data, err := os.ReadFile(outside)
	if err != nil || string(data) != sentinel {
		t.Fatalf("sidecar clear changed the symlink target: data=%q err=%v", data, err)
	}
	if info, err := os.Lstat(sidecar); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("unsafe sidecar leaf was removed: info=%v err=%v", info, err)
	}
}

func TestARestartHandsTheStoredStateToTheFirstClient(t *testing.T) {
	room := t.TempDir()
	writeNodeFile(t, room, "alpha", "on disk")

	hub := NewHub()
	client := docClientIn(hub, "aaa", room)
	hub.openDoc(client, "alpha")
	if state := firstEvent(t, client, "doc-state"); !state.Seed {
		t.Fatalf("the first opener of an unwritten document was not told to seed: %+v", state)
	}
	hub.docUpdate(client, "alpha", "AAAA")
	hub.docUpdate(client, "alpha", "BBBB")
	// The last participant leaving is what puts an unflushed session on disk.
	hub.closeDoc(client, "alpha")

	restarted := NewHub()
	joiner := docClientIn(restarted, "bbb", room)
	restarted.openDoc(joiner, "alpha")
	events := docEvents(t, joiner)
	if len(events) != 3 || events[0].Type != "doc-state" || events[0].Seed {
		t.Fatalf("the first client after a restart was not handed the stored session: %+v", events)
	}
	if events[1].Payload != "AAAA" || events[2].Payload != "BBBB" {
		t.Fatalf("the stored log replayed as %+v", events[1:])
	}
}

// The file is the document at rest. Something that replaced it while nothing
// was running — vim, a checkout, a restored backup — is the newer document, and
// a session put back on top of it would undo the replacement.
func TestASidecarIsDiscardedWhenTheFileChangedUnderneathIt(t *testing.T) {
	room := t.TempDir()
	writeNodeFile(t, room, "alpha", "on disk")

	hub := NewHub()
	client := docClientIn(hub, "aaa", room)
	hub.openDoc(client, "alpha")
	hub.docUpdate(client, "alpha", "AAAA")
	hub.closeDoc(client, "alpha")
	if _, err := os.Stat(sidecarFile(t, room, "alpha")); err != nil {
		t.Fatalf("no sidecar to go stale: %v", err)
	}

	writeNodeFile(t, room, "alpha", "someone else wrote this")

	restarted := NewHub()
	joiner := docClientIn(restarted, "bbb", room)
	restarted.openDoc(joiner, "alpha")
	events := docEvents(t, joiner)
	if len(events) != 1 || events[0].Type != "doc-state" || !events[0].Seed {
		t.Fatalf("a stale session was handed back instead of the file: %+v", events)
	}
	if _, err := os.Stat(sidecarFile(t, room, "alpha")); !os.IsNotExist(err) {
		t.Fatalf("a sidecar that can never be used again was kept: %v", err)
	}
}

func TestACorruptSidecarIsDroppedRatherThanApplied(t *testing.T) {
	room := t.TempDir()
	path := sidecarFile(t, room, "alpha")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("nodevas-crdt v1 0a1b2c3d\n9\nshort"), 0o600); err != nil {
		t.Fatal(err)
	}

	hub := NewHub()
	client := docClientIn(hub, "aaa", room)
	hub.openDoc(client, "alpha")
	if state := firstEvent(t, client, "doc-state"); !state.Seed {
		t.Fatalf("a client was told a corrupt sidecar was its document: %+v", state)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("a corrupt sidecar was kept: %v", err)
	}
}

// A session that ends with nothing in its log has nothing the file does not
// already have, and an older sidecar left next to it would speak for the
// document at the next open.
func TestAnEmptySessionClearsWhatTheLastOneLeft(t *testing.T) {
	room := t.TempDir()
	hub := NewHub()
	writer := docClientIn(hub, "aaa", room)
	hub.openDoc(writer, "alpha")
	hub.docUpdate(writer, "alpha", "AAAA")
	hub.closeDoc(writer, "alpha")
	if _, err := os.Stat(sidecarFile(t, room, "alpha")); err != nil {
		t.Fatalf("no sidecar to clear: %v", err)
	}

	hub.persistDoc(room, "alpha", docSidecar{rev: store.Rev(nil)})
	if _, err := os.Stat(sidecarFile(t, room, "alpha")); !os.IsNotExist(err) {
		t.Fatalf("a session with nothing to recover left a sidecar behind: %v", err)
	}
}

func TestAnEmptySessionIsRetriedBeforeSweepDeletesIt(t *testing.T) {
	room := t.TempDir()
	hub := NewHub()
	client := docClientIn(hub, "aaa", room)
	hub.openDoc(client, "alpha")
	hub.docUpdate(client, "alpha", "AAAA")

	dataDir := filepath.Join(room, store.DataDir)
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dataDir, docSidecarDir)); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	hub.closeDoc(client, "alpha")
	if event := firstEvent(t, client, "doc-persistence-error"); event.ID != "alpha" {
		t.Fatalf("close persistence error = %+v", event)
	}
	hub.mu.Lock()
	retained := hub.docs[room]["alpha"]
	hub.mu.Unlock()
	if retained == nil || len(retained.members) != 0 {
		t.Fatal("failed empty session was deleted")
	}

	if err := os.Remove(filepath.Join(dataDir, docSidecarDir)); err != nil {
		t.Fatal(err)
	}
	hub.sweepDocSessions(time.Now().Add(docSessionGrace + time.Second))
	hub.mu.Lock()
	remaining := hub.docs[room]["alpha"]
	hub.mu.Unlock()
	if remaining != nil {
		t.Fatal("sweep kept a sidecar it successfully persisted")
	}
	updates, _, _, _, _ := hub.recoverDoc(room, "alpha")
	if len(updates) != 1 || updates[0] != "AAAA" {
		t.Fatalf("retried sidecar = %q", updates)
	}
}

func TestCloseIgnoresARepairedDisconnectPersistenceFailure(t *testing.T) {
	room := t.TempDir()
	hub := NewHub()
	client := docClientIn(hub, "aaa", room)
	hub.openDoc(client, "alpha")
	hub.docUpdate(client, "alpha", "AAAA")
	dataDir := filepath.Join(room, store.DataDir)
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	crdtDir := filepath.Join(dataDir, docSidecarDir)
	if err := os.Symlink(t.TempDir(), crdtDir); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	// This is the asynchronous-disconnect path, not doc-close. It fails its
	// first sidecar write and leaves a quarantined empty session behind.
	hub.remove(client)
	hub.mu.Lock()
	retained := hub.docs[room]["alpha"]
	hub.mu.Unlock()
	if retained == nil {
		t.Fatal("failed disconnect did not retain the document for retry")
	}
	if err := os.Remove(crdtDir); err != nil {
		t.Fatal(err)
	}
	hub.sweepDocSessions(time.Now().Add(docSessionGrace + time.Second))
	hub.mu.Lock()
	remaining := hub.docs[room]["alpha"]
	hub.mu.Unlock()
	if remaining != nil {
		t.Fatal("repaired sweep did not finish the retained document")
	}
	if err := hub.Close(); err != nil {
		t.Fatalf("Close returned repaired disconnect history instead of final durability: %v", err)
	}
}

func TestSnapshotReportsPersistenceFailureAndRestoration(t *testing.T) {
	room := t.TempDir()
	hub := NewHub()
	client := docClientIn(hub, "aaa", room)
	hub.openDoc(client, "alpha")
	docEvents(t, client)
	dataDir := filepath.Join(room, store.DataDir)
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dataDir, docSidecarDir)); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	commitDocSnapshot(hub, client, "alpha", "FIRST")
	if event := firstEvent(t, client, "doc-persistence-error"); event.ID != "alpha" {
		t.Fatalf("snapshot persistence error = %+v", event)
	}
	if err := os.Remove(filepath.Join(dataDir, docSidecarDir)); err != nil {
		t.Fatal(err)
	}
	commitDocSnapshot(hub, client, "alpha", "SECOND")
	if event := firstEvent(t, client, "doc-persistence-restored"); event.ID != "alpha" {
		t.Fatalf("snapshot persistence restored = %+v", event)
	}
}

func TestFailedSnapshotPersistenceStillRelaysExactStateAndFreezesPeers(t *testing.T) {
	room := t.TempDir()
	hub := NewHub()
	sender := docClientIn(hub, "aaa", room)
	peer := docClientIn(hub, "bbb", room)
	hub.openDoc(sender, "alpha")
	hub.openDoc(peer, "alpha")
	docEvents(t, sender)
	docEvents(t, peer)
	dataDir := filepath.Join(room, store.DataDir)
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(dataDir, docSidecarDir)); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	token := commitDocSnapshot(hub, sender, "alpha", "EXACT-SNAPSHOT")
	senderEvents := docEvents(t, sender)
	var rejected, senderWarning bool
	for _, event := range senderEvents {
		rejected = rejected || event.Type == "doc-snapshot-rejected" && event.Token == token
		senderWarning = senderWarning || event.Type == "doc-persistence-error"
	}
	peerEvents := docEvents(t, peer)
	var relayed string
	var committed, peerWarning bool
	for _, event := range peerEvents {
		if event.Type == "doc-snapshot-chunk" && event.Token == token {
			relayed += event.Payload
		}
		committed = committed || event.Type == "doc-snapshot-commit" && event.Token == token
		peerWarning = peerWarning || event.Type == "doc-persistence-error"
	}
	if !rejected || !senderWarning || relayed != "EXACT-SNAPSHOT" || !committed || !peerWarning {
		t.Fatalf("failed persistence contract: sender=%+v peer=%+v", senderEvents, peerEvents)
	}
	hub.mu.Lock()
	frozen := hub.docs[room]["alpha"].frozen
	hub.mu.Unlock()
	if !frozen {
		t.Fatal("failed persistence did not freeze dependent deltas")
	}
}

func TestCloseReportsFailureUntilASecondCloseCanRetry(t *testing.T) {
	room := t.TempDir()
	hub := NewHub()
	client := docClientIn(hub, "aaa", room)
	hub.openDoc(client, "alpha")
	hub.docUpdate(client, "alpha", "AAAA")
	dataDir := filepath.Join(room, store.DataDir)
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dataDir, docSidecarDir)); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	hub.closeDoc(client, "alpha")
	if err := hub.Close(); err == nil {
		t.Fatal("Close swallowed the retained sidecar error")
	}
	if err := os.Remove(filepath.Join(dataDir, docSidecarDir)); err != nil {
		t.Fatal(err)
	}
	if err := hub.Close(); err != nil {
		t.Fatalf("second Close did not retry retained sidecar: %v", err)
	}
}

func TestOverflowKeepsThePriorSessionWhenSidecarClearFails(t *testing.T) {
	room := t.TempDir()
	hub := NewHub()
	leader := docClientIn(hub, "aaa", room)
	peer := docClientIn(hub, "bbb", room)
	hub.openDoc(leader, "alpha")
	hub.openDoc(peer, "alpha")
	docEvents(t, leader)
	docEvents(t, peer)
	hub.mu.Lock()
	session := hub.docs[room]["alpha"]
	session.log = []string{"OLD"}
	session.logBytes = maxDocLogBytes
	hub.mu.Unlock()
	dataDir := filepath.Join(room, store.DataDir)
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dataDir, docSidecarDir)); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	hub.docUpdate(leader, "alpha", "REJECTED")
	events := docEvents(t, leader)
	var errored, reopened bool
	for _, event := range events {
		errored = errored || event.Type == "doc-persistence-error" && event.ID == "alpha"
		reopened = reopened || event.Type == "doc-reopen" && event.ID == "alpha"
	}
	if !errored || reopened {
		t.Fatalf("overflow did not retain its sender state safely: %+v", events)
	}
	peerEvents := docEvents(t, peer)
	var peerError bool
	for _, event := range peerEvents {
		peerError = peerError || event.Type == "doc-persistence-error"
		if event.Type == "doc-reopen" {
			t.Fatalf("overflow failure reopened peer: %+v", peerEvents)
		}
	}
	if !peerError {
		t.Fatalf("overflow failure did not warn peer: %+v", peerEvents)
	}
	hub.mu.Lock()
	kept := hub.docs[room]["alpha"]
	hub.mu.Unlock()
	if kept != session || !kept.frozen || kept.logBytes != maxDocLogBytes+len("REJECTED") || len(kept.log) != 2 || kept.log[0] != "OLD" || kept.log[1] != "REJECTED" {
		t.Fatalf("overflow failure did not retain the accepted crossing update: %+v", kept)
	}
}

func TestFrozenOverflowSurvivesRestartAndAsksForCompaction(t *testing.T) {
	room := t.TempDir()
	writeNodeFile(t, room, "alpha", "base")
	hub := NewHub()
	leader := docClientIn(hub, "aaa", room)
	hub.openDoc(leader, "alpha")
	docEvents(t, leader)
	hub.mu.Lock()
	session := hub.docs[room]["alpha"]
	chunk := strings.Repeat("A", maxDocUpdateBytes)
	for remaining := maxDocLogBytes; remaining > 0; remaining -= len(chunk) {
		session.log = append(session.log, chunk[:min(remaining, len(chunk))])
	}
	session.logBytes = maxDocLogBytes
	hub.mu.Unlock()
	hub.docUpdate(leader, "alpha", "LAST")
	hub.closeDoc(leader, "alpha")

	restarted := NewHub()
	reopened := docClientIn(restarted, "aaa", room)
	restarted.openDoc(reopened, "alpha")
	events := docEvents(t, reopened)
	var warning, compact bool
	for _, event := range events {
		warning = warning || event.Type == "doc-persistence-error" && event.ID == "alpha"
		compact = compact || event.Type == "doc-compact" && event.ID == "alpha"
	}
	if !warning || !compact {
		t.Fatalf("recovered frozen session did not warn and compact: %+v", events)
	}
	restarted.mu.Lock()
	frozen := restarted.docs[room]["alpha"].frozen
	restarted.mu.Unlock()
	if !frozen {
		t.Fatal("overflow state was not frozen after restart")
	}
}

func TestVerifiedFlushUnfreezesARecoveredOverflow(t *testing.T) {
	room := t.TempDir()
	writeNodeFile(t, room, "alpha", "base")
	hub := NewHub()
	leader := docClientIn(hub, "aaa", room)
	peer := docClientIn(hub, "bbb", room)
	hub.openDoc(leader, "alpha")
	hub.openDoc(peer, "alpha")
	docEvents(t, leader)
	docEvents(t, peer)
	hub.mu.Lock()
	session := hub.docs[room]["alpha"]
	chunk := strings.Repeat("A", maxDocUpdateBytes)
	for remaining := maxDocLogBytes; remaining > 0; remaining -= len(chunk) {
		session.log = append(session.log, chunk[:min(remaining, len(chunk))])
	}
	session.logBytes = maxDocLogBytes
	hub.mu.Unlock()
	hub.docUpdate(leader, "alpha", "LAST")
	docEvents(t, leader)
	docEvents(t, peer)

	writeNodeFile(t, room, "alpha", "flushed")
	rev := store.Rev([]byte("flushed"))
	hub.docFlushed(leader, "alpha", rev)
	hub.mu.Lock()
	frozen := hub.docs[room]["alpha"].frozen
	hub.mu.Unlock()
	if frozen {
		t.Fatal("verified flush left frozen session quarantined")
	}
	events := docEvents(t, peer)
	var flushed, restored bool
	for _, event := range events {
		flushed = flushed || event.Type == "doc-flushed" && event.Rev == rev
		restored = restored || event.Type == "doc-persistence-restored"
	}
	if !flushed || !restored {
		t.Fatalf("verified flush did not relay and restore: %+v", events)
	}
	if _, err := os.Stat(sidecarFile(t, room, "alpha")); !os.IsNotExist(err) {
		t.Fatalf("verified flush did not clear frozen sidecar: %v", err)
	}
}

func TestVerifiedFlushUnfreezesWhenSidecarCleanupFails(t *testing.T) {
	room := t.TempDir()
	writeNodeFile(t, room, "alpha", "base")
	hub := NewHub()
	leader := docClientIn(hub, "aaa", room)
	peer := docClientIn(hub, "bbb", room)
	hub.openDoc(leader, "alpha")
	hub.openDoc(peer, "alpha")
	hub.mu.Lock()
	session := hub.docs[room]["alpha"]
	session.log = []string{strings.Repeat("A", maxDocUpdateBytes), strings.Repeat("B", maxDocUpdateBytes)}
	session.logBytes = 2 * maxDocUpdateBytes
	session.frozen = true
	session.persistenceDegraded = true
	hub.mu.Unlock()
	if err := hub.persistDoc(room, "alpha", session.sidecarLocked()); err != nil {
		t.Fatal(err)
	}
	crdtDir := filepath.Join(room, store.DataDir, docSidecarDir)
	if err := os.Remove(sidecarFile(t, room, "alpha")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(crdtDir); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), crdtDir); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	docEvents(t, leader)
	docEvents(t, peer)
	writeNodeFile(t, room, "alpha", "flushed")
	hub.docFlushed(leader, "alpha", store.Rev([]byte("flushed")))
	hub.mu.Lock()
	frozen, degraded := session.frozen, session.persistenceDegraded
	hub.mu.Unlock()
	if frozen || !degraded {
		t.Fatalf("verified flush capacity/health state = frozen=%v degraded=%v", frozen, degraded)
	}
	events := docEvents(t, peer)
	var flushed, warned, restored bool
	for _, event := range events {
		flushed = flushed || event.Type == "doc-flushed"
		warned = warned || event.Type == "doc-persistence-error"
		restored = restored || event.Type == "doc-persistence-restored"
	}
	if !flushed || !warned || restored {
		t.Fatalf("cleanup failure did not keep a health warning: %+v", events)
	}
}

func TestStaleFlushCannotRebaseAReplacementSession(t *testing.T) {
	room := t.TempDir()
	writeNodeFile(t, room, "alpha", "base")
	hub := NewHub()
	old := docClientIn(hub, "aaa", room)
	hub.openDoc(old, "alpha")
	commitDocSnapshot(hub, old, "alpha", "OLD")
	writeNodeFile(t, room, "alpha", "flushed")

	release := hub.lockDocPersistence(room, "alpha")
	done := make(chan struct{})
	go func() {
		hub.docFlushed(old, "alpha", store.Rev([]byte("flushed")))
		close(done)
	}()
	waitForDocPersistenceRefs(t, hub, room, "alpha", 2)
	hub.mu.Lock()
	hub.docs[room]["alpha"] = &docSession{members: map[*wsClient]struct{}{}, fileRev: "replacement", log: []string{"NEW"}, logBytes: len("NEW")}
	hub.mu.Unlock()
	release()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("stale flush did not resume")
	}
	data, err := os.ReadFile(sidecarFile(t, room, "alpha"))
	if err != nil {
		t.Fatal(err)
	}
	card, err := decodeDocSidecar(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(card.updates) != 1 || card.updates[0] != "OLD" {
		t.Fatalf("stale flush rewrote replacement sidecar: %+v", card)
	}
}

// The doc key is wire format: the browser computes the same string and routes
// every doc-* event on it.
func TestADocKeyIsTheNodeAndThePageAndNothingElse(t *testing.T) {
	if key := docKey(clientMessage{NodeID: "alpha"}); key != "alpha" {
		t.Fatalf("main document key = %q", key)
	}
	if key := docKey(clientMessage{NodeID: "alpha", PageID: "page1"}); key != "alpha/page1" {
		t.Fatalf("subpage key = %q", key)
	}
	node, page := splitDocKey("alpha/page1")
	if node != "alpha" || page != "page1" {
		t.Fatalf("split = %q, %q", node, page)
	}
}

func TestASubpageIsASessionOfItsOwn(t *testing.T) {
	hub := NewHub()
	client := docClient(hub, "aaa", identity.RoleMember)
	main := docKey(clientMessage{NodeID: "alpha"})
	page := docKey(clientMessage{NodeID: "alpha", PageID: "page1"})

	hub.openDoc(client, main)
	hub.docUpdate(client, main, "MAIN")
	hub.openDoc(client, page)
	hub.docUpdate(client, page, "PAGE")

	joiner := docClient(hub, "bbb", identity.RoleMember)
	hub.openDoc(joiner, page)
	events := docEvents(t, joiner)
	if len(events) != 2 || events[0].ID != page || events[1].Payload != "PAGE" {
		t.Fatalf("a subpage joiner was not given the subpage alone: %+v", events)
	}

	hub.mu.Lock()
	sessions := len(hub.docs["room"])
	hub.mu.Unlock()
	if sessions != 2 {
		t.Fatalf("sessions = %d, want the document and its subpage kept apart", sessions)
	}
}

// A page id is whatever a client sent. Used as a path component it would name a
// file outside the project; escaped, the worst it can do is be ugly.
func TestAPageIdCannotNameAFileOutsideTheStore(t *testing.T) {
	for _, key := range []string{"..", "alpha/..", "alpha/../../escaped", `alpha/..\..\escaped`, "alpha/.", "a/b/c"} {
		name := escapeDocKey(key)
		if name == "." || name == ".." || strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") {
			t.Fatalf("escaped %q as %q", key, name)
		}
	}
	// A long key still has to be one filename, and two of them still have to be
	// two files.
	long := strings.Repeat("page-id/", 64)
	if name := escapeDocKey(long); len(name) > maxDocSidecarNameBytes+16 || strings.ContainsAny(name, `/\`) {
		t.Fatalf("escaped a long key as %q", name)
	}
	if escapeDocKey(long+"a") == escapeDocKey(long+"b") {
		t.Fatal("two long keys were escaped onto one file")
	}

	room := t.TempDir()
	hub := NewHub()
	client := docClientIn(hub, "aaa", room)
	key := docKey(clientMessage{NodeID: "alpha", PageID: "../../escaped"})
	hub.openDoc(client, key)
	commitDocSnapshot(hub, client, key, "SNAPSHOT")

	entries, err := os.ReadDir(filepath.Join(room, store.DataDir, docSidecarDir))
	if err != nil {
		t.Fatalf("no sidecar directory: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("crdt directory holds %d entries", len(entries))
	}
	// The escape aimed two levels above the project; nothing there may have
	// moved, and the file that was written must be the one in the directory.
	outside := filepath.Dir(filepath.Dir(room))
	if _, err := os.Stat(filepath.Join(outside, "escaped.bin")); !os.IsNotExist(err) {
		t.Fatalf("a page id wrote outside the project: %v", err)
	}
	if _, err := os.Stat(sidecarFile(t, room, key)); err != nil {
		t.Fatalf("the escaped sidecar is not where it belongs: %v", err)
	}
}
