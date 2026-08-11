package server

import (
	"context"
	"net/http/httptest"
	"nodevas/internal/realtime"
	"nodevas/internal/store"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNonLeaderLargeSnapshotSurvivesWireLimitsAndLateOpenReplay(t *testing.T) {
	httpServer, _ := hubTestServer(t)
	first := joinRoom(t, httpServer, "main")
	firstState := first.openDoc("alpha")
	second := joinRoom(t, httpServer, "main")
	secondState := second.openDoc("alpha")

	writer, peer := first, second
	if firstState.Leader {
		writer, peer = second, first
	} else if secondState.Leader {
		writer, peer = first, second
	} else {
		t.Fatal("neither participant was elected leader")
	}
	writer.send(map[string]any{"type": "doc-compact-request", "nodeId": "alpha"})
	compact := writer.await("doc-compact", func(event realtime.Event) bool { return event.ID == "alpha" })
	if compact.Token == "" || compact.Token[0] == '0' {
		t.Fatalf("non-leader compact token = %+v", compact)
	}

	payload := strings.Repeat("S", 60*1024)
	const chunkSize = 48 * 1024
	writer.send(map[string]any{"type": "doc-snapshot-start", "nodeId": "alpha", "token": compact.Token, "size": len(payload), "chunks": 2})
	ready := writer.await("doc-snapshot-ready", nil)
	if ready.Token != compact.Token {
		t.Fatalf("snapshot start ack = %+v", ready)
	}
	writer.send(map[string]any{"type": "doc-snapshot-chunk", "nodeId": "alpha", "token": compact.Token, "seq": 0, "payload": payload[:chunkSize]})
	writer.send(map[string]any{"type": "doc-snapshot-chunk", "nodeId": "alpha", "token": compact.Token, "seq": 1, "payload": payload[chunkSize:]})
	writer.send(map[string]any{"type": "doc-snapshot-commit", "nodeId": "alpha", "token": compact.Token})

	start := peer.await("doc-snapshot-start", nil)
	firstChunk := peer.await("doc-snapshot-chunk", nil)
	secondChunk := peer.await("doc-snapshot-chunk", nil)
	commit := peer.await("doc-snapshot-commit", nil)
	if start.Token != compact.Token || start.Size != len(payload) || start.Chunks != 2 ||
		firstChunk.Seq != 0 || secondChunk.Seq != 1 || firstChunk.Payload+secondChunk.Payload != payload ||
		commit.Token != compact.Token || commit.From == "" {
		t.Fatalf("peer snapshot frames: start=%+v first=%+v second=%+v commit=%+v", start, firstChunk, secondChunk, commit)
	}
	accepted := writer.await("doc-snapshot-accepted", nil)
	if accepted.Token != compact.Token {
		t.Fatalf("sender ack = %+v", accepted)
	}

	writer.send(map[string]any{"type": "doc-update", "nodeId": "alpha", "payload": "TAIL"})
	if update := peer.await("doc-update", nil); update.Payload != "TAIL" {
		t.Fatalf("live tail = %+v", update)
	}
	late := joinRoom(t, httpServer, "main")
	if state := late.openDoc("alpha"); state.Seed {
		t.Fatalf("late opener seeded over snapshot: %+v", state)
	}
	lateStart := late.await("doc-snapshot-start", nil)
	var replay string
	for seq := 0; seq < lateStart.Chunks; seq++ {
		chunk := late.await("doc-snapshot-chunk", nil)
		if chunk.Token != lateStart.Token || chunk.Seq != seq {
			t.Fatalf("late chunk %d = %+v", seq, chunk)
		}
		replay += chunk.Payload
	}
	lateCommit := late.await("doc-snapshot-commit", nil)
	lateTail := late.await("doc-update", nil)
	if lateStart.Token == "" || lateStart.Token[0] == '0' || replay != payload || lateCommit.Token != lateStart.Token || lateTail.Payload != "TAIL" {
		t.Fatalf("late replay: start=%+v commit=%+v tail=%+v bytes=%d", lateStart, lateCommit, lateTail, len(replay))
	}
	// If the chunk exception bypassed authorization/rate limits globally, the
	// connection would have policy-closed before this ordered echo.
	writer.sync("still-connected")
}

// openDoc joins a live document and returns the reply that says whether this
// client loads the file and whether it writes it.
func (p *wsPeer) openDoc(nodeID string) realtime.Event {
	p.t.Helper()
	p.send(map[string]any{"type": "doc-open", "nodeId": nodeID})
	return p.await("doc-state", func(event realtime.Event) bool { return event.ID == nodeID })
}

// openPage joins a node's subpage. The key the server answers on is wire
// format: the browser builds the same string from the same two fields.
func (p *wsPeer) openPage(nodeID, pageID string) realtime.Event {
	p.t.Helper()
	key := nodeID + "/" + pageID
	p.send(map[string]any{"type": "doc-open", "nodeId": nodeID, "pageId": pageID})
	return p.await("doc-state", func(event realtime.Event) bool { return event.ID == key })
}

// sync waits for everything this peer has sent to have been handled. Presence
// is the only message that comes back to its own sender, and one connection's
// frames are handled in order, so its echo is the marker for the rest.
func (p *wsPeer) sync(marker string) {
	p.t.Helper()
	p.send(map[string]any{"type": "presence", "nodeId": marker})
	p.await("presence", func(event realtime.Event) bool {
		for _, peer := range event.Peers {
			if peer.NodeID == marker {
				return true
			}
		}
		return false
	})
}

func TestFirstOpenerSeedsTheDocumentAndLeadsIt(t *testing.T) {
	httpServer, _ := hubTestServer(t)

	peer := joinRoom(t, httpServer, "main")
	state := peer.openDoc("alpha")
	if !state.Seed || !state.Leader {
		t.Fatalf("the first opener was not told to seed and lead: %+v", state)
	}
}

// The second client must not seed: it would apply the file on top of the
// updates that already describe it and end up with the text twice.
func TestASecondOpenerReplaysTheStoredUpdatesInstead(t *testing.T) {
	httpServer, _ := hubTestServer(t)

	first := joinRoom(t, httpServer, "main")
	first.openDoc("alpha")
	first.send(map[string]any{"type": "doc-update", "nodeId": "alpha", "payload": "QUFB"})
	first.send(map[string]any{"type": "doc-update", "nodeId": "alpha", "payload": "QkJC"})
	first.sync("alpha")

	second := joinRoom(t, httpServer, "main")
	state := second.openDoc("alpha")
	if state.Seed {
		t.Fatalf("a joiner with a stored log was told to seed: %+v", state)
	}
	replayed := []string{
		second.await("doc-update", nil).Payload,
		second.await("doc-update", nil).Payload,
	}
	if replayed[0] != "QUFB" || replayed[1] != "QkJC" {
		t.Fatalf("the stored log replayed as %v", replayed)
	}
}

func TestAnUpdateReachesThePeerAndNotItsSender(t *testing.T) {
	httpServer, _ := hubTestServer(t)

	first := joinRoom(t, httpServer, "main")
	first.openDoc("alpha")
	second := joinRoom(t, httpServer, "main")
	second.openDoc("alpha")

	first.send(map[string]any{"type": "doc-update", "nodeId": "alpha", "payload": "QUFB"})
	update := second.await("doc-update", nil)
	if update.ID != "alpha" || update.Payload != "QUFB" || update.From == "" {
		t.Fatalf("doc-update = %+v", update)
	}
	// The sender already has what it typed; the round trip is what would make
	// the caret jump.
	first.awaitNone("doc-update", 300*time.Millisecond)
}

func TestALeaderThatDisconnectsHandsTheFileToTheSurvivor(t *testing.T) {
	httpServer, _ := hubTestServer(t)

	first := joinRoom(t, httpServer, "main")
	firstState := first.openDoc("alpha")
	second := joinRoom(t, httpServer, "main")
	secondState := second.openDoc("alpha")

	// Which of the two leads depends on their connection ids, which are random.
	leader, survivor := first, second
	if secondState.Leader {
		leader, survivor = second, first
	} else if !firstState.Leader {
		t.Fatal("neither participant was made leader")
	}

	_ = leader.conn.CloseNow()
	survivor.await("doc-leader", func(event realtime.Event) bool {
		return event.ID == "alpha" && event.Leader
	})
}

func TestAFlushedRevisionReachesTheRoomButNotTheWriter(t *testing.T) {
	httpServer, server := hubTestServer(t)
	content := []byte("flushed through websocket")
	path := store.NodeDocPath(server.pm.Store().Root(), "alpha")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}

	first := joinRoom(t, httpServer, "main")
	firstState := first.openDoc("alpha")
	second := joinRoom(t, httpServer, "main")
	secondState := second.openDoc("alpha")
	writer, peer := first, second
	if secondState.Leader {
		writer, peer = second, first
	} else if !firstState.Leader {
		t.Fatal("neither document participant was elected leader")
	}

	rev := store.Rev(content)
	writer.send(map[string]any{"type": "doc-flushed", "nodeId": "alpha", "rev": rev})
	flushed := peer.await("doc-flushed", nil)
	if flushed.ID != "alpha" || flushed.Rev != rev || flushed.From == "" {
		t.Fatalf("doc-flushed = %+v", flushed)
	}
	writer.awaitNone("doc-flushed", 300*time.Millisecond)
}

