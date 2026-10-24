package runs

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"nodevas/internal/runners"
	"nodevas/internal/runnerwire"
)

const testHead = "89abcdef0123456789abcdef0123456789abcdef"

// greenRun is a running run with an open PR whose CI passed at testHead.
func greenRun(t *testing.T, s *Store) Record {
	t.Helper()
	r := startRun(t, s)
	updated, _, err := s.SetPR(context.Background(), r.ID, "r1", runnerwire.PRInfo{
		URL: "https://github.com/o/r/pull/7", Number: 7, State: runnerwire.PROpen,
		CI: runnerwire.CISuccess, HeadSHA: testHead,
	})
	if err != nil {
		t.Fatal(err)
	}
	return updated
}

// recorder collects what a dispatcher announces.
type recorder struct {
	notified []string
	audited  []AuditEntry
}

func newTestDispatcher(s *Store) (*Dispatcher, *recorder) {
	rec := &recorder{}
	// A registry with nobody attached: every runner is offline.
	d := NewDispatcher(s, runners.NewRegistry(s.db, func() {}), nil, nil)
	d.SetNotify(func(project, runID string) { rec.notified = append(rec.notified, runID) })
	d.SetAudit(func(e AuditEntry) { rec.audited = append(rec.audited, e) })
	return d, rec
}

func TestMergeApprovalAndResult(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	d, rec := newTestDispatcher(s)
	r := greenRun(t, s)
	bea := Approver{ID: "u-bea", Name: "bea"}

	approved, err := d.Merge(ctx, r.ID, bea, testHead)
	if err != nil {
		t.Fatal(err)
	}
	m := approved.Merge
	if m == nil || m.Status != runnerwire.MergePending || m.HeadSHA != testHead || m.RequestedBy != "bea" ||
		m.RunID != r.ID || m.CreatedAt == "" || m.FinishedAt != "" {
		t.Fatalf("approved merge = %+v", m)
	}
	if len(rec.audited) != 1 || rec.audited[0].Action != "run.merge.approve" || rec.audited[0].ActorID != "u-bea" {
		t.Fatalf("audit = %+v", rec.audited)
	}
	if _, err := d.Merge(ctx, r.ID, bea, testHead); !errors.Is(err, ErrPrecondition) || err.Error() != MsgMergePending {
		t.Fatalf("second approval err = %v", err)
	}
	pending, err := s.PendingMerges(ctx, "r1")
	if err != nil || len(pending) != 1 || pending[0].ID != m.ID || pending[0].HeadSHA != testHead {
		t.Fatalf("pending = %+v %v", pending, err)
	}
	if other, _ := s.PendingMerges(ctx, "r2"); len(other) != 0 {
		t.Fatalf("another runner's pending = %+v", other)
	}

	// Only the run's runner answers, and only for the merge it was sent.
	for name, tc := range map[string]struct {
		runner  string
		outcome runnerwire.MergeOutcome
	}{
		"other runner":  {"r2", runnerwire.MergeOutcome{ID: m.ID, RunID: r.ID}},
		"other run":     {"r1", runnerwire.MergeOutcome{ID: m.ID, RunID: "nope"}},
		"unknown merge": {"r1", runnerwire.MergeOutcome{ID: "nope", RunID: r.ID}},
	} {
		if err := d.MergeResult(ctx, tc.runner, tc.outcome); !errors.Is(err, ErrNotFound) {
			t.Errorf("%s: err = %v, want ErrNotFound", name, err)
		}
	}
	if len(rec.notified) != 0 {
		t.Fatalf("notified before any result: %v", rec.notified)
	}
	if err := d.MergeResult(ctx, "r1", runnerwire.MergeOutcome{ID: m.ID, RunID: r.ID}); err != nil {
		t.Fatal(err)
	}
	if err := d.MergeResult(ctx, "r1", runnerwire.MergeOutcome{ID: m.ID, RunID: r.ID, Error: "late"}); !errors.Is(err, ErrTransition) {
		t.Fatalf("a second answer: %v", err)
	}
	got, _ := s.Get(ctx, r.ID)
	if got.Merge == nil || got.Merge.Status != runnerwire.MergeDone || got.Merge.FinishedAt == "" || got.Merge.Reason != "" {
		t.Fatalf("merged = %+v", got.Merge)
	}
	if len(rec.notified) != 1 || rec.notified[0] != r.ID {
		t.Fatalf("notified = %v, want one for the merge", rec.notified)
	}
	if last := rec.audited[len(rec.audited)-1]; last.Action != "run.merge.merged" || last.Target != r.ID {
		t.Fatalf("audit = %+v", last)
	}

	// After a result, a new approval may follow; a failure carries its reason.
	again, err := d.Merge(ctx, r.ID, bea, testHead)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.MergeResult(ctx, "r1", runnerwire.MergeOutcome{ID: again.Merge.ID, RunID: r.ID, Error: "PR is a draft"}); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Get(ctx, r.ID)
	if got.Merge.ID != again.Merge.ID || got.Merge.Status != runnerwire.MergeFailed || got.Merge.Reason != "PR is a draft" {
		t.Fatalf("failed merge = %+v", got.Merge)
	}
	// The list shows the latest merge too.
	list, err := s.List(ctx, Filter{Project: "."})
	if err != nil || len(list) != 1 || list[0].Merge == nil || list[0].Merge.ID != again.Merge.ID {
		t.Fatalf("list = %+v %v", list, err)
	}
}

