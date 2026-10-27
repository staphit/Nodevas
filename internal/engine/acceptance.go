package engine

// Acceptance criteria on dispatch and Autopilot nodes.
//
// RED PHASE STUB: declarations only, no behavior.

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

// AcceptanceAllowed reports whether a node's type may carry acceptance.
func (g *Graph) AcceptanceAllowed(n *Node) bool { return false }

// ValidateNodeAcceptance checks one node's acceptance list.
func ValidateNodeAcceptance(g *Graph, n *Node) error { return nil }

// AssignAcceptanceIDs gives every item without an id the next free aN id,
// keeping existing ids.
func AssignAcceptanceIDs(items []AcceptanceItem) []AcceptanceItem { return items }
