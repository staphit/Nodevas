package engine

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"strings"
	"time"
)

// Status is a node's lifecycle state.
type Status string

const (
	StatusReady      Status = "ready"
	StatusStarted    Status = "started"
	StatusInProgress Status = "in_progress"
	StatusDone       Status = "done"
	StatusSkipped    Status = "skipped"
	StatusFailed     Status = "failed"
	// StatusDeprecated marks work that is no longer part of the plan. Like
	// done and skipped it settles the node, so whatever depended on it is
	// released rather than blocked forever.
	StatusDeprecated Status = "deprecated"
)

// Explicit statuses are stored in RunState as lifecycle records
// (開始/進行/完成…).
func (s Status) explicit() bool {
	return ValidStatus(s)
}

// ValidStatus reports whether s can be written as a lifecycle event.
// Locked is decode-only; ready is the user-visible "not started" state.
func ValidStatus(s Status) bool {
	switch s {
	case StatusReady, StatusStarted, StatusInProgress, StatusDone, StatusSkipped,
		StatusFailed, StatusDeprecated:
		return true
	}
	const customPrefix = "custom-status-"
	return strings.HasPrefix(string(s), customPrefix) &&
		len(strings.TrimPrefix(string(s), customPrefix)) > 0
}

// CanTransition accepts every user-visible state. Status changes are an
// editable record, not a workflow lock; each change is still journaled.
func CanTransition(_, to Status) bool {
	return ValidStatus(to)
}

// RunState mirrors run/state.json. Always recomputable from graph + history;
// the stored node map is a cache of the latest explicit statuses.
type RunState struct {
	StartedAt string                `json:"startedAt,omitempty"`
	Nodes     map[string]*NodeState `json:"nodes"`
	Flags     map[string]any        `json:"flags,omitempty"`
	History   []HistoryEvent        `json:"history"`
}

type NodeState struct {
	Status Status `json:"status"`
	At     string `json:"at,omitempty"` // RFC3339
	By     string `json:"by,omitempty"`
}

// HistoryEvent records one status transition — this feeds the timeline view
// (one lane per node, a colored marker on the day the transition happened).
// Note and Flags capture the situation at that moment.
type HistoryEvent struct {
	ID    string         `json:"id,omitempty"`
	T     string         `json:"t"`     // RFC3339
	Event string         `json:"event"` // "status" | "move"
	Node  string         `json:"node,omitempty"`
	From  Status         `json:"from,omitempty"`
	To    Status         `json:"to,omitempty"`
	By    string         `json:"by,omitempty"`
	Note  string         `json:"note,omitempty"`
	Flags map[string]any `json:"flags,omitempty"`
	// RecordedAt is the append/audit time for events whose T has another
	// meaning. Move events use T as the corrected display time.
	RecordedAt string `json:"recordedAt,omitempty"`
	// StateFlags is the durable runtime override map. Flags above is the
	// effective display snapshot (graph defaults + overrides).
	StateFlags map[string]any `json:"stateFlags"`
	// move events: Ref points at the status event whose display time
	// becomes this event's T. The original line is never rewritten —
	// the journal stays append-only and the move is itself the audit.
	Ref string `json:"ref,omitempty"`
}

// NewRunState returns an empty state stamped with now.
func NewRunState(now time.Time) *RunState {
	return &RunState{
		StartedAt: now.UTC().Format(time.RFC3339),
		Nodes:     map[string]*NodeState{},
		History:   []HistoryEvent{},
	}
}

// ParseRunState decodes run/state.json bytes.
func ParseRunState(data []byte) (*RunState, error) {
	var rs RunState
	if err := json.Unmarshal(data, &rs); err != nil {
		return nil, fmt.Errorf("state.json: %w", err)
	}
	if rs.Nodes == nil {
		rs.Nodes = map[string]*NodeState{}
	}
	return &rs, nil
}

// MarshalRunState encodes state to pretty JSON (it lives in the project dir;
// humans and agents read it).
func MarshalRunState(rs *RunState) ([]byte, error) {
	return json.MarshalIndent(rs, "", "  ")
}

// SetStatus applies any valid user-selected status and appends to history.
// A missing state is treated as ready.
func (rs *RunState) SetStatus(nodeID string, to Status, by string, now time.Time) error {
	if !ValidStatus(to) {
		return fmt.Errorf("invalid status %q", to)
	}
	from := StatusReady
	if st := rs.Nodes[nodeID]; st != nil && st.Status.explicit() {
		from = st.Status
	}
	if !CanTransition(from, to) {
		return fmt.Errorf("invalid status transition %s -> %s", from, to)
	}
	ts := now.UTC().Format(time.RFC3339)
	rs.Nodes[nodeID] = &NodeState{Status: to, At: ts, By: by}
	rs.History = append(rs.History, HistoryEvent{
		ID: fmt.Sprintf("ev-%d-%d", now.UnixNano(), len(rs.History)),
		T:  ts, Event: "status", Node: nodeID, From: from, To: to, By: by,
	})
	return nil
}

