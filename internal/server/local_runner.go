package server

import (
	"context"
	"errors"
	"time"

	"nodevas/internal/audit"
	"nodevas/internal/auth"
	"nodevas/internal/identity"
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
// server's runners are linked from the Runners page by their owners.
func (s *Server) LocalRunnerToken(ctx context.Context, existing, name string) (token string, created bool, err error) {
	if s.runners == nil {
		return "", false, errors.New("runners are not enabled on this server")
	}
	if _, local := s.auth.(auth.LocalOnly); !local {
		return "", false, errors.New("a server with accounts has no local owner for a runner; link one from the Runners page")
	}
	if existing != "" {
		runner, err := s.runners.Authenticate(ctx, existing)
		switch {
		case err == nil && runner.OwnerID == identity.Local.ID:
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
	if s.audit != nil {
		s.audit.RecordOrLog(ctx, audit.Event{
			At: time.Now(), ActorID: identity.Local.ID, ActorName: identity.Local.Name,
			Action: "runner.create", Target: runner.ID,
			Detail: map[string]any{"name": runner.Name, "via": "serve local runner"},
		})
	}
	return token, true, nil
}