// A snapshot is a full state, which is a valid update to apply: peers that were
// only sent incremental updates would drift away from the stored log.
func TestASnapshotIsRelayedToThePeersAsAnUpdate(t *testing.T) {
	httpServer, _ := hubTestServer(t)

	first := joinRoom(t, httpServer, "main")
	first.openDoc("alpha")
	second := joinRoom(t, httpServer, "main")
	second.openDoc("alpha")

	second.send(map[string]any{"type": "doc-snapshot", "nodeId": "alpha", "payload": "U05BUA=="})
	update := first.await("doc-update", nil)
	if update.ID != "alpha" || update.Payload != "U05BUA==" || update.From == "" {
		t.Fatalf("doc-update = %+v", update)
	}
	second.awaitNone("doc-update", 300*time.Millisecond)
}

func TestShutdownWaitsForLiveDocumentPersistence(t *testing.T) {
	httpServer, server := hubTestServer(t)

	peer := joinRoom(t, httpServer, "main")
	peer.openDoc("alpha")
	peer.send(map[string]any{"type": "doc-update", "nodeId": "alpha", "payload": "QUFB"})
	// One connection's frames are handled in order, so the presence echo says
	// the update has reached the session before Shutdown makes its last-member
	// sidecar write.
	peer.sync("alpha")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}

	restarted := serverForTest(t, server.pm, realtime.NewHub(), nil)
	restartedHTTP := httptest.NewServer(restarted.Handler())
	t.Cleanup(restartedHTTP.Close)
	next := joinRoom(t, restartedHTTP, "main")
	if state := next.openDoc("alpha"); state.Seed {
		t.Fatalf("shutdown discarded the live document: %+v", state)
	}
	if update := next.await("doc-update", nil); update.Payload != "QUFB" {
		t.Fatalf("recovered update = %+v", update)
	}
}

