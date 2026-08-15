// Claims: who is working on a node right now.
//
// The graph has never recorded this. A status says what state a node is in, not
// who is holding it, which is enough when the people editing can see each other
// in the room and talk. It stops being enough the moment two agents read the
// same ready queue: both would see the same node, both would start, and the
// only sign of it would be two sets of changes to the same file.
//
// So claiming is a server-side operation with the check and the write in one
// critical section. Nothing about "read the queue, then set a status" can be
// made safe from the outside, however carefully a client sequences it.
//
// The mutual exclusion is this process's own mutex, and that is sufficient
// rather than merely convenient: the workspace lock is exclusive, so there is
// exactly one process serving a workspace, and StoreFor hands every caller the
// same *Store for a directory. A second process cannot exist to race with.

package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"nodevas/internal/engine"
	"nodevas/internal/identity"
)

// claimClock is time.Now, replaced in tests that need a lease to expire
// without the test sleeping through it.
var claimClock = time.Now

// Lease bounds.
const (
	// DefaultLease is how long a claim holds if the caller does not say. Long
	// enough for a substantial task, short enough that an agent which died
	// mid-task does not wedge the node until someone notices.
	DefaultLease = 30 * time.Minute
	MinLease     = time.Minute
	MaxLease     = 8 * time.Hour

	// claimRetention is how long a finished or expired claim record is kept.
	// The record is what distinguishes "an agent abandoned this" from "a person
	// started it by hand", so dropping it is not free; see ClaimNode.
	claimRetention = 7 * 24 * time.Hour

	// Replay protection bounds. A retry arrives seconds after the call it
	// repeats, so an hour is generous, and the count stops a long-running
	// server from growing the file without limit.
	replayRetention = time.Hour
	maxReplayed     = 256

	maxClaimsFileBytes = 4 << 20
)

// Claim is one holder's lease on a node.
type Claim struct {
	NodeID  string    `json:"nodeId"`
	Owner   string    `json:"owner"`
	At      time.Time `json:"at"`
	Expires time.Time `json:"expires"`
	// Released is set when the holder finished or gave the node back. The
	// record outlives the hold on purpose.
	Released bool `json:"released,omitempty"`
}

// Live reports whether this claim still holds the node.
func (c *Claim) Live(now time.Time) bool {
	return c != nil && !c.Released && now.Before(c.Expires)
}

// claimsFile is the whole sidecar.
type claimsFile struct {
	Claims map[string]*Claim `json:"claims"`
	// Replayed remembers the request ids already carried out, so a retried
	// call answers with what the first one did rather than doing it twice.
	Replayed map[string]*replayedRequest `json:"replayed,omitempty"`
}

// replayedRequest records only that a request was carried out, not what it
// produced.
//
// Storing the answer was the first shape of this and was wrong twice over: a
// RunState carries the whole journal, so a few hundred remembered requests
// would have dwarfed the claims they sit beside, and a replayed answer would
// describe a board that has since moved on. The effect happened once; the
// current state is what is true now, and that is what a retry should hear.
type replayedRequest struct {
	At time.Time `json:"at"`
}

// ErrAlreadyClaimed is returned when somebody else holds the node.
type ErrAlreadyClaimed struct {
	Owner   string
	Expires time.Time
}

func (e *ErrAlreadyClaimed) Error() string {
	return fmt.Sprintf("node is already claimed by %q until %s",
		e.Owner, e.Expires.Format(time.RFC3339))
}

// ErrNotOwner is returned when a caller tries to finish work it does not hold.
type ErrNotOwner struct {
	Owner string
}

func (e *ErrNotOwner) Error() string {
	if e.Owner == "" {
		// What to do about it is added by the caller that knows what its tools
		// are called; saying it here too produced the same sentence twice.
		return "this node is not claimed by anybody"
	}
	return fmt.Sprintf("this node is held by %q", e.Owner)
}

