package server

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"time"

	"nodevas/internal/audit"
	"nodevas/internal/auth"
	"nodevas/internal/finalpath"
	"nodevas/internal/identity"
	"nodevas/internal/locale"
	"nodevas/internal/runners"
)

// LocalRunnerToken is the runner token for the runner `serve` starts beside a
// standalone hub (by default, or with --with-runner/--repo). existing is
// kept when it still opens a live runner of this server's one local actor;
// otherwise a new runner is registered for that actor, exactly as the Runners
// page registers one on loopback, and its token returned. created says which.
//
// It refuses a server with accounts: there the local actor is nobody, and a
// runner owned by it would be a credential no person answers for. Such a
// server's runners are linked under Settings → Agents by their owners.
//
// The runner it returns, kept or new, is this startup's built-in runner
// (single-user edition): the only one the hub's welcome may call built in,
// and then only on a socket straight from this machine (httpapi/runners).
// Any other runner of the local actor stays a classic runner.
func (s *Server) LocalRunnerToken(ctx context.Context, existing, name string) (token string, created bool, err error) {
	if s.runners == nil {
		return "", false, locale.Errorf("runners are not enabled on this server", "這個伺服器沒有啟用 runner")
	}
	if _, local := s.auth.(auth.LocalOnly); !local {
		return "", false, locale.Errorf("a server with accounts has no local owner for a runner; link one under Settings → Agents → Link a new device", "有帳號的伺服器沒有 runner 的本機擁有者；請在「設定 → Agent 與 Runner → 連結新裝置」連結一個")
	}
	if existing != "" {
		runner, err := s.runners.Authenticate(ctx, existing)
		switch {
		case err == nil && runner.OwnerID == identity.Local.ID:
			s.runners.SetBuiltIn(runner.ID)
			return existing, false, nil
		case err != nil && !errors.Is(err, runners.ErrUnauthenticated):
			return "", false, err
		}
		// Unknown, revoked, or someone else's: register a new runner below.
	}
	runner, token, err := s.runners.Create(ctx, identity.Local, name)
	if err != nil {
		return "", false, err
	}
	s.runners.SetBuiltIn(runner.ID)
	if s.audit != nil {
		s.audit.RecordOrLog(ctx, audit.Event{
			At: time.Now(), ActorID: identity.Local.ID, ActorName: identity.Local.Name,
			Action: "runner.create", Target: runner.ID,
			Detail: map[string]any{"name": runner.Name, "via": "serve local runner"},
		})
	}
	return token, true, nil
}

// singleUser reports whether this is the single-user edition: no accounts,
// so the person at the machine is the only user (runnerwire/edition.go).
func (s *Server) singleUser() bool {
	_, local := s.auth.(auth.LocalOnly)
	return local
}

// hubDataFolder reports whether path is this hub's own data, its workspace
// or its catalog root, a folder inside one or a folder holding one, which is
// never added as a repo: an agent working there would work on Nodevas
// itself, and making a folder that holds the catalog root a Git project
// would commit its secrets (the master key, runner access tokens) into the
// first version. Each folder is compared as the system finally opens it
// (finalpath: links, and on Windows junctions and prefixed paths, resolved)
// where it exists, and the comparison ignores letter case on Windows.
func (s *Server) hubDataFolder(path string) bool {
	if s.pm == nil {
		return false
	}
	target := resolvedFolder(path)
	for _, root := range []string{s.pm.Workspace(), s.pm.CatalogRoot()} {
		if root == "" {
			continue
		}
		root = resolvedFolder(root)
		if insideFolder(target, root) || insideFolder(root, target) {
			return true
		}
	}
	return false
}

// resolvedFolder is path made absolute and clean, resolved as the system
// finally opens it when it exists.
func resolvedFolder(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	if real, err := finalpath.Resolve(path); err == nil {
		path = real
	}
	return filepath.Clean(path)
}

// insideFolder reports whether path is root or below it. filepath.Rel
// ignores letter case on Windows, as the file system does.
func insideFolder(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}
