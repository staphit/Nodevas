package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"nodevas/internal/engine"
	"nodevas/internal/identity"
)

// newClaimTestStore builds a two-node chain: design blocks build.
func newClaimTestStore(t *testing.T) *Store {
	t.Helper()
	root := t.TempDir()
	g := &engine.Graph{
		Version: 1,
		Nodes: []*engine.Node{
			{ID: "design", Title: "Design"},
			{ID: "build", Title: "Build", Requires: "design"},
		},
		Edges: []*engine.Edge{{From: "design", To: "build"}},
	}
	data, err := engine.MarshalGraph(g)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "graph.yaml"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	return NewStore(root)
}

func TestReasonNotClaimableExplainsAFalseGateCondition(t *testing.T) {
	readiness := engine.Readiness{Blocked: []engine.ReadyNode{{
		ID:           "target",
		Reason:       "gate_condition",
		GateID:       "gate-xor",
		GateOperator: "xor",
		BlockedBy:    []string{"a", "b"},
	}}}
	got := reasonNotClaimable(readiness, "target")
	if got != "its XOR gate condition does not hold (inputs: a, b)" {
		t.Fatalf("reason = %q", got)
	}
}

func TestReasonNotClaimableDoesNotInventAnInputForAnUnwiredGate(t *testing.T) {
	readiness := engine.Readiness{Blocked: []engine.ReadyNode{{
		ID:           "target",
		Reason:       "gate_condition",
		GateID:       "gate-empty",
		GateOperator: "and",
	}}}
	got := reasonNotClaimable(readiness, "target")
	if got != "its AND gate condition does not hold" {
		t.Fatalf("reason = %q", got)
	}
}

// atTime runs the rest of the test as though the clock read `when`.
func atTime(t *testing.T, when time.Time) {
	t.Helper()
	previous := claimClock
	claimClock = func() time.Time { return when }
	t.Cleanup(func() { claimClock = previous })
}

func statusOf(t *testing.T, st *Store, id string) engine.Status {
	t.Helper()
	g, _, err := st.LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	rs, err := st.LoadState()
	if err != nil {
		t.Fatal(err)
	}
	return engine.ComputeStatuses(g, rs)[id]
}

// This is the whole reason claiming is a server-side operation. Two agents
// reading the same ready queue see the same node; if the check and the write
// were not in one critical section, both would start it and the only evidence
// would be two sets of changes to one file.
func TestOnlyOneOfTwoSimultaneousClaimsWins(t *testing.T) {
	st := newClaimTestStore(t)

	const agents = 8
	var (
		start   sync.WaitGroup
		done    sync.WaitGroup
		mu      sync.Mutex
		winners []string
		losers  int
	)
	start.Add(1)
	for index := range agents {
		done.Add(1)
		go func() {
			defer done.Done()
			start.Wait()
			owner := "agent-" + string(rune('a'+index))
			_, err := st.ClaimNode(identity.Local, "design", owner, 0, "")
			mu.Lock()
			defer mu.Unlock()
			var taken *ErrAlreadyClaimed
			switch {
			case err == nil:
				winners = append(winners, owner)
			case errors.As(err, &taken):
				losers++
			default:
				t.Errorf("%s: unexpected error %v", owner, err)
			}
		}()
	}
	start.Done()
	done.Wait()

	if len(winners) != 1 {
		t.Fatalf("%d agents claimed the same node: %v", len(winners), winners)
	}
	if losers != agents-1 {
		t.Fatalf("%d agents were turned away, want %d", losers, agents-1)
	}
	if got := statusOf(t, st, "design"); got != engine.StatusInProgress {
		t.Fatalf("status = %q, want in_progress", got)
	}
}

func TestABlockedNodeCannotBeClaimed(t *testing.T) {
	st := newClaimTestStore(t)

	_, err := st.ClaimNode(identity.Local, "build", "agent", 0, "")
	var refused *ErrNotClaimable
	if !errors.As(err, &refused) {
		t.Fatalf("error = %v, want ErrNotClaimable", err)
	}
	if !strings.Contains(refused.Reason, "design") {
		t.Fatalf("reason = %q, want it to name the blocker", refused.Reason)
	}
}

