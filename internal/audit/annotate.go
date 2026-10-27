package audit

import (
	"context"
	"sync"
)

// The request middleware (internal/server withAuditTrail) records every
// successful write as a row naming its method and path, which is all it can
// see. A handler that knows more about its own write says so with Annotate,
// and the row, and so its log line, carries it: the account a membership
// change was for, say, and the project, for a route that names its project
// itself rather than through the per-request project.

// Annotation is what a handler adds to its write's row. A field left empty
// leaves the middleware's own value.
type Annotation struct {
	// Project is the root of the project the write changed.
	Project string
	// Target is what the write changed, such as an account's ID.
	Target string
	// Detail is stored like any event's detail: sanitised, credentials dropped.
	Detail map[string]any
}

type annotationKey struct{}

// annotations is one write's slot. It travels as a pointer because the
// handler runs on a context the middleware never sees again.
type annotations struct {
	mu   sync.Mutex
	note Annotation
}

// Annotatable returns a context that Annotate writes to, and the function that
// reads back what was written once the handler has returned.
func Annotatable(ctx context.Context) (context.Context, func() Annotation) {
	slot := &annotations{}
	return context.WithValue(ctx, annotationKey{}, slot), func() Annotation {
		slot.mu.Lock()
		defer slot.mu.Unlock()
		return slot.note
	}
}

// Annotate adds to the row of the write ctx serves: each field it sets
// replaces what an earlier call said. Outside Annotatable, on a read or a
// route the middleware does not record, it does nothing.
func Annotate(ctx context.Context, note Annotation) {
	if ctx == nil {
		return
	}
	slot, _ := ctx.Value(annotationKey{}).(*annotations)
	if slot == nil {
		return
	}
	slot.mu.Lock()
	defer slot.mu.Unlock()
	if note.Project != "" {
		slot.note.Project = note.Project
	}
	if note.Target != "" {
		slot.note.Target = note.Target
	}
	if note.Detail != nil {
		slot.note.Detail = note.Detail
	}
}
