package runs

import (
	"strings"
	"testing"

	"nodevas/internal/runnerwire"
)

func TestBuildPromptCarriesReferenceNotes(t *testing.T) {
	refs := ReferencesBlock([]Reference{
		{ID: "note-01", Title: "角色卡：林曉", TypeLabel: "Note", Summary: "林曉怕水。"},
		{ID: "note-02", Title: "Setting", Summary: "A harbour town."},
	}, true)
	prompt := BuildPrompt("第三場", "Write the scene.", refs, "Keep it short.")
	for _, want := range []string{
		"# 第三場\n\nWrite the scene.\n\n## Reference notes\nRead these before writing;",
		"### 角色卡：林曉 (note-01, Note)\n林曉怕水。",
		"### Setting (note-02, Note)\nA harbour town.",
		"(More references exist; get_node <node id> lists them.)\n\nAdditional instructions:\nKeep it short.",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt lacks %q:\n%s", want, prompt)
		}
	}
	if ReferencesBlock(nil, false) != "" {
		t.Fatal("a node without references got a block")
	}
}

func TestHugeContentGivesWayToReferencesAndInstructions(t *testing.T) {
	var notes []Reference
	for i := 0; i < 8; i++ {
		notes = append(notes, Reference{ID: "n", Title: "角色", Summary: strings.Repeat("字", 500)})
	}
	refs := ReferencesBlock(notes, true)
	prompt := BuildPrompt("t", strings.Repeat("x", runnerwire.MaxPromptBytes*2), refs, "extra instructions")
	if len(prompt) > runnerwire.MaxPromptBytes {
		t.Fatalf("prompt is %d bytes", len(prompt))
	}
	if !strings.Contains(prompt, truncatedNote) || !strings.Contains(prompt, refs) ||
		!strings.HasSuffix(prompt, "Additional instructions:\nextra instructions") {
		t.Fatal("content did not give way first")
	}
}
