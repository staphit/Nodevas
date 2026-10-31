package agents

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	// RunAskPreviewRunes caps a timeline entry's one-line texts: a
	// permission prompt's input preview, a question, a note.
	RunAskPreviewRunes = 120
	// MaxRunAsks caps the asks a run's timeline lists; the newest are kept.
	MaxRunAsks = 200
)

// RunAsk is one ask of a run as the run's timeline shows it to a viewer
// (Hub.RunAsks): enough to say what the run waits on and how that ended,
// never a permission prompt's whole input. Preview and Note are for those
// who may answer the ask (Viewer.MayAnswer), as in the ask listing
// (Viewer.Shown); everyone else sees the tool, or a question's text, alone.
type RunAsk struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Status string `json:"status"`
	// Decision is DecisionOf the ask: "" while pending or once expired.
	Decision string `json:"decision,omitempty"`
	// Question is an approval's, question's or choice's text on one line,
	// cut to RunAskPreviewRunes. A permission prompt has none: its relayed
	// question quotes the input.
	Question string `json:"question,omitempty"`
	Tool     string `json:"tool,omitempty"`
	// Preview is a permission prompt's input on one line, cut to
	// RunAskPreviewRunes: the command or path when the input names one.
	Preview string `json:"preview,omitempty"`
	// Note is what the person answered the ask with, cut like Preview.
	Note        string `json:"note,omitempty"`
	SessionName string `json:"sessionName,omitempty"`
	CreatedAt   int64  `json:"createdAt"`
	DecidedAt   int64  `json:"decidedAt,omitempty"`
	// CanAnswer: the ask still waits and the viewer is a person who may
	// answer it.
	CanAnswer bool `json:"canAnswer"`
}

// RunAsksResponse is the body of GET /api/runs/:id/asks.
type RunAsksResponse struct {
	Asks []RunAsk `json:"asks"`
}

// allowedWithoutAsking reports whether a is a permission prompt the hub
// allowed on an earlier "allow for this run" without it ever waiting
// (allowForRun). A run makes one per tool call, and the call itself is in
// the run's events already, so the timeline leaves them out: the entry of
// the prompt that was allowed for the run says why nobody was asked.
func allowedWithoutAsking(a Ask) bool {
	return a.Status == AskApproved && a.ExpiresAt == 0 && a.Note == AllowedForRunNote
}

// RunAsks returns the asks of run runID in project, oldest first and at most
// MaxRunAsks of them (the newest), each as v may see it in the run's
// timeline. Prompts allowed without asking are left out.
func (h *Hub) RunAsks(ctx context.Context, project, runID string, v Viewer) ([]RunAsk, error) {
	project = strings.Trim(strings.TrimSpace(project), "/")
	runID = strings.TrimSpace(runID)
	out := []RunAsk{}
	if runID == "" {
		return out, nil
	}
	// An ask past its time shows as expired, as in the listing.
	h.expireStaleAsks(ctx, project)
	var asks []Ask
	if h.database == nil {
		h.mu.Lock()
		for _, a := range h.memAsks {
			if a.Project == project && a.RunID == runID && !allowedWithoutAsking(*a) {
				asks = append(asks, *a)
			}
		}
		h.mu.Unlock()
		sort.Slice(asks, func(i, j int) bool {
			if asks[i].CreatedAt != asks[j].CreatedAt {
				return asks[i].CreatedAt < asks[j].CreatedAt
			}
			return asks[i].ID < asks[j].ID
		})
		if len(asks) > MaxRunAsks {
			asks = asks[len(asks)-MaxRunAsks:]
		}
	} else {
		rows, err := h.database.QueryContext(ctx, `
			SELECT `+askColumns+`
			  FROM agent_asks
			 WHERE project = ? AND run_id = ?
			   AND NOT (status = 'approved' AND expires_at = 0 AND note = ?)
			 ORDER BY created_at DESC, id DESC LIMIT ?`,
			project, runID, AllowedForRunNote, MaxRunAsks)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			a, err := scanAsk(rows)
			if err != nil {
				return nil, err
			}
			asks = append(asks, a)
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
		for i, j := 0, len(asks)-1; i < j; i, j = i+1, j-1 {
			asks[i], asks[j] = asks[j], asks[i]
		}
	}
	for _, a := range asks {
		out = append(out, runAskFor(a, v))
	}
	return out, nil
}

// runAskFor is a as v sees it in its run's timeline.
func runAskFor(a Ask, v Viewer) RunAsk {
	may := v.MayAnswer(a)
	e := RunAsk{
		ID:          a.ID,
		Kind:        a.Kind,
		Status:      a.Status,
		Decision:    DecisionOf(a),
		Tool:        a.Tool,
		SessionName: a.SessionName,
		CreatedAt:   a.CreatedAt,
		DecidedAt:   a.DecidedAt,
		CanAnswer:   may && !v.Agent && a.Status == AskPending,
	}
	if a.Kind != AskKindPermission {
		e.Question = previewLine(a.Question)
	}
	if !may {
		return e
	}
	if a.Kind == AskKindPermission {
		e.Preview = inputPreview(a.ToolInput)
	}
	// The hub's own note on a prompt released by an allow for the run says
	// what the decision does already.
	if a.Note != AllowedForRunNote {
		e.Note = previewLine(a.Note)
	}
	return e
}

// previewKeys are the tool-input fields (lower case) a preview shows alone,
// in order of preference: what a call runs or touches.
var previewKeys = []string{
	"command", "commandline", "cmd", "file_path", "filepath", "notebook_path", "path", "url", "pattern", "query",
}

// inputPreview is a permission prompt's input on one line: the command or
// path it names when it is a JSON object with one, else the input itself.
func inputPreview(input string) string {
	var fields map[string]any
	if json.Unmarshal([]byte(input), &fields) == nil && fields != nil {
		lower := make(map[string]string, len(fields))
		for key, value := range fields {
			if text, ok := value.(string); ok {
				lower[strings.ToLower(key)] = text
			}
		}
		for _, key := range previewKeys {
			if text := previewLine(lower[key]); text != "" {
				return text
			}
		}
	}
	return previewLine(input)
}

// previewLine is s with its whitespace runs made single spaces, cut to
// RunAskPreviewRunes characters (an ellipsis included).
func previewLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= RunAskPreviewRunes {
		return s
	}
	runes := []rune(s)
	return strings.TrimRight(string(runes[:RunAskPreviewRunes-1]), " ") + "…"
}
