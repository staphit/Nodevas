package engine

import (
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"nodevas/internal/locale"
)

// Acceptance criteria on dispatch and Autopilot nodes. They live in
// graph.yaml (not the node file) so one locked write path checks the role,
// the type and the lock.

const (
	MaxAcceptanceItems     = 30
	MaxAcceptanceItemRunes = 400
)

type Acceptance struct {
	Items  []AcceptanceItem `yaml:"items" json:"items"`
	Locked bool             `yaml:"locked,omitempty" json:"locked,omitempty"`
	By     string           `yaml:"by,omitempty" json:"by,omitempty"`
}

type AcceptanceItem struct {
	ID   string `yaml:"id" json:"id"`
	Text string `yaml:"text" json:"text"`
}

var acceptanceIDPattern = regexp.MustCompile(`^a[0-9]{1,3}$`)

// AcceptanceAllowed reports whether a node's type may carry acceptance.
func (g *Graph) AcceptanceAllowed(n *Node) bool {
	switch g.TypeOf(n).AgentMode {
	case AgentDispatch, AgentAutopilot:
		return true
	}
	return false
}

// Empty reports whether there are no criteria.
func (a *Acceptance) Empty() bool { return a == nil || len(a.Items) == 0 }

// ValidateNodeAcceptance checks one node's acceptance list.
func ValidateNodeAcceptance(g *Graph, n *Node) error {
	if n == nil || n.Acceptance == nil {
		return nil
	}
	if !g.AcceptanceAllowed(n) {
		return locale.Errorf("node %q: type %q cannot carry acceptance criteria", "節點「%s」：類型「%s」不能有驗收條件", n.ID, g.TypeOf(n).ID)
	}
	return ValidateAcceptanceItems(n.Acceptance.Items)
}

// ValidateAcceptanceItems checks the count, ids and texts of a list.
func ValidateAcceptanceItems(items []AcceptanceItem) error {
	if len(items) > MaxAcceptanceItems {
		return locale.Errorf("acceptance: at most %d criteria", "驗收條件：最多 %d 條", MaxAcceptanceItems)
	}
	seen := map[string]bool{}
	for _, item := range items {
		if !acceptanceIDPattern.MatchString(item.ID) {
			return locale.Errorf("acceptance: invalid criterion id %q (a1…a999)", "驗收條件：條件代號「%s」無效（a1…a999）", item.ID)
		}
		if seen[item.ID] {
			return locale.Errorf("acceptance: duplicate criterion id %q", "驗收條件：重複的條件代號「%s」", item.ID)
		}
		seen[item.ID] = true
		text := strings.TrimSpace(item.Text)
		if text == "" {
			return locale.Errorf("acceptance: criterion %s is empty", "驗收條件：條件 %s 是空的", item.ID)
		}
		if strings.ContainsAny(item.Text, "\r\n") {
			return locale.Errorf("acceptance: criterion %s must be one line", "驗收條件：條件 %s 必須是單行", item.ID)
		}
		if utf8.RuneCountInString(item.Text) > MaxAcceptanceItemRunes {
			return locale.Errorf("acceptance: criterion %s is longer than %d characters", "驗收條件：條件 %s 超過 %d 個字元", item.ID, MaxAcceptanceItemRunes)
		}
	}
	return nil
}

// AssignAcceptanceIDs gives every item without an id the next free aN id,
// keeping existing ids.
func AssignAcceptanceIDs(items []AcceptanceItem) []AcceptanceItem {
	if items == nil {
		return nil
	}
	maxID := 0
	for _, item := range items {
		if acceptanceIDPattern.MatchString(item.ID) {
			if n, err := strconv.Atoi(item.ID[1:]); err == nil && n > maxID {
				maxID = n
			}
		}
	}
	out := make([]AcceptanceItem, len(items))
	for i, item := range items {
		if item.ID == "" {
			maxID++
			item.ID = "a" + strconv.Itoa(maxID)
		}
		out[i] = item
	}
	return out
}

// EqualItems reports whether two acceptance values hold the same criteria
// (ids and texts, in order).
func (a *Acceptance) EqualItems(b *Acceptance) bool {
	var ai, bi []AcceptanceItem
	if a != nil {
		ai = a.Items
	}
	if b != nil {
		bi = b.Items
	}
	if len(ai) != len(bi) {
		return false
	}
	for i := range ai {
		if ai[i] != bi[i] {
			return false
		}
	}
	return true
}