// AppendJournalLine renders one history event as a JSONL line — the
// append-only journal (run/journal.jsonl) is the durable source of truth
// for run state; state.json is a rebuildable cache.
func AppendJournalLine(ev HistoryEvent) ([]byte, error) {
	b, err := json.Marshal(ev)
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// StateFromJournal rebuilds a RunState by replaying a JSONL journal.
// Malformed lines are skipped (a torn final line after a crash is expected).
func StateFromJournal(data []byte) *RunState {
	rs, _ := StateFromJournalChecked(data)
	return rs
}

// StateFromJournalChecked rebuilds state and reports corrupted complete
// records. Only a malformed final unterminated line is tolerated because it
// can be left behind by a crash during append.
func StateFromJournalChecked(data []byte) (*RunState, error) {
	return ResumeJournal(nil, data)
}

// ResumeJournal replays data on top of an already-reduced state — this is what
// lets a checkpoint stand in for the journal prefix it covers, so replay does
// not have to start from event zero every time. base may be nil (start empty);
// when it is not, it is mutated in place and returned.
//
// The result is identical to replaying base's own journal prefix followed by
// data in one pass, so a compacted project and an un-compacted one reduce to
// the same state.
func ResumeJournal(base *RunState, data []byte) (*RunState, error) {
	replay := newJournalReplay(base)
	rs := replay.rs
	lines := bytes.Split(data, []byte{'\n'})
	for index, line := range lines {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var ev HistoryEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			isTornTail := index == len(lines)-1 && !bytes.HasSuffix(data, []byte{'\n'})
			if isTornTail {
				break
			}
			return rs, fmt.Errorf("journal line %d: %w", index+1, err)
		}
		switch {
		case ev.Event == "status" && ev.Node != "" && ev.ID != "":
			replay.status(ev)
		case ev.Event == "move" && ev.Ref != "":
			replay.move(ev)
		}
	}
	return rs, nil
}

// journalReplay carries the indexes that keep replay linear in the number of
// events. Without them every move event rescans the whole history twice, which
// made a long-lived journal quadratic to load.
type journalReplay struct {
	rs *RunState
	// statusIndex maps a status event id to its first position in History,
	// matching the first-match rule the original linear scan used.
	statusIndex map[string]int
	// latestStatus maps a node id to the position of its newest status event.
	latestStatus map[string]int
}

func newJournalReplay(base *RunState) *journalReplay {
	rs := base
	if rs == nil {
		rs = &RunState{}
	}
	if rs.Nodes == nil {
		rs.Nodes = map[string]*NodeState{}
	}
	if rs.History == nil {
		rs.History = []HistoryEvent{}
	}
	replay := &journalReplay{
		rs:           rs,
		statusIndex:  make(map[string]int, len(rs.History)),
		latestStatus: map[string]int{},
	}
	for i := range rs.History {
		replay.index(i)
	}
	return replay
}

func (r *journalReplay) index(i int) {
	ev := &r.rs.History[i]
	if ev.Event != "status" {
		return
	}
	if ev.ID != "" {
		if _, seen := r.statusIndex[ev.ID]; !seen {
			r.statusIndex[ev.ID] = i
		}
	}
	if ev.Node != "" {
		r.latestStatus[ev.Node] = i
	}
}

func (r *journalReplay) appendHistory(ev HistoryEvent) {
	r.rs.History = append(r.rs.History, ev)
	r.index(len(r.rs.History) - 1)
}

// status rebuilds effective state from one appended lifecycle event.
func (r *journalReplay) status(ev HistoryEvent) {
	rs := r.rs
	target := ev.To
	if !ValidStatus(target) {
		return
	}
	from := StatusReady
	if current := rs.Nodes[ev.Node]; current != nil {
		from = current.Status
	}
	ev.From = from
	r.appendHistory(ev)
	rs.Nodes[ev.Node] = &NodeState{Status: target, At: ev.T, By: ev.By}
	if ev.StateFlags != nil {
		rs.Flags = maps.Clone(ev.StateFlags)
	}
	if rs.StartedAt == "" {
		rs.StartedAt = ev.T
	}
}

// move retargets the referenced stamp's display time; the move itself stays in
// history as the audit trail.
func (r *journalReplay) move(ev HistoryEvent) {
	if i, ok := r.statusIndex[ev.Ref]; ok {
		node := r.rs.History[i].Node
		if state := r.rs.Nodes[node]; state != nil && r.latestStatus[node] == i {
			state.At = ev.T
		}
		r.rs.History[i].T = ev.T
	}
	r.appendHistory(ev)
}

// EffectiveFlags merges graph defaults with runtime overrides.
func EffectiveFlags(g *Graph, rs *RunState) map[string]any {
	out := map[string]any{}
	maps.Copy(out, g.Flags)
	if rs != nil {
		maps.Copy(out, rs.Flags)
	}
	return out
}

// ComputeStatuses returns the effective lifecycle status of every node.
// Explicit history wins; nodes without history are ready ("未開始").
// Requires expressions remain graph metadata and validation input, but do not
// lock lifecycle transitions.
func ComputeStatuses(g *Graph, rs *RunState) map[string]Status {
	out := map[string]Status{}
	for _, n := range g.Nodes {
		if n == nil {
			continue
		}
		if rs != nil {
			if st := rs.Nodes[n.ID]; st != nil && st.Status.explicit() {
				out[n.ID] = st.Status
				continue
			}
		}
		out[n.ID] = StatusReady
	}
	return out
}