func TestClaimUsesRequiresOverLegacyEdgeProjection(t *testing.T) {
	t.Run("edge only is claimable", func(t *testing.T) {
		st := newClaimTestStore(t)
		graph, rev, err := st.LoadGraph()
		if err != nil {
			t.Fatal(err)
		}
		graph.NodeByID("build").Requires = ""
		if _, err := st.SaveGraph(identity.Local, graph, rev); err != nil {
			t.Fatal(err)
		}
		if _, err := st.ClaimNode(identity.Local, "build", "agent", 0, ""); err != nil {
			t.Fatalf("visual-only edge refused claim: %v", err)
		}
	})

	t.Run("requires only is not claimable", func(t *testing.T) {
		st := newClaimTestStore(t)
		graph, rev, err := st.LoadGraph()
		if err != nil {
			t.Fatal(err)
		}
		graph.Edges = nil
		if _, err := st.SaveGraph(identity.Local, graph, rev); err != nil {
			t.Fatal(err)
		}
		_, err = st.ClaimNode(identity.Local, "build", "agent", 0, "")
		var refused *ErrNotClaimable
		if !errors.As(err, &refused) {
			t.Fatalf("requires-only claim error = %v, want ErrNotClaimable", err)
		}
	})
}

// Retries and long tasks land in the same place: the holder asking again is
// extending its own lease, not competing with itself.
func TestTheSameHolderClaimingAgainExtendsItsLease(t *testing.T) {
	st := newClaimTestStore(t)
	base := time.Now()
	atTime(t, base)

	first, err := st.ClaimNode(identity.Local, "design", "agent", 10*time.Minute, "")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	atTime(t, base.Add(5*time.Minute))
	second, err := st.ClaimNode(identity.Local, "design", "agent", 10*time.Minute, "")
	if err != nil {
		t.Fatalf("re-claim: %v", err)
	}
	if !second.Claim.Expires.After(first.Claim.Expires) {
		t.Fatalf("lease did not extend: %v then %v", first.Claim.Expires, second.Claim.Expires)
	}
}

// An agent that died mid-task must not wedge the node until a person notices.
func TestAnExpiredLeaseLetsAnotherAgentTakeOver(t *testing.T) {
	st := newClaimTestStore(t)
	base := time.Now()
	atTime(t, base)
	if _, err := st.ClaimNode(identity.Local, "design", "first", time.Minute, ""); err != nil {
		t.Fatalf("claim: %v", err)
	}

	atTime(t, base.Add(90*time.Second))
	result, err := st.ClaimNode(identity.Local, "design", "second", 0, "")
	if err != nil {
		t.Fatalf("takeover: %v", err)
	}
	if result.Claim.Owner != "second" {
		t.Fatalf("owner = %q, want second", result.Claim.Owner)
	}
}

// The counterpart to the test above, and the reason the expired record is kept
// rather than deleted: a node a person moved to in_progress by hand has no
// claim record at all, and must never be taken out from under them.
func TestWorkAPersonStartedIsNotStealable(t *testing.T) {
	st := newClaimTestStore(t)
	if _, err := st.SetStatus("design", engine.StatusInProgress, "patrick", ""); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}

	_, err := st.ClaimNode(identity.Local, "design", "agent", 0, "")
	var refused *ErrNotClaimable
	if !errors.As(err, &refused) {
		t.Fatalf("error = %v, want the claim refused", err)
	}
}

func TestReportingOnANodeYouDoNotHoldIsRefused(t *testing.T) {
	st := newClaimTestStore(t)
	if _, err := st.ClaimNode(identity.Local, "design", "first", 0, ""); err != nil {
		t.Fatalf("claim: %v", err)
	}

	_, err := st.ReportStatus(identity.Local, "design", engine.StatusDone, "second", "", "second", "")
	var wrongOwner *ErrNotOwner
	if !errors.As(err, &wrongOwner) {
		t.Fatalf("error = %v, want ErrNotOwner", err)
	}
	if wrongOwner.Owner != "first" {
		t.Fatalf("error named %q as the holder, want first", wrongOwner.Owner)
	}
}

