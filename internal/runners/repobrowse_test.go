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
