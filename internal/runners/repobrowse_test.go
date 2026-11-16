package runners

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"nodevas/internal/runnerwire"
)

func browseInfo() runnerwire.Info {
	return runnerwire.Info{Version: "v", Capabilities: []string{runnerwire.CapHeadless, runnerwire.CapRepoBrowse},
		Repos: []runnerwire.Repo{{ID: "repo-1", Name: "visual-editor"}}}
}

func listReq() runnerwire.RepoRequest {
	return runnerwire.RepoRequest{RepoID: "repo-1", Op: runnerwire.RepoOpList, Path: "src"}
}

// B-47: the relay only reaches the asker's own online runner, with the
// capability and the repo; it takes the answer only from that runner, and
// gives up after its timeout.
func TestRepoRelayRefusesAndRelays(t *testing.T) {
	reg, _ := newRegistry(t)
	relay := NewRepoRelay(reg)
	relay.Timeout = 2 * time.Second
	ctx := context.Background()

	conn := newLLMConn()
	mine := online(t, reg, "ann", browseInfo(), conn)
	theirs := online(t, reg, "bob", browseInfo(), newLLMConn())
	plain := browseInfo()
	plain.Capabilities = []string{runnerwire.CapHeadless}
	noCap := online(t, reg, "ann", plain, newLLMConn())

	if _, err := relay.Browse(ctx, ann.ID, theirs.ID, listReq()); !errors.Is(err, ErrRepoNotFound) {
		t.Fatalf("bob's runner: %v, want ErrRepoNotFound", err)
	}
	if _, err := relay.Browse(ctx, ann.ID, noCap.ID, listReq()); !errors.Is(err, ErrRepoBrowseOff) {
		t.Fatalf("runner without repo-browse: %v, want ErrRepoBrowseOff", err)
	}
	other := listReq()
	other.RepoID = "repo-9"
	if _, err := relay.Browse(ctx, ann.ID, mine.ID, other); !errors.Is(err, ErrRepoNotFound) {
		t.Fatalf("repo not in the hello: %v, want ErrRepoNotFound", err)
	}
	offline, _, err := reg.Create(ctx, ann, "ann laptop")
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.SetHello(ctx, offline.ID, browseInfo()); err != nil {
		t.Fatal(err)
	}
	if _, err := relay.Browse(ctx, ann.ID, offline.ID, listReq()); !errors.Is(err, ErrRunnerOffline) {
		t.Fatalf("offline runner: %v, want ErrRunnerOffline", err)
	}

	type result struct {
		done runnerwire.RepoDone
		err  error
	}
	results := make(chan result, 1)
	go func() {
		done, err := relay.Browse(ctx, ann.ID, mine.ID, listReq())
		results <- result{done, err}
	}()
	sent := conn.next(t)
	if sent.Type != runnerwire.TypeRepoRequest || sent.Repo == nil || sent.Repo.ID == "" || sent.Repo.AccountID != ann.ID || sent.Repo.Path != "src" {
		t.Fatalf("sent = %+v", sent)
	}
	answer := runnerwire.Envelope{Type: runnerwire.TypeRepoDone, RepoDone: &runnerwire.RepoDone{ID: sent.Repo.ID,
		Listing: &runnerwire.RepoListing{Path: "src", Entries: []runnerwire.RepoEntry{{Name: "math.js", Type: "file"}}}}}
	// The same answer from another runner is dropped.
	_ = relay.Deliver(theirs.ID, answer)
	// So is an oversized one.
	huge := answer
	huge.RepoDone = &runnerwire.RepoDone{ID: sent.Repo.ID, File: &runnerwire.RepoFile{Content: strings.Repeat("x", runnerwire.MaxRepoDoneBytes+1)}}
	_ = relay.Deliver(mine.ID, huge)
	if err := relay.Deliver(mine.ID, answer); err != nil {
		t.Fatal(err)
	}
	got := <-results
	if got.err != nil || got.done.Listing == nil || got.done.Listing.Entries[0].Name != "math.js" {
		t.Fatalf("browse = %+v, %v", got.done, got.err)
	}

	relay.Timeout = 50 * time.Millisecond
	if _, err := relay.Browse(ctx, ann.ID, mine.ID, listReq()); !errors.Is(err, ErrRepoTimeout) {
		t.Fatalf("unanswered: %v, want ErrRepoTimeout", err)
	}
}

