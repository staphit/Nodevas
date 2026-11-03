package runs

import (
	"context"
	"errors"
	"strings"
	"testing"

	"nodevas/internal/runnerwire"
	"nodevas/internal/testpg"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	return NewStore(testpg.New(t))
}

func createRun(t *testing.T, s *Store, node string) Record {
	t.Helper()
	r, err := s.Create(context.Background(), New{
		Project: ".", NodeID: node, NodeTitle: "Title", RunnerID: "r1", RunnerName: "desk",
		OwnerID: "u-ann", Agent: "claude", Model: "default", RepoID: "repo", RepoName: "repo",
		Prompt: "extra", JobPrompt: "# Title", CreatedBy: "u-ann", CreatedByName: "ann",
	})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestCreateQueuesARun(t *testing.T) {
	s := newStore(t)
	r := createRun(t, s, "n1")
	if r.Status != runnerwire.RunQueued || r.ID == "" || r.CreatedAt == "" || r.OwnerID != "u-ann" {
		t.Fatalf("created = %+v", r)
	}
	job := r.Job()
	if job.RunID != r.ID || job.Prompt != "# Title" || job.RequestedBy != "ann" || job.Project != "." {
		t.Fatalf("job = %+v", job)
	}
	if _, err := s.Create(context.Background(), New{Project: "."}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty create err = %v", err)
	}
}

func TestTransitionsFollowTheLifecycle(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	r := createRun(t, s, "n1")

	if _, err := s.Transition(ctx, r.ID, runnerwire.RunRunning, Change{}); !errors.Is(err, ErrTransition) {
		t.Fatalf("queued → running err = %v, want ErrTransition", err)
	}
	if _, err := s.Transition(ctx, r.ID, runnerwire.RunOffered, Change{}); err != nil {
		t.Fatal(err)
	}
	// Another runner cannot move it.
	if _, err := s.Transition(ctx, r.ID, runnerwire.RunRunning, Change{Runner: "r2"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign runner err = %v, want ErrNotFound", err)
	}
	running, err := s.Transition(ctx, r.ID, runnerwire.RunRunning, Change{Runner: "r1", Base: "main", Branch: "agent/n1"})
	if err != nil {
		t.Fatal(err)
	}
	if running.StartedAt == "" || running.Base != "main" || running.Branch != "agent/n1" {
		t.Fatalf("running = %+v", running)
	}
	usage := &runnerwire.Usage{InputTokens: 10, OutputTokens: 5}
	done, err := s.Finish(ctx, r.ID, "r1", runnerwire.RunResult{
		Status: runnerwire.RunSucceeded, Summary: strings.Repeat("s", runnerwire.MaxSummaryBytes+50), ExitCode: 0, Usage: usage,
	})
	if err != nil {
		t.Fatal(err)
	}
	if done.Status != runnerwire.RunSucceeded || done.FinishedAt == "" || len(done.Summary) != runnerwire.MaxSummaryBytes ||
		done.Usage == nil || done.Usage.InputTokens != 10 {
		t.Fatalf("done = %+v", done)
	}
	// Finished is finished: a late cancel or replayed finish changes nothing.
	for _, to := range []string{runnerwire.RunCancelled, runnerwire.RunFailed, runnerwire.RunQueued, runnerwire.RunRunning} {
		if _, err := s.Transition(ctx, r.ID, to, Change{}); !errors.Is(err, ErrTransition) {
			t.Errorf("succeeded → %s err = %v", to, err)
		}
	}
	if _, err := s.Finish(ctx, r.ID, "r1", runnerwire.RunResult{Status: "weird"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad finish status err = %v", err)
	}
}

func TestLegalTable(t *testing.T) {
	cases := map[[2]string]bool{
		{runnerwire.RunQueued, runnerwire.RunOffered}:    true,
		{runnerwire.RunQueued, runnerwire.RunCancelled}:  true,
		{runnerwire.RunQueued, runnerwire.RunFailed}:     false,
		{runnerwire.RunOffered, runnerwire.RunQueued}:    true,
		{runnerwire.RunOffered, runnerwire.RunDeclined}:  true,
		{runnerwire.RunOffered, runnerwire.RunSucceeded}: false,
		{runnerwire.RunRunning, runnerwire.RunDeclined}:  false,
		{runnerwire.RunRunning, runnerwire.RunSucceeded}: true,
		{runnerwire.RunDeclined, runnerwire.RunQueued}:   false,
		{runnerwire.RunCancelled, runnerwire.RunRunning}: false,
		{runnerwire.RunSucceeded, runnerwire.RunFailed}:  false,
		{runnerwire.RunFailed, runnerwire.RunSucceeded}:  false,
		{runnerwire.RunRunning, runnerwire.RunRunning}:   false,
		{runnerwire.RunOffered, runnerwire.RunCancelled}: true,
		{runnerwire.RunRunning, runnerwire.RunCancelled}: true,
		{runnerwire.RunOffered, runnerwire.RunFailed}:    true,
	}
	for pair, want := range cases {
		if got := Legal(pair[0], pair[1]); got != want {
			t.Errorf("Legal(%s, %s) = %v, want %v", pair[0], pair[1], got, want)
		}
	}
}

func TestAppendEventsDropsResendsAndUpdatesUsage(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	r := createRun(t, s, "n1")
	batch := []runnerwire.RunEvent{
		{Seq: 1, Type: runnerwire.EventText, Text: "hello"},
		{Seq: 2, Type: runnerwire.EventToolCall, Tool: "Edit", Input: strings.Repeat("x", runnerwire.MaxEventText+10)},
		{Seq: 3, Type: runnerwire.EventUsage, Usage: &runnerwire.Usage{InputTokens: 7}},
	}
	if n, err := s.AppendEvents(ctx, r.ID, batch); err != nil || n != 3 {
		t.Fatalf("append = %d, %v", n, err)
	}
	// The same batch again, plus one out of order and one new.
	again := append(batch, runnerwire.RunEvent{Seq: 2, Type: runnerwire.EventText}, runnerwire.RunEvent{Seq: 4, Type: runnerwire.EventText, Text: "bye"})
	if n, err := s.AppendEvents(ctx, r.ID, again); err != nil || n != 1 {
		t.Fatalf("resend append = %d, %v", n, err)
	}
	events, more, err := s.Events(ctx, r.ID, 0, 0)
	if err != nil || more || len(events) != 4 {
		t.Fatalf("events = %+v, more %v, err %v", events, more, err)
	}
	if len(events[1].Input) != runnerwire.MaxEventText {
		t.Fatalf("input not truncated: %d", len(events[1].Input))
	}
	got, err := s.Get(ctx, r.ID)
	if err != nil || got.Usage == nil || got.Usage.InputTokens != 7 {
		t.Fatalf("usage = %+v, %v", got.Usage, err)
	}
	// Paging.
	page, more, err := s.Events(ctx, r.ID, 1, 2)
	if err != nil || !more || len(page) != 2 || page[0].Seq != 2 || page[1].Seq != 3 {
		t.Fatalf("page = %+v, more %v, err %v", page, more, err)
	}
	page, more, err = s.Events(ctx, r.ID, 3, 2)
	if err != nil || more || len(page) != 1 || page[0].Seq != 4 {
		t.Fatalf("last page = %+v, more %v, err %v", page, more, err)
	}
}

func TestAppendEventsStopsAtTheCap(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	r := createRun(t, s, "n1")
	var seq int64
	for added := 0; added < runnerwire.MaxRunEvents; added += runnerwire.MaxEventsPerMessage {
		batch := make([]runnerwire.RunEvent, 0, runnerwire.MaxEventsPerMessage)
		for i := 0; i < runnerwire.MaxEventsPerMessage; i++ {
			seq++
			batch = append(batch, runnerwire.RunEvent{Seq: seq, Type: runnerwire.EventLog, Text: "x"})
		}
		if _, err := s.AppendEvents(ctx, r.ID, batch); err != nil {
			t.Fatal(err)
		}
	}
	// Past the cap events are dropped, but usage still lands.
	n, err := s.AppendEvents(ctx, r.ID, []runnerwire.RunEvent{
		{Seq: seq + 1, Type: runnerwire.EventText, Text: "late"},
		{Seq: seq + 2, Type: runnerwire.EventUsage, Usage: &runnerwire.Usage{OutputTokens: 99}},
	})
	if err != nil || n != 0 {
		t.Fatalf("past the cap: %d, %v", n, err)
	}
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM run_events WHERE run_id = ?`, r.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != runnerwire.MaxRunEvents {
		t.Fatalf("stored %d events, want %d", count, runnerwire.MaxRunEvents)
	}
	if got, _ := s.Get(ctx, r.ID); got.Usage == nil || got.Usage.OutputTokens != 99 {
		t.Fatalf("usage past the cap = %+v", got.Usage)
	}
}

func TestListFiltersByViewerAndNode(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	first := createRun(t, s, "n1")
	second := createRun(t, s, "n1")
	createRun(t, s, "n2")
	other, err := s.Create(ctx, New{
		Project: ".", NodeID: "n1", RunnerID: "r9", OwnerID: "u-bob", Agent: "claude",
		RepoID: "repo", CreatedBy: "u-bob",
	})
	if err != nil {
		t.Fatal(err)
	}
	all, err := s.List(ctx, Filter{Project: ".", NodeID: "n1"})
	if err != nil || len(all) != 3 || all[0].ID != other.ID || all[2].ID != first.ID {
		t.Fatalf("admin view = %+v, %v", all, err)
	}
	mine, err := s.List(ctx, Filter{Project: ".", NodeID: "n1", Viewer: "u-ann"})
	if err != nil || len(mine) != 2 || mine[0].ID != second.ID {
		t.Fatalf("ann's view = %+v, %v", mine, err)
	}
	if none, _ := s.List(ctx, Filter{Project: "elsewhere"}); len(none) != 0 {
		t.Fatalf("other project = %+v", none)
	}
	queued, err := s.ForRunner(ctx, "r1", runnerwire.RunQueued)
	if err != nil || len(queued) != 3 || queued[0].ID != first.ID {
		t.Fatalf("queue = %+v, %v", queued, err)
	}
}

func TestTruncateKeepsUTF8Whole(t *testing.T) {
	if got := Truncate("aé", 2); got != "a" {
		t.Fatalf("Truncate = %q", got)
	}
	if got := Truncate("ok", 10); got != "ok" {
		t.Fatalf("Truncate = %q", got)
	}
	if got := Truncate("a\xffb", 10); got != "a�b" {
		t.Fatalf("invalid UTF-8 = %q", got)
	}
}