// ErrNotClaimable is returned when the node cannot be picked up at all.
type ErrNotClaimable struct {
	NodeID string
	Reason string
}

func (e *ErrNotClaimable) Error() string {
	return fmt.Sprintf("node %q cannot be claimed: %s", e.NodeID, e.Reason)
}

func (s *Store) claimsPath() string {
	return filepath.Join(s.root, DataDir, "claims.json")
}

// loadClaimsLocked reads the sidecar. A missing or unreadable file is an empty
// set rather than an error: claims are operational state, and refusing to serve
// a project because a scratch file went bad would be the worse failure.
func (s *Store) loadClaimsLocked() *claimsFile {
	file := &claimsFile{Claims: map[string]*Claim{}, Replayed: map[string]*replayedRequest{}}
	raw, err := ReadProjectFileLimit(s.root, s.claimsPath(), maxClaimsFileBytes)
	if err != nil {
		return file
	}
	if err := json.Unmarshal(raw, file); err != nil {
		return &claimsFile{Claims: map[string]*Claim{}, Replayed: map[string]*replayedRequest{}}
	}
	if file.Claims == nil {
		file.Claims = map[string]*Claim{}
	}
	if file.Replayed == nil {
		file.Replayed = map[string]*replayedRequest{}
	}
	return file
}

// pruneLocked drops what is too old to mean anything.
func (file *claimsFile) prune(now time.Time) {
	for id, claim := range file.Claims {
		if claim == nil || now.Sub(claim.Expires) > claimRetention {
			delete(file.Claims, id)
		}
	}
	for id, entry := range file.Replayed {
		if entry == nil || now.Sub(entry.At) > replayRetention {
			delete(file.Replayed, id)
		}
	}
	if len(file.Replayed) > maxReplayed {
		ids := make([]string, 0, len(file.Replayed))
		for id := range file.Replayed {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(a, b int) bool {
			return file.Replayed[ids[a]].At.After(file.Replayed[ids[b]].At)
		})
		for _, id := range ids[maxReplayed:] {
			delete(file.Replayed, id)
		}
	}
}

func (s *Store) saveClaimsLocked(file *claimsFile) error {
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	if err := MkdirAllProjectPath(s.root, filepath.Dir(s.claimsPath()), 0o755); err != nil {
		return err
	}
	return s.WriteAtomic(s.claimsPath(), append(data, '\n'))
}

// ClaimResult is what a successful claim hands back.
type ClaimResult struct {
	Claim *Claim            `json:"claim"`
	State *engine.RunState  `json:"-"`
	Node  *engine.ReadyNode `json:"task,omitempty"`
}