// A preview reaches only the hub's built-in runner, as its welcome said,
// with the capability: a forged capability on another runner, or the
// built-in runner with the preview off, is refused with nothing sent.
func TestBrowseRunPreviewNeedsBuiltIn(t *testing.T) {
	reg, _ := newRegistry(t)
	relay := NewRepoRelay(reg)
	relay.Timeout = 2 * time.Second
	ctx := context.Background()
	previewInfo := func(caps ...string) runnerwire.Info {
		info := browseInfo()
		info.Capabilities = append(info.Capabilities, runnerwire.CapRunWorkspace)
		info.Capabilities = append(info.Capabilities, caps...)
		return info
	}
	req := runnerwire.RepoRequest{RepoID: "repo-1", Op: runnerwire.RepoOpRunPreview, RunID: "run-1"}

	forgedConn := newLLMConn()
	forged := online(t, reg, "ann", previewInfo(runnerwire.CapRunPreview), forgedConn)
	offConn := newLLMConn()
	off := online(t, reg, "ann", previewInfo(), offConn)
	reg.SetWelcomed(off.ID, true)
	for name, id := range map[string]string{"forged capability": forged.ID, "built in, preview off": off.ID} {
		if _, err := relay.BrowseRun(ctx, ann.ID, id, req); !errors.Is(err, ErrRunPreviewOff) {
			t.Fatalf("%s: %v, want ErrRunPreviewOff", name, err)
		}
	}
	if len(forgedConn.got) != 0 || len(offConn.got) != 0 {
		t.Fatal("a refused preview reached a runner")
	}
	// Other run ops on the forged runner are untouched.
	infoDone := make(chan error, 1)
	go func() {
		_, err := relay.BrowseRun(ctx, ann.ID, forged.ID, runnerwire.RepoRequest{RepoID: "repo-1", Op: runnerwire.RepoOpRunInfo, RunID: "run-1"})
		infoDone <- err
	}()
	sent := forgedConn.next(t)
	if sent.Repo == nil || sent.Repo.Op != runnerwire.RepoOpRunInfo {
		t.Fatalf("run_info = %+v", sent)
	}
	_ = relay.Deliver(forged.ID, runnerwire.Envelope{Type: runnerwire.TypeRepoDone, RepoDone: &runnerwire.RepoDone{ID: sent.Repo.ID,
		RunInfo: &runnerwire.RunWorkspaceInfo{}}})
	if err := <-infoDone; err != nil {
		t.Fatal(err)
	}

	conn := newLLMConn()
	builtIn := online(t, reg, "ann", previewInfo(runnerwire.CapRunPreview), conn)
	reg.SetWelcomed(builtIn.ID, true)
	// Someone else asking for it: not found.
	if _, err := relay.BrowseRun(ctx, bob.ID, builtIn.ID, req); !errors.Is(err, ErrRepoNotFound) {
		t.Fatalf("bob: %v, want ErrRepoNotFound", err)
	}
	results := make(chan error, 1)
	go func() {
		done, err := relay.BrowseRun(ctx, ann.ID, builtIn.ID, req)
		if err == nil && (done.Preview == nil || done.Preview.Token != "t") {
			err = errors.New("no preview in the answer")
		}
		results <- err
	}()
	sent = conn.next(t)
	if sent.Repo == nil || sent.Repo.Op != runnerwire.RepoOpRunPreview || sent.Repo.RunID != "run-1" || sent.Repo.AccountID != ann.ID {
		t.Fatalf("sent = %+v", sent)
	}
	_ = relay.Deliver(builtIn.ID, runnerwire.Envelope{Type: runnerwire.TypeRepoDone, RepoDone: &runnerwire.RepoDone{ID: sent.Repo.ID,
		Preview: &runnerwire.RunPreview{Web: true, Port: 1234, Token: "t", Entry: "index.html"}}})
	if err := <-results; err != nil {
		t.Fatal(err)
	}
}
