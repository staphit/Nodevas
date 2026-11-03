package engine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// officeStyleFixture is testdata/office_styles.json, which the web client's
// tests read too, so the two sides cannot drift.
type officeStyleFixture struct {
	Styles   []string          `json:"styles"`
	Defaults map[string]string `json:"defaults"`
	Invalid  []string          `json:"invalid"`
}

func loadOfficeStyleFixture(t *testing.T) officeStyleFixture {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "office_styles.json"))
	if err != nil {
		t.Fatal(err)
	}
	var f officeStyleFixture
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestOfficeStylesMatchTheFixture(t *testing.T) {
	f := loadOfficeStyleFixture(t)
	if strings.Join(f.Styles, ",") != strings.Join(OfficeStyles, ",") {
		t.Fatalf("styles = %v, fixture %v", OfficeStyles, f.Styles)
	}
	for _, style := range f.Styles {
		if !ValidOfficeStyle(style) {
			t.Errorf("%q should be valid", style)
		}
		g := &Graph{Type: "story", OfficeStyle: style}
		if got := g.ResolvedOfficeStyle(); got != style {
			t.Errorf("resolved %q = %q", style, got)
		}
	}
	for layout, want := range f.Defaults {
		if got := DefaultOfficeStyle(layout); got != want {
			t.Errorf("default for %q = %q, want %q", layout, got, want)
		}
		if got := (&Graph{Type: layout}).ResolvedOfficeStyle(); got != want {
			t.Errorf("unset style on %q resolves to %q, want %q", layout, got, want)
		}
	}
	for _, bad := range f.Invalid {
		// "" is a valid stored value: it means unset.
		if bad == "" {
			if !ValidOfficeStyle(bad) {
				t.Error(`"" should be storable (unset)`)
			}
			continue
		}
		if ValidOfficeStyle(bad) {
			t.Errorf("%q should be invalid", bad)
		}
		g := &Graph{Type: "story", OfficeStyle: bad}
		if got := g.ResolvedOfficeStyle(); got != OfficeStyleStudy {
			t.Errorf("invalid %q resolves to %q, want the layout default", bad, got)
		}
	}
	if got := (*Graph)(nil).ResolvedOfficeStyle(); got != OfficeStyleEngineering {
		t.Errorf("nil graph resolves to %q", got)
	}
}

func TestOfficeStyleRoundTripsThroughYAML(t *testing.T) {
	g := &Graph{Version: 1, Type: "story", OfficeStyle: OfficeStyleCafe}
	data, err := MarshalGraph(g)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "office_style: cafe") {
		t.Fatalf("yaml = %s", data)
	}
	back, err := ParseGraph(data)
	if err != nil {
		t.Fatal(err)
	}
	if back.OfficeStyle != OfficeStyleCafe {
		t.Fatalf("round trip = %q", back.OfficeStyle)
	}
	unset, err := MarshalGraph(&Graph{Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(unset), "office_style") {
		t.Fatalf("unset style was written: %s", unset)
	}
}
