package store

import (
	"testing"

	"nodevas/internal/engine"
)

// A stored colour lands in a CSS custom property on every reader's board, so
// it must not be able to reach url() or image-set() and fetch a remote image.
func TestGraphColorsCannotFetch(t *testing.T) {
	graph := func(style engine.NodeStyle, decoration string) *engine.Graph {
		return &engine.Graph{
			Nodes: []*engine.Node{{ID: "a", Title: "A"}},
			UI: &engine.UIState{
				NodeStyles: map[string]engine.NodeStyle{"a": style},
				Groups:     []engine.CanvasGroup{{ID: "g", X: 1, Y: 1, Width: 100, Height: 100, Color: decoration}},
			},
		}
	}
	for _, ok := range []string{"", "#31566a", "#fff", "rebeccapurple", "rgb(1, 2, 3)", "hsl(200 50% 40%)"} {
		if err := ValidateGraphForStorage(graph(engine.NodeStyle{Color: ok, TextColor: ok, BorderColor: ok}, ok)); err != nil {
			t.Errorf("%q refused: %v", ok, err)
		}
	}
	for _, bad := range []string{"url(https://a.co/x)", "url(//a.co/x)", `image-set("x")`, `\75rl(x)`, "red;x:y"} {
		for name, g := range map[string]*engine.Graph{
			"color":      graph(engine.NodeStyle{Color: bad}, ""),
			"text":       graph(engine.NodeStyle{TextColor: bad}, ""),
			"border":     graph(engine.NodeStyle{BorderColor: bad}, ""),
			"decoration": graph(engine.NodeStyle{}, bad),
		} {
			if err := ValidateGraphForStorage(g); err == nil {
				t.Errorf("%s %q accepted", name, bad)
			}
		}
	}
}
