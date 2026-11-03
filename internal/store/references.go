// Reference notes: the short summaries of what a node uses as context.

package store

import (
	"errors"
	"os"
	"strings"
	"unicode/utf8"

	"nodevas/internal/engine"
)

// Caps on the reference block a node's context and a run's prompt carry. They
// keep the block near 12 KB of UTF-8 at most, well inside a prompt's budget.
const (
	MaxReferenceNotes        = 8
	MaxReferenceSummaryRunes = 400
	MaxReferenceTotalRunes   = 4000
	// maxReferenceTitleRunes cuts a title (which may be up to 64 KiB) so eight
	// of them cannot crowd a run prompt's instructions out.
	maxReferenceTitleRunes = 200
)

// ReferenceSummary is one note a node uses as context, cut to a summary.
type ReferenceSummary struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Kind      string `json:"kind"`
	TypeLabel string `json:"typeLabel"`
	Summary   string `json:"summary"`
	// Truncated says the summary was cut; get_node on ID reads it whole.
	Truncated bool `json:"truncated"`
}

// ReferenceSummaries summarizes, in order, the nodes nodeID references. A
// reference whose target is gone is skipped. The bool reports that the list
// was cut, by the note cap or the total budget. A node that is not in g has
// no references.
func (s *Store) ReferenceSummaries(g *engine.Graph, nodeID string) ([]ReferenceSummary, bool, error) {
	if g == nil {
		return nil, false, nil
	}
	node := g.NodeByID(nodeID)
	if node == nil || len(node.Refs) == 0 {
		return nil, false, nil
	}
	var out []ReferenceSummary
	total := 0
	for _, ref := range node.Refs {
		target := g.NodeByID(ref)
		if target == nil {
			continue
		}
		if len(out) == MaxReferenceNotes {
			return out, true, nil
		}
		summary, cut, err := s.referenceSummary(target.ID)
		if err != nil {
			return nil, false, err
		}
		runes := utf8.RuneCountInString(summary)
		if total+runes > MaxReferenceTotalRunes {
			return out, true, nil
		}
		total += runes
		out = append(out, ReferenceSummary{
			ID:        target.ID,
			Title:     referenceTitle(target.Title),
			Kind:      target.Kind,
			TypeLabel: g.TypeOf(target).Label,
			Summary:   summary,
			Truncated: cut,
		})
	}
	return out, false, nil
}

// referenceSummary is a note's frontmatter "summary" when it has one, or else
// the start of its body, with whitespace collapsed and cut to
// MaxReferenceSummaryRunes. A note without a document has an empty summary;
// an unparseable one is summarized from its raw text.
func (s *Store) referenceSummary(id string) (string, bool, error) {
	data, err := s.ReadFile(s.NodePath(id))
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	text := string(data)
	if nf, parseErr := engine.ParseNodeFile(data); parseErr == nil {
		text = nf.Body
		if summary, ok := nf.Meta["summary"].(string); ok && strings.TrimSpace(summary) != "" {
			text = summary
		}
	}
	summary, cut := cutRunes(strings.Join(strings.Fields(text), " "), MaxReferenceSummaryRunes)
	return summary, cut, nil
}

// cutRunes shortens text to at most limit runes, marking a cut with "…"
// (which counts toward the limit).
func cutRunes(text string, limit int) (string, bool) {
	if utf8.RuneCountInString(text) <= limit {
		return text, false
	}
	runes := []rune(text)
	return strings.TrimSpace(string(runes[:limit-1])) + "…", true
}

// referenceTitle is a title on one line, cut to maxReferenceTitleRunes.
func referenceTitle(title string) string {
	cut, _ := cutRunes(strings.Join(strings.Fields(title), " "), maxReferenceTitleRunes)
	return cut
}