// ClaimNode takes a node for one holder and moves it to in_progress.
//
// The whole check happens under the store's write lock: whether the node
// exists, whether it is actionable, whether anybody else holds it, and the
// status write itself. Two agents calling this at the same moment are
// serialized here, and the second one is told who won.
//
// A node is claimable when it is ready and unblocked, or when it is in_progress
// and carries a claim record whose lease has run out — an agent that died
// mid-task should not wedge the node forever. A node a *person* moved to
// in_progress has no claim record, so it is never taken out from under them.
// When a record is eventually pruned the node stops being reclaimable, which is
// the safe direction to fail in.
func (s *Store) ClaimNode(actor identity.Actor, nodeID, owner string, lease time.Duration, requestID string) (*ClaimResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	owner = strings.TrimSpace(owner)
	if owner == "" {
		return nil, errors.New("a claim needs an owner")
	}
	now := claimClock()
	file := s.loadClaimsLocked()
	file.prune(now)

	if file.alreadyDone(requestID) {
		state, err := s.LoadState()
		if err != nil {
			return nil, err
		}
		return &ClaimResult{Claim: file.Claims[nodeID], State: state}, nil
	}

	g, _, err := s.loadGraphLocked()
	if err != nil {
		return nil, err
	}
	if g.NodeByID(nodeID) == nil {
		return nil, fmt.Errorf("node %q not found", nodeID)
	}
	if err := checkNodeWrite(actor, g.NodeByID(nodeID)); err != nil {
		return nil, err
	}
	rs, err := s.LoadState()
	if err != nil {
		return nil, err
	}

	existing := file.Claims[nodeID]
	if existing.Live(now) {
		if existing.Owner != owner {
			return nil, &ErrAlreadyClaimed{Owner: existing.Owner, Expires: existing.Expires}
		}
		// The same holder asking again is extending its own lease, not
		// competing with itself. Retries and long tasks both land here.
		existing.Expires = now.Add(clampLease(lease))
		file.record(requestID, now)
		if err := s.saveClaimsLocked(file); err != nil {
			return nil, err
		}
		return &ClaimResult{Claim: existing, State: rs}, nil
	}

	readiness := engine.ComputeReadiness(g, rs)
	claimable := false
	var task *engine.ReadyNode
	for index, candidate := range readiness.Ready {
		if candidate.ID == nodeID {
			claimable = true
			task = &readiness.Ready[index]
			break
		}
	}
	if !claimable {
		// The one other way in: reclaiming a node whose holder gave up.
		if existing != nil && !existing.Released &&
			engine.ComputeStatuses(g, rs)[nodeID] == engine.StatusInProgress {
			claimable = true
		}
	}
	if !claimable {
		return nil, &ErrNotClaimable{NodeID: nodeID, Reason: reasonNotClaimable(readiness, nodeID)}
	}

	if _, err := s.setStatusLocked(nodeID, engine.StatusInProgress, owner,
		"claimed by "+owner); err != nil {
		return nil, err
	}
	claim := &Claim{
		NodeID:  nodeID,
		Owner:   owner,
		At:      now,
		Expires: now.Add(clampLease(lease)),
	}
	file.Claims[nodeID] = claim
	file.record(requestID, now)
	if err := s.saveClaimsLocked(file); err != nil {
		return nil, err
	}
	state, err := s.LoadState()
	if err != nil {
		return nil, err
	}
	return &ClaimResult{Claim: claim, State: state, Node: task}, nil
}

// reasonNotClaimable says why in words the caller can act on.
func reasonNotClaimable(readiness engine.Readiness, nodeID string) string {
	for _, blocked := range readiness.Blocked {
		if blocked.ID != nodeID {
			continue
		}
		if blocked.Reason == "requires" {
			return "its stated condition does not hold yet"
		}
		if blocked.Reason == "gate_condition" {
			condition := "logic gate"
			if blocked.GateOperator != "" {
				condition = strings.ToUpper(blocked.GateOperator) + " gate"
			}
			if len(blocked.BlockedBy) == 0 {
				return "its " + condition + " condition does not hold"
			}
			return "its " + condition + " condition does not hold (inputs: " +
				strings.Join(blocked.BlockedBy, ", ") + ")"
		}
		return "it is waiting on " + strings.Join(blocked.BlockedBy, ", ")
	}
	return "it has already been started or finished"
}

// ReleaseClaim gives a node back without finishing it.
//
// It takes no actor and applies no write-access check on purpose: if the
// node's access is tightened while an agent holds it, the agent must still be
// able to hand the node back rather than wedge it until the lease expires.
func (s *Store) ReleaseClaim(nodeID, owner string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := claimClock()
	file := s.loadClaimsLocked()
	file.prune(now)
	claim := file.Claims[nodeID]
	if !claim.Live(now) {
		return &ErrNotOwner{}
	}
	if claim.Owner != strings.TrimSpace(owner) {
		return &ErrNotOwner{Owner: claim.Owner}
	}
	claim.Released = true
	// The node goes back to where it was found, or the queue would never offer
	// it again.
	if _, err := s.setStatusLocked(nodeID, engine.StatusReady, owner,
		"released by "+owner); err != nil {
		return err
	}
	return s.saveClaimsLocked(file)
}