func TestMergeRefusals(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	d, _ := newTestDispatcher(s)
	bea := Approver{ID: "u-bea", Name: "bea"}

	noPR := startRun(t, s)
	set := func(r Record, pr runnerwire.PRInfo) {
		t.Helper()
		if _, _, err := s.SetPR(ctx, r.ID, "r1", pr); err != nil {
			t.Fatal(err)
		}
	}
	pending := startRun(t, s)
	set(pending, runnerwire.PRInfo{URL: "https://github.com/o/r/pull/1", State: runnerwire.PROpen, CI: runnerwire.CIPending, HeadSHA: testHead})
	failing := startRun(t, s)
	set(failing, runnerwire.PRInfo{URL: "https://github.com/o/r/pull/2", State: runnerwire.PROpen, CI: runnerwire.CIFailure, HeadSHA: testHead})
	closed := startRun(t, s)
	set(closed, runnerwire.PRInfo{URL: "https://github.com/o/r/pull/3", State: runnerwire.PRClosed, CI: runnerwire.CISuccess, HeadSHA: testHead})
	noHead := startRun(t, s)
	set(noHead, runnerwire.PRInfo{URL: "https://github.com/o/r/pull/4", State: runnerwire.PROpen, CI: runnerwire.CISuccess})
	green := greenRun(t, s)

	for name, tc := range map[string]struct {
		run  Record
		head string
		want string
	}{
		"no PR":      {noPR, testHead, MsgNoOpenPR},
		"closed PR":  {closed, testHead, MsgNoOpenPR},
		"CI pending": {pending, testHead, MsgCINotGreen},
		"CI red":     {failing, testHead, MsgCINotGreen},
		"no head":    {noHead, testHead, MsgNoHead},
		"head moved": {green, "0000000000000000000000000000000000000000", MsgHeadMoved},
	} {
		_, err := d.Merge(ctx, tc.run.ID, bea, tc.head)
		if !errors.Is(err, ErrPrecondition) || err.Error() != tc.want {
			t.Errorf("%s: err = %v, want %q", name, err, tc.want)
		}
	}
	if _, err := d.Merge(ctx, "nope", bea, testHead); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown run: %v", err)
	}
	if got, _ := s.Get(ctx, green.ID); got.Merge != nil {
		t.Fatalf("a refused approval was recorded: %+v", got.Merge)
	}
}

