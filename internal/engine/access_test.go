package engine

import (
	"strings"
	"testing"
)

func TestValidateRefusesAnUnknownWriteAccess(t *testing.T) {
	g := &Graph{Version: 1, Nodes: []*Node{{ID: "a", WriteAccess: "robots"}}}
	var found bool
	for _, issue := range Validate(g) {
		if issue.Severity == "error" && issue.Field == "write_access" &&
			strings.Contains(issue.Msg, `invalid write_access "robots"`) {
			found = true
		}
	}
	if !found {
		t.Fatalf("issues = %+v, want an invalid write_access error", Validate(g))
	}
	for _, access := range []string{
		WriteAccessAll, WriteAccessWorker, WriteAccessOrchestrator, WriteAccessHumanOnly,
	} {
		g := &Graph{Version: 1, Nodes: []*Node{{ID: "a", WriteAccess: access}}}
		for _, issue := range Validate(g) {
			if issue.Field == "write_access" {
				t.Errorf("write_access %q flagged: %+v", access, issue)
			}
		}
	}
}

func TestSyncFrontmatterCarriesWriteAccess(t *testing.T) {
	nf := &NodeFile{Meta: map[string]any{}, Body: "text\n"}
	n := &Node{ID: "a", WriteAccess: WriteAccessHumanOnly}
	SyncFrontmatter(nf, n)
	if nf.Meta["write_access"] != WriteAccessHumanOnly {
		t.Fatalf("write_access = %v, want %q", nf.Meta["write_access"], WriteAccessHumanOnly)
	}
	// Back to unrestricted: the key leaves the frontmatter instead of
	// lingering as an empty string.
	n.WriteAccess = WriteAccessAll
	SyncFrontmatter(nf, n)
	if _, exists := nf.Meta["write_access"]; exists {
		t.Fatalf("write_access still present after clearing: %v", nf.Meta)
	}
}