// A deploy in the middle of two people typing costs whatever the leader had not
// written to the file yet, unless the session was left on disk. A second hub
// over the same project directory is that restart.
func TestALiveDocumentSurvivesARestart(t *testing.T) {
	server, pm := twoProjectServer(t)
	httpServer := httptest.NewServer(server.Handler())
	t.Cleanup(httpServer.Close)

	peer := joinRoom(t, httpServer, "main")
	if state := peer.openDoc("alpha"); !state.Seed {
		t.Fatalf("the first opener was not told to seed: %+v", state)
	}
	// Only the tokenized barrier is a durable snapshot. Legacy doc-snapshot is
	// intentionally an ordinary append so tiny frames cannot amplify fsyncs.
	peer.send(map[string]any{"type": "doc-compact-request", "nodeId": "alpha"})
	compact := peer.await("doc-compact", nil)
	peer.send(map[string]any{"type": "doc-snapshot-start", "nodeId": "alpha", "token": compact.Token, "size": 8, "chunks": 1})
	peer.await("doc-snapshot-ready", nil)
	peer.send(map[string]any{"type": "doc-snapshot-chunk", "nodeId": "alpha", "token": compact.Token, "seq": 0, "payload": "U05BUA=="})
	peer.send(map[string]any{"type": "doc-snapshot-commit", "nodeId": "alpha", "token": compact.Token})
	peer.await("doc-snapshot-accepted", nil)
	peer.sync("alpha")

	restartedServer := serverForTest(t, pm, realtime.NewHub(), nil)
	restarted := httptest.NewServer(restartedServer.Handler())
	t.Cleanup(restarted.Close)
	next := joinRoom(t, restarted, "main")
	state := next.openDoc("alpha")
	if state.Seed {
		t.Fatalf("the first client after a restart was told to seed over the stored session: %+v", state)
	}
	start := next.await("doc-snapshot-start", nil)
	chunk := next.await("doc-snapshot-chunk", nil)
	commit := next.await("doc-snapshot-commit", nil)
	if start.ID != "alpha" || start.Token == "" || start.Size != 8 || start.Chunks != 1 || chunk.Token != start.Token || chunk.Seq != 0 || chunk.Payload != "U05BUA==" || commit.Token != start.Token {
		t.Fatalf("snapshot replay = start=%+v chunk=%+v commit=%+v", start, chunk, commit)
	}
}

// The node's document and its subpage are two documents. One session for both
// would apply each one's updates to the other.
func TestASubpageIsNotTheNodesOwnDocument(t *testing.T) {
	httpServer, _ := hubTestServer(t)

	first := joinRoom(t, httpServer, "main")
	first.openDoc("alpha")
	first.send(map[string]any{"type": "doc-update", "nodeId": "alpha", "payload": "QUFB"})
	first.sync("alpha")

	second := joinRoom(t, httpServer, "main")
	state := second.openPage("alpha", "page1")
	if !state.Seed {
		t.Fatalf("a subpage was answered with the node's own session: %+v", state)
	}
	second.awaitNone("doc-update", 300*time.Millisecond)

	// And the traffic on one never reaches the other.
	second.send(map[string]any{"type": "doc-update", "nodeId": "alpha", "pageId": "page1", "payload": "QkJC"})
	first.awaitNone("doc-update", 300*time.Millisecond)
}