func TestMergeExpiresAfterTTL(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	d, _ := newTestDispatcher(s)
	r := greenRun(t, s)
	base := time.Now()
	s.SetClock(func() time.Time { return base })
	first, err := d.Merge(ctx, r.ID, Approver{ID: "u-bea"}, testHead)
	if err != nil {
		t.Fatal(err)
	}
	if first.Merge.RequestedBy != "u-bea" {
		t.Fatalf("requestedBy without a name = %q", first.Merge.RequestedBy)
	}

	s.SetClock(func() time.Time { return base.Add(MergeTTL - time.Minute) })
	if got, _ := s.Get(ctx, r.ID); got.Merge.Status != runnerwire.MergePending {
		t.Fatalf("within the TTL: %+v", got.Merge)
	}
	if ids, err := s.ExpireMerges(ctx); err != nil || len(ids) != 0 {
		t.Fatalf("sweep within the TTL: %v %v", ids, err)
	}

	s.SetClock(func() time.Time { return base.Add(MergeTTL + time.Minute) })
	if ids, err := s.ExpireMerges(ctx); err != nil || len(ids) != 1 || ids[0] != r.ID {
		t.Fatalf("sweep past the TTL: %v %v", ids, err)
	}
	got, _ := s.Get(ctx, r.ID)
	if got.Merge.Status != runnerwire.MergeExpired || got.Merge.Reason != ReasonMergeStale || got.Merge.FinishedAt == "" {
		t.Fatalf("expired = %+v", got.Merge)
	}
	if jobs, _ := s.PendingMerges(ctx, "r1"); len(jobs) != 0 {
		t.Fatalf("an expired merge is still pending: %+v", jobs)
	}
	// A fresh approval is allowed, and expires lazily when read too late.
	second, err := d.Merge(ctx, r.ID, Approver{ID: "u-bea", Name: "bea"}, testHead)
	if err != nil || second.Merge.ID == first.Merge.ID || second.Merge.Status != runnerwire.MergePending {
		t.Fatalf("second approval = %+v %v", second.Merge, err)
	}
	s.SetClock(func() time.Time { return base.Add(2*MergeTTL + 3*time.Minute) })
	if got, _ := s.Get(ctx, r.ID); got.Merge.ID != second.Merge.ID || got.Merge.Status != runnerwire.MergeExpired {
		t.Fatalf("read past the TTL = %+v", got.Merge)
	}
	// A runner that got the job in time and merged anyway is believed.
	if err := d.MergeResult(ctx, "r1", runnerwire.MergeOutcome{ID: second.Merge.ID, RunID: r.ID}); err != nil {
		t.Fatalf("result for an expired merge: %v", err)
	}
	if got, _ := s.Get(ctx, r.ID); got.Merge.Status != runnerwire.MergeDone {
		t.Fatalf("after the late result = %+v", got.Merge)
	}
}

// run-notify fires when a run succeeds or fails, when its CI turns red, and
// when a merge finishes; not for cancels, repeats or other PR moves.
func TestDispatcherNotifies(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	d, rec := newTestDispatcher(s)

	ok := startRun(t, s)
	pr := runnerwire.PRInfo{URL: "https://github.com/o/r/pull/1", Number: 1, State: runnerwire.PROpen, CI: runnerwire.CIPending, HeadSHA: "aa"}
	for _, ci := range []string{runnerwire.CIPending, runnerwire.CIFailure, runnerwire.CIFailure, runnerwire.CIPending, runnerwire.CISuccess} {
		pr.CI = ci
		if err := d.PR(ctx, "r1", ok.ID, pr); err != nil {
			t.Fatal(err)
		}
	}
	if len(rec.notified) != 1 {
		t.Fatalf("after CI pending → red → red → pending → green: %v, want one", rec.notified)
	}
	pr.CI, pr.HeadSHA = runnerwire.CIFailure, "bb"
	if err := d.PR(ctx, "r1", ok.ID, pr); err != nil {
		t.Fatal(err)
	}
	if len(rec.notified) != 2 {
		t.Fatalf("red again after green: %v", rec.notified)
	}
	if err := d.Finish(ctx, "r1", ok.ID, runnerwire.RunResult{Status: runnerwire.RunSucceeded}); err != nil {
		t.Fatal(err)
	}
	if err := d.Finish(ctx, "r1", ok.ID, runnerwire.RunResult{Status: runnerwire.RunSucceeded}); err == nil {
		t.Fatal("a second finish was accepted")
	}
	if len(rec.notified) != 3 {
		t.Fatalf("after the finish: %v", rec.notified)
	}

	bad := startRun(t, s)
	if err := d.Finish(ctx, "r1", bad.ID, runnerwire.RunResult{Status: runnerwire.RunFailed, Error: "boom"}); err != nil {
		t.Fatal(err)
	}
	stopped := startRun(t, s)
	if err := d.Finish(ctx, "r1", stopped.ID, runnerwire.RunResult{Status: runnerwire.RunCancelled}); err != nil {
		t.Fatal(err)
	}
	if len(rec.notified) != 4 || rec.notified[3] != bad.ID {
		t.Fatalf("after a failure and a cancel: %v", rec.notified)
	}
}

