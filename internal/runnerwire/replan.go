package runnerwire

import (
	"strings"
	"unicode/utf8"
)

// Replan (W7): the person changed a goal's requirement after it was broken
// down. The hub appends the change to the goal under a dated "Revisions"
// heading and dispatches a plan run whose Job.Review is the revision: the
// change and the goal's tasks as they stand (CreateRunRequest.Revision).
//
// The planner works as a first breakdown does — it reads the goal and the
// code, and creates new tasks with create_node and link_nodes — but it does
// not touch the tasks that exist. It says what should happen to each of them
// in a reconciliation block that ends its final message:
//
//	RECONCILE:
//	- keep t1
//	- change t2: also export the totals row
//	- skip t3: the PDF export was dropped
//	- flag t4: its run is in progress; it must also cover the header row
//	- add t9: Export the totals row
//	END RECONCILE
//
// The hub applies the block once the run ends, and only what the rules allow
// (a done task stays done, a task with a run in progress is never changed),
// so a planner, or text it read in the repository, cannot do more than that.

// Reconciliation block markers and actions.
const (
	ReconcileStart = "RECONCILE:"
	ReconcileEnd   = "END RECONCILE"

	ReconcileKeep   = "keep"
	ReconcileChange = "change"
	ReconcileSkip   = "skip"
	ReconcileFlag   = "flag"
	ReconcileAdd    = "add"
)

// Bounds on a revision and its reconciliation.
const (
	// MaxRevisionBytes caps CreateRunRequest.Revision.
	MaxRevisionBytes = 16 << 10
	// MaxReconcileItems caps the lines read from a block.
	MaxReconcileItems = 100
	// MaxReconcileNote caps one line's note, in bytes.
	MaxReconcileNote = 300
)

// ReconcileItem is one line of a reconciliation block.
type ReconcileItem struct {
	Action string
	NodeID string
	Note   string
}

// ParseReconcile reads the last reconciliation block in text. ok is false
// when there is none (an older runner, or a planner that left it out).
// Lines that are not "<action> <id>[: note]" are ignored.
func ParseReconcile(text string) (items []ReconcileItem, ok bool) {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	start := -1
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.EqualFold(markerText(lines[i]), ReconcileStart) {
			start = i
			break
		}
	}
	if start < 0 {
		return nil, false
	}
	for _, line := range lines[start+1:] {
		bare := markerText(line)
		if strings.EqualFold(bare, ReconcileEnd) {
			break
		}
		item, valid := parseReconcileLine(bare)
		if !valid {
			continue
		}
		items = append(items, item)
		if len(items) == MaxReconcileItems {
			break
		}
	}
	return items, true
}

// markerText strips the markdown an agent tends to wrap a line in.
func markerText(line string) string {
	return strings.TrimSpace(strings.Trim(strings.TrimSpace(line), "*_`>#"))
}

func parseReconcileLine(line string) (ReconcileItem, bool) {
	line = strings.TrimSpace(strings.TrimLeft(line, "-*•+ \t"))
	head, note, _ := strings.Cut(line, ":")
	fields := strings.Fields(head)
	if len(fields) != 2 {
		return ReconcileItem{}, false
	}
	action := strings.ToLower(strings.Trim(fields[0], "*_`"))
	switch action {
	case ReconcileKeep, ReconcileChange, ReconcileSkip, ReconcileFlag, ReconcileAdd:
	default:
		return ReconcileItem{}, false
	}
	id := strings.Trim(fields[1], "*_`\"'()[]")
	if id == "" || len(id) > 128 {
		return ReconcileItem{}, false
	}
	note = strings.Join(strings.Fields(note), " ")
	if len(note) > MaxReconcileNote {
		note = cutUTF8(note, MaxReconcileNote) + "…"
	}
	return ReconcileItem{Action: action, NodeID: id, Note: note}, true
}

// FormatReconcile writes items as a block, at most limit bytes: keep lines
// go first when it does not fit (keeping is what an absent line means
// anyway), then the last lines.
func FormatReconcile(items []ReconcileItem, limit int) string {
	render := func(list []ReconcileItem) string {
		var b strings.Builder
		b.WriteString(ReconcileStart + "\n")
		for _, item := range list {
			b.WriteString("- " + item.Action + " " + item.NodeID)
			if item.Note != "" {
				b.WriteString(": " + item.Note)
			}
			b.WriteString("\n")
		}
		b.WriteString(ReconcileEnd)
		return b.String()
	}
	out := render(items)
	if len(out) <= limit {
		return out
	}
	kept := make([]ReconcileItem, 0, len(items))
	for _, item := range items {
		if item.Action != ReconcileKeep {
			kept = append(kept, item)
		}
	}
	for out = render(kept); len(out) > limit && len(kept) > 0; out = render(kept) {
		kept = kept[:len(kept)-1]
	}
	return out
}

func cutUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