func TestFinishingWorkReleasesTheNode(t *testing.T) {
	st := newClaimTestStore(t)
	if _, err := st.ClaimNode(identity.Local, "design", "agent", 0, ""); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, err := st.ReportStatus(identity.Local, "design", engine.StatusDone, "agent", "built it", "agent", ""); err != nil {
		t.Fatalf("report: %v", err)
	}

	if st.ClaimFor("design") != nil {
		t.Fatal("a finished node is still held")
	}
	// And the node it was blocking is now available.
	if _, err := st.ClaimNode(identity.Local, "build", "agent", 0, ""); err != nil {
		t.Fatalf("the next task did not become claimable: %v", err)
	}
}

// An agent's claim exists to keep other agents off a node, not to lock a person
// out of their own board. The override goes through -- and is written down.
func TestAPersonMayOverrideAnAgentAndItIsRecorded(t *testing.T) {
	st := newClaimTestStore(t)
	if _, err := st.ClaimNode(identity.Local, "design", "mcp:agent", 0, ""); err != nil {
		t.Fatalf("claim: %v", err)
	}

	// No owner: this caller is not acting under a claim.
	rs, err := st.ReportStatus(identity.Local, "design", engine.StatusDone, "patrick", "done by hand", "", "")
	if err != nil {
		t.Fatalf("override: %v", err)
	}
	last := rs.History[len(rs.History)-1]
	if !strings.Contains(last.Note, "mcp:agent") {
		t.Fatalf("note = %q, want the override to name whose claim it took", last.Note)
	}
	if st.ClaimFor("design") != nil {
		t.Fatal("the overridden claim still holds the node")
	}
}

// A retry after the answer was lost must not write the work down twice: the
// timeline is what people read to reconstruct what happened.
func TestARetriedReportIsCarriedOutOnce(t *testing.T) {
	st := newClaimTestStore(t)
	if _, err := st.ClaimNode(identity.Local, "design", "agent", 0, ""); err != nil {
		t.Fatalf("claim: %v", err)
	}
	before, err := st.LoadState()
	if err != nil {
		t.Fatal(err)
	}

	for range 3 {
		if _, err := st.ReportStatus(identity.Local,
			"design", engine.StatusDone, "agent", "finished", "agent", "req-1",
		); err != nil {
			t.Fatalf("report: %v", err)
		}
	}

	after, err := st.LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if added := len(after.History) - len(before.History); added != 1 {
		t.Fatalf("%d events were written for one reported result", added)
	}
}

func TestReleasingANodePutsItBackInTheQueue(t *testing.T) {
	st := newClaimTestStore(t)
	if _, err := st.ClaimNode(identity.Local, "design", "agent", 0, ""); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if err := st.ReleaseClaim("design", "agent"); err != nil {
		t.Fatalf("release: %v", err)
	}

	if got := statusOf(t, st, "design"); got != engine.StatusReady {
		t.Fatalf("status = %q, want ready again", got)
	}
	if _, err := st.ClaimNode(identity.Local, "design", "other", 0, ""); err != nil {
		t.Fatalf("the released node was not claimable: %v", err)
	}
}

func TestReleasingSomebodyElsesClaimIsRefused(t *testing.T) {
	st := newClaimTestStore(t)
	if _, err := st.ClaimNode(identity.Local, "design", "first", 0, ""); err != nil {
		t.Fatalf("claim: %v", err)
	}

	var wrongOwner *ErrNotOwner
	if err := st.ReleaseClaim("design", "second"); !errors.As(err, &wrongOwner) {
		t.Fatalf("error = %v, want ErrNotOwner", err)
	}
}

// Claims are operational scratch state. A project must still open if the file
// is truncated, half-written or edited by hand into nonsense.
func TestACorruptClaimsFileIsTreatedAsNoClaims(t *testing.T) {
	st := newClaimTestStore(t)
	if _, err := st.ClaimNode(identity.Local, "design", "agent", 0, ""); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if err := os.WriteFile(st.claimsPath(), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	if st.ClaimFor("design") != nil {
		t.Fatal("a corrupt file produced a claim")
	}
	if got := st.LiveClaims(); len(got) != 0 {
		t.Fatalf("live claims = %v, want none", got)
	}
}
