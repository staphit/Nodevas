package audit

import (
	"context"
	"testing"
)

func TestAnnotateFillsTheSlotItIsGivenAndNothingElse(t *testing.T) {
	// Outside Annotatable there is nowhere to write, and nothing breaks.
	Annotate(context.Background(), Annotation{Target: "u-1"})

	ctx, annotated := Annotatable(context.Background())
	if got := annotated(); got.Project != "" || got.Target != "" || got.Detail != nil {
		t.Fatalf("an untouched slot = %+v", got)
	}
	// A handler annotates the context it was handed, or one derived from it,
	// and a later call adds to what an earlier one said.
	type derivedKey struct{}
	Annotate(ctx, Annotation{Project: "/workspace/alpha"})
	Annotate(context.WithValue(ctx, derivedKey{}, "derived"),
		Annotation{Target: "u-1", Detail: map[string]any{"role": "viewer"}})
	got := annotated()
	if got.Project != "/workspace/alpha" || got.Target != "u-1" || got.Detail["role"] != "viewer" || len(got.Detail) != 1 {
		t.Fatalf("annotation = %+v, want the project, then the member and the role", got)
	}
}
