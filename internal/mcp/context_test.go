package mcp

import (
	"context"
	"errors"
	"net/url"
	"os"
	"strings"
	"testing"

	"nodevas/internal/engine"
	"nodevas/internal/identity"
)

func TestCompactReadsStayScopedAndObserveExternalChanges(t *testing.T) {
	endpoint, pm := liveServer(t)
	g, rev, err := pm.Store().LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	g.UI = &engine.UIState{Positions: map[string]engine.Position{"design": {X: 10, Y: 20}}}
	if _, err := pm.Store().SaveGraph(identity.Local, g, rev); err != nil {
		t.Fatal(err)
	}
	client, err := NewClient(ClientOptions{Server: endpoint})
	if err != nil {
		t.Fatal(err)
	}
	projects, err := client.Projects(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	client.project = projects[0].Name
	ctx := context.Background()
	full, err := client.Graph(ctx)
	if err != nil || full.Graph.UI == nil {
		t.Fatal("regular graph lost UI state")
	}
	outline, err := client.graph(ctx, url.Values{"view": {"outline"}})
	if err != nil {
		t.Fatal(err)
	}
	if outline.Graph.UI != nil || len(outline.Graph.Nodes) != len(full.Graph.Nodes) || len(outline.Graph.Edges) != len(full.Graph.Edges) {
		t.Fatal("compact graph lost topology or carried UI state")
	}
	sources, err := client.DocumentSources(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var before string
	for _, source := range sources {
		if source.ID == "design" {
			before = source.Rev
		}
	}
	if before == "" {
		t.Fatal("manifest omitted revision")
	}
	body, _, err := pm.Store().LoadNodeContent("design")
	if err != nil {
		t.Fatal(err)
	}
	body += "\nExternal 文🙂 edit.\n"
	if err := os.WriteFile(pm.Store().NodePath("design"), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	sources, err = client.DocumentSources(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range sources {
		if source.ID == "design" && (source.Rev == before || source.Bytes != len(body)) {
			t.Fatal("manifest cached an external edit")
		}
	}
	node, err := client.NodeContext(ctx, "design")
	if err != nil {
		t.Fatal(err)
	}
	if node.Content != body || node.Rev == before || len(node.Downstream) != 1 || node.Downstream[0] != "build" {
		t.Fatal("compact context lost live content or dependencies")
	}
	// An orphan Markdown file is not a live node and must not be retrievable as context.
	if err := os.WriteFile(pm.Store().NodePath("orphan"), []byte("orphan body"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err = client.NodeContext(ctx, "orphan")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != CodeNotFound {
		t.Fatalf("orphan context: %v", err)
	}
	client.project = "missing-project"
	if _, err := client.DocumentSources(ctx); err == nil {
		t.Fatal("manifest silently switched project")
	}
	if _, err := client.NodeContext(ctx, "design"); err == nil {
		t.Fatal("context silently switched project")
	}
	client.project = projects[0].Name
	if !strings.Contains(node.Content, "External 文🙂") {
		t.Fatal("Unicode content changed")
	}
}
