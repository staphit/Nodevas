package httpx

import (
	"context"
	"net/http"
	"nodevas/internal/project"
	"nodevas/internal/store"
	"strings"
)

// ProjectHeader names the target project for clients that cannot add a query
// string to the request.
const ProjectHeader = "X-Nodevas-Project"

type projectStoreKey struct{}

// projectScopedPathExempt lists the API prefixes that manage projects
// themselves. They take their target from the body or from their own
// ?project= parameter (which may name a folder with no graph.yaml), so the
// per-request project must not be resolved for them.
var projectScopedPathExempt = []string{
	"/api/projects",
	"/api/workspaces",
	"/api/fs/",
	"/api/search",
	// /api/remote/push takes its target through ExportScope's own ?project=
	// semantics (which may name a folder with no graph.yaml).
	"/api/remote",
}

// requestProjectName reads the per-request project override. Empty means the
// active project, which is what every existing client sends.
func RequestProjectName(r *http.Request) string {
	name := strings.TrimSpace(r.URL.Query().Get("project"))
	if name == "" {
		name = strings.TrimSpace(r.Header.Get(ProjectHeader))
	}
	return strings.Trim(name, "/")
}

func ProjectScopedPath(path string) bool {
	if !strings.HasPrefix(path, "/api/") {
		return false
	}
	for _, prefix := range projectScopedPathExempt {
		if strings.HasPrefix(path, prefix) {
			return false
		}
	}
	return true
}

// WithStore returns a context carrying the store this request targets.
func WithStore(ctx context.Context, st *store.Store) context.Context {
	return context.WithValue(ctx, projectStoreKey{}, st)
}

// StoreFor returns the store a request targets: the one the middleware
// resolved, or the active project's.
func StoreFor(r *http.Request, pm *project.ProjectManager) *store.Store {
	if r != nil {
		if st, ok := r.Context().Value(projectStoreKey{}).(*store.Store); ok && st != nil {
			return st
		}
	}
	if pm == nil {
		return nil
	}
	return pm.Store()
}