// ClaimFor returns the live claim on a node, if any.
func (s *Store) ClaimFor(nodeID string) *Claim {
	s.mu.Lock()
	defer s.mu.Unlock()
	claim := s.loadClaimsLocked().Claims[nodeID]
	if !claim.Live(claimClock()) {
		return nil
	}
	return claim
}

// LiveClaims lists every node currently held.
func (s *Store) LiveClaims() []*Claim {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := claimClock()
	var live []*Claim
	for _, claim := range s.loadClaimsLocked().Claims {
		if claim.Live(now) {
			live = append(live, claim)
		}
	}
	sort.Slice(live, func(a, b int) bool { return live[a].NodeID < live[b].NodeID })
	return live
}

// ReportStatus is SetStatus with the ownership rules applied.
//
// `by` is who to record. `owner` is who the caller claims to be holding the
// node as; an empty owner means the caller is not acting as a claim holder --
// a person in the editor -- and is allowed through. That asymmetry is
// deliberate: an agent's claim exists to keep other agents off the node, not to
// lock a person out of their own board. The override is recorded rather than
// refused, so nothing happens silently.
func (s *Store) ReportStatus(
	actor identity.Actor, nodeID string, status engine.Status, by, note, owner, requestID string,
) (*engine.RunState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	g, _, err := s.loadGraphLocked()
	if err != nil {
		return nil, err
	}
	if err := checkNodeWrite(actor, g.NodeByID(nodeID)); err != nil {
		return nil, err
	}

	now := claimClock()
	file := s.loadClaimsLocked()
	file.prune(now)

	if file.alreadyDone(requestID) {
		return s.LoadState()
	}

	claim := file.Claims[nodeID]
	owner = strings.TrimSpace(owner)
	switch {
	case owner == "":
		// A person, or any caller not working under a claim. If an agent holds
		// the node, the override is noted in the timeline where the agent's own
		// entries are.
		if claim.Live(now) && claim.Owner != by {
			note = strings.TrimSpace(note + " (overriding " + claim.Owner + ")")
			claim.Released = true
		}
	case !claim.Live(now):
		return nil, &ErrNotOwner{}
	case claim.Owner != owner:
		return nil, &ErrNotOwner{Owner: claim.Owner}
	}

	rs, err := s.setStatusLocked(nodeID, status, by, note)
	if err != nil {
		return nil, err
	}
	// Finishing releases the node. Leaving the claim standing would keep every
	// completed task nominally held until its lease ran out.
	if claim != nil && engine.Settled(status, definitionsOf(s)) {
		claim.Released = true
	}
	file.record(requestID, now)
	if err := s.saveClaimsLocked(file); err != nil {
		return nil, err
	}
	return rs, nil
}

// definitionsOf reads the project's custom lifecycle states. The caller already
// holds the lock, so the graph is loaded through the locked path.
func definitionsOf(s *Store) []engine.StatusDefinition {
	g, _, err := s.loadGraphLocked()
	if err != nil || g == nil || g.UI == nil {
		return nil
	}
	return g.UI.CustomStatuses
}

func clampLease(lease time.Duration) time.Duration {
	switch {
	case lease <= 0:
		return DefaultLease
	case lease < MinLease:
		return MinLease
	case lease > MaxLease:
		return MaxLease
	}
	return lease
}

// alreadyDone reports whether a request id has been carried out before.
//
// Without this a retried write -- the client's connection dropped after the
// server acted but before the answer arrived -- appends a second journal event
// for work that happened once. The timeline is the record people trust to
// reconstruct what happened, and a duplicated entry in it is worse than the
// failed request the retry was covering for.
func (file *claimsFile) alreadyDone(requestID string) bool {
	if requestID == "" {
		return false
	}
	entry, ok := file.Replayed[requestID]
	return ok && entry != nil
}

// record marks a request id as carried out.
func (file *claimsFile) record(requestID string, now time.Time) {
	if requestID == "" {
		return
	}
	file.Replayed[requestID] = &replayedRequest{At: now}
}
