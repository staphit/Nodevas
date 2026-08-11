package mcp

import (
	"context"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestTheBoardCanBeAttachedAsAResource(t *testing.T) {
	url, _ := liveServer(t)
	session := mcpSession(t, url, "")

	result, err := session.ReadResource(context.Background(), &sdk.ReadResourceParams{
		URI: "nodevas://project/active/outline",
	})
	if err != nil {
		t.Fatalf("read the outline: %v", err)
	}
	if len(result.Contents) != 1 {
		t.Fatalf("contents = %d, want one", len(result.Contents))
	}
	text := result.Contents[0].Text
	if !strings.Contains(text, `"design"`) || !strings.Contains(text, `"build"`) {
		t.Fatalf("outline = %s, want both nodes", text)
	}
	if strings.Contains(text, "positions") {
		t.Fatalf("the outline resource carried layout: %s", text)
	}
}

func TestANodeCanBeReadThroughItsURI(t *testing.T) {
	url, _ := liveServer(t)
	session := mcpSession(t, url, "")

	result, err := session.ReadResource(context.Background(), &sdk.ReadResourceParams{
		URI: "nodevas://project/active/node/design",
	})
	if err != nil {
		t.Fatalf("read the node: %v", err)
	}
	if !strings.Contains(result.Contents[0].Text, "Work out the shape") {
		t.Fatalf("node = %s, want its markdown", result.Contents[0].Text)
	}
}

// A URI that names another project and quietly serves this one's data is worse
// than a URI that fails: it looks like it addressed something it did not.
func TestAURINamingAnotherProjectIsRefused(t *testing.T) {
	url, _ := liveServer(t)
	session := mcpSession(t, url, "")

	_, err := session.ReadResource(context.Background(), &sdk.ReadResourceParams{
		URI: "nodevas://project/somewhere-else/node/design",
	})
	if err == nil {
		t.Fatal("a URI for another project was served from this one")
	}
}

// The refusal above comes from the SDK's template matching, which only accepts
// the one project segment the templates were registered with. The handler
// checks it too: the two are independent, and this one is what holds if the
// templates are ever widened.
func TestTheHandlerAlsoChecksTheProjectInAURI(t *testing.T) {
	if _, err := nodeIDFromURI("nodevas://project/other/node/design", "active"); err == nil {
		t.Fatal("the handler accepted a URI for another project")
	} else if !strings.Contains(err.Error(), "another one") {
		t.Fatalf("error = %v, want it to say the project does not match", err)
	}

	id, err := nodeIDFromURI("nodevas://project/active/node/design", "active")
	if err != nil || id != "design" {
		t.Fatalf("id = %q, err = %v, want design", id, err)
	}
	if _, err := nodeIDFromURI("nodevas://project/active/node/", "active"); err == nil {
		t.Fatal("an empty node id was accepted")
	}
}

func TestThePromptSaysWhatToDoWithAnEmptyQueue(t *testing.T) {
	url, _ := liveServer(t)
	session := mcpSession(t, url, "")

	result, err := session.GetPrompt(context.Background(), &sdk.GetPromptParams{
		Name:      "work_the_queue",
		Arguments: map[string]string{"assignee": "claude"},
	})
	if err != nil {
		t.Fatalf("get the prompt: %v", err)
	}
	text := result.Messages[0].Content.(*sdk.TextContent).Text
	// The stopping condition is what the per-tool descriptions cannot carry,
	// and where an unattended agent goes wrong.
	if !strings.Contains(text, "people are the blockers") {
		t.Fatalf("prompt = %q, want it to name the two ways a queue empties", text)
	}
	if !strings.Contains(text, `assigned to "claude"`) {
		t.Fatalf("prompt = %q, want the assignee filter applied", text)
	}
}
