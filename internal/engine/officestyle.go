package engine

// Office styles: how a project's office looks in Station's war room. The
// style is the project's (graph.yaml office_style), shared with everyone in
// it like the node type list, and only people may change it. It changes the
// floor, walls and furniture; the agents and their state icons look the same
// in every style, so a state reads the same everywhere.

const (
	OfficeStyleDefault     = "default"
	OfficeStyleEngineering = "engineering"
	OfficeStyleStudy       = "study"
	OfficeStyleStudio      = "studio"
	OfficeStyleCafe        = "cafe"
)

// OfficeStyles lists every style in display order. testdata/office_styles.json
// pins it for both the hub and the web client.
var OfficeStyles = []string{
	OfficeStyleDefault, OfficeStyleEngineering, OfficeStyleStudy, OfficeStyleStudio, OfficeStyleCafe,
}

// ValidOfficeStyle reports whether s may be stored: "" (unset) or one of
// OfficeStyles, spelled exactly.
func ValidOfficeStyle(s string) bool {
	if s == "" {
		return true
	}
	for _, style := range OfficeStyles {
		if s == style {
			return true
		}
	}
	return false
}

// DefaultOfficeStyle is the style of a project that has not chosen one: a
// story is written in a study, anything else is engineering.
func DefaultOfficeStyle(layout string) string {
	if layout == "story" {
		return OfficeStyleStudy
	}
	return OfficeStyleEngineering
}

// ResolvedOfficeStyle is the style Station draws: the stored one when it is
// set and valid, otherwise the default for the graph's layout.
func (g *Graph) ResolvedOfficeStyle() string {
	if g == nil {
		return DefaultOfficeStyle("")
	}
	if g.OfficeStyle != "" && ValidOfficeStyle(g.OfficeStyle) {
		return g.OfficeStyle
	}
	return DefaultOfficeStyle(g.Type)
}
