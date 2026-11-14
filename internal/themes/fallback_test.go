package themes

import (
	"encoding/json"
	"testing"
)

// A pack's one fallback character stands in for agents no character matches,
// and a pack needs none at all: it can replace just one character.
func TestAFallbackCharacterIsOptionalAndKept(t *testing.T) {
	doc := manifestDoc()
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := parseManifest(data)
	if err != nil {
		t.Fatalf("a pack with no fallback is refused: %v", err)
	}
	if manifest.Characters["claude"].Fallback {
		t.Fatal("fallback defaults to false")
	}

	doc["characters"].(map[string]any)["claude"].(map[string]any)["fallback"] = true
	if data, err = json.Marshal(doc); err != nil {
		t.Fatal(err)
	}
	if manifest, err = parseManifest(data); err != nil {
		t.Fatalf("a pack with one fallback is refused: %v", err)
	}
	if !manifest.Characters["claude"].Fallback {
		t.Fatal("fallback was lost")
	}
}