func TestBudgetStatus(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	at := func(stamp string) {
		ts, err := time.Parse(time.RFC3339, stamp)
		if err != nil {
			t.Fatal(err)
		}
		s.SetClock(func() time.Time { return ts })
	}
	finish := func(r Record, usage runnerwire.Usage) {
		t.Helper()
		if _, err := s.Transition(ctx, r.ID, runnerwire.RunOffered, Change{}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Transition(ctx, r.ID, runnerwire.RunRunning, Change{Runner: "r1"}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Finish(ctx, r.ID, "r1", runnerwire.RunResult{Status: runnerwire.RunSucceeded, Usage: &usage}); err != nil {
			t.Fatal(err)
		}
	}
	// Yesterday, one microsecond before midnight UTC: not today's.
	at("2026-03-09T23:59:59.999999Z")
	finish(createRun(t, s, "n0"), runnerwire.Usage{InputTokens: 1000, OutputTokens: 1000, CostUSD: 5})
	// Today, in another time zone's evening: still today's UTC day.
	at("2026-03-10T00:00:00+00:00")
	finish(createRun(t, s, "n1"), runnerwire.Usage{InputTokens: 100, OutputTokens: 20, CacheReadTokens: 9999, CostUSD: 0.25})
	at("2026-03-10T23:30:00-00:00")
	finish(createRun(t, s, "n2"), runnerwire.Usage{InputTokens: 50, OutputTokens: 30, CostUSD: 0.5})
	createRun(t, s, "n3") // queued, no usage yet: counts as a run
	// Someone else's runner: not ann's budget.
	if _, err := s.Create(ctx, New{Project: ".", NodeID: "x", RunnerID: "r9", OwnerID: "u-cal", Agent: "claude",
		RepoID: "repo", CreatedBy: "u-cal"}); err != nil {
		t.Fatal(err)
	}

	st, err := s.BudgetStatus(ctx, "u-ann")
	if err != nil {
		t.Fatal(err)
	}
	if st.Used.Runs != 3 || st.Used.Tokens != 200 || math.Abs(st.Used.CostUSD-0.75) > 1e-9 || st.Exceeded != "" ||
		st.Budget != (runnerwire.Budget{}) {
		t.Fatalf("status = %+v", st)
	}

	for _, tc := range []struct {
		budget runnerwire.Budget
		want   string
	}{
		{runnerwire.Budget{DailyRuns: 3}, ExceededRuns},
		{runnerwire.Budget{DailyRuns: 4}, ""},
		{runnerwire.Budget{DailyCostUSD: 0.75}, ExceededCost},
		{runnerwire.Budget{DailyCostUSD: 0.8}, ""},
		{runnerwire.Budget{DailyTokens: 200}, ExceededTokens},
		{runnerwire.Budget{DailyTokens: 201, DailyRuns: 10, DailyCostUSD: 1}, ""},
	} {
		if err := s.SetBudget(ctx, "u-ann", tc.budget); err != nil {
			t.Fatal(err)
		}
		st, err := s.BudgetStatus(ctx, "u-ann")
		if err != nil || st.Budget != tc.budget || st.Exceeded != tc.want {
			t.Errorf("budget %+v: %+v %v, want exceeded %q", tc.budget, st, err, tc.want)
		}
		if tc.want != "" && BudgetMessage(st) == "" {
			t.Errorf("budget %+v: no message", tc.budget)
		}
	}

	// The next UTC day starts from zero.
	at("2026-03-11T00:00:01Z")
	if st, _ := s.BudgetStatus(ctx, "u-ann"); st.Used.Runs != 0 || st.Used.Tokens != 0 || st.Exceeded != "" {
		t.Fatalf("next day = %+v", st)
	}
}

func TestValidateBudget(t *testing.T) {
	for name, b := range map[string]runnerwire.Budget{
		"negative runs":  {DailyRuns: -1},
		"negative cost":  {DailyCostUSD: -0.01},
		"negative token": {DailyTokens: -5},
		"NaN cost":       {DailyCostUSD: math.NaN()},
		"infinite cost":  {DailyCostUSD: math.Inf(1)},
		"too many runs":  {DailyRuns: MaxDailyRuns + 1},
		"too much cost":  {DailyCostUSD: MaxDailyCostUSD + 1},
		"too many token": {DailyTokens: MaxDailyTokens + 1},
	} {
		if err := ValidateBudget(b); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if err := ValidateBudget(runnerwire.Budget{DailyRuns: MaxDailyRuns, DailyCostUSD: 12.5, DailyTokens: MaxDailyTokens}); err != nil {
		t.Fatal(err)
	}
}
