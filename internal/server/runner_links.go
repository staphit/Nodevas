package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"path/filepath"
	"time"

	"nodevas/internal/audit"
	"nodevas/internal/auth"
	runnersapi "nodevas/internal/httpapi/runners"
	"nodevas/internal/identity"
	"nodevas/internal/runners"
	"nodevas/internal/secrets"
)

// useRunnerLinks sets up device links (internal/runners/links.go) beside the
// registry. The runner access token is kept like the copilot's key: in the
// catalog's secrets directory, under the machine key, named per workspace.
func (s *Server) useRunnerLinks() {
	s.links = runners.NewLinks()
	if s.pm == nil {
		return
	}
	sum := sha256.Sum256([]byte(filepath.Clean(s.pm.Workspace())))
	dir := filepath.Join(s.pm.CatalogRoot(), "secrets")
	s.runnerAccess = runners.NewAccessTokens(secrets.New(
		filepath.Join(dir, "runner-access-"+hex.EncodeToString(sum[:])[:16]+".enc"),
		filepath.Join(dir, "master.key")))
}

// runnerLinkOptions wires device links once the authenticator and mailer are
// settled, which is when Handler runs.
func (s *Server) runnerLinkOptions() runnersapi.LinkOptions {
	opts := runnersapi.LinkOptions{
		Links:  s.links,
		Access: s.runnerAccess,
		Audit:  s.auditRunnerLink,
	}
	if accounts, ok := s.auth.(*auth.SessionAuth); ok {
		opts.StepUpRequired = true
		opts.SteppedUpAt = accounts.SteppedUpAt
	}
	if users := s.users; users != nil {
		opts.Owner = func(ctx context.Context, id string) (runnersapi.OwnerAccount, bool) {
			actor, revision, ok := users.ActorRevision(ctx, id)
			if !ok {
				return runnersapi.OwnerAccount{}, false
			}
			account := runnersapi.OwnerAccount{Actor: actor, Revision: revision}
			for _, record := range users.Records(ctx) {
				if record.ID == id {
					account.Email = record.Email
					break
				}
			}
			return account, true
		}
	}
	if s.mailer != nil {
		opts.Mail = s.mailer.Send
	}
	return opts
}

func (s *Server) auditRunnerLink(r *http.Request, action string, actor identity.Actor, target string, detail map[string]any) {
	if s.audit == nil {
		return
	}
	// A device link signs a machine in as its owner, so its log line names
	// the owner's address like any other sign-in; links are rare, and so is
	// the one read.
	email := ""
	if accounts, ok := s.auth.(*auth.SessionAuth); ok && actor.ID != "" {
		email = accounts.AccountEmail(r.Context(), actor.ID)
	}
	s.audit.RecordOrLog(r.Context(), audit.Event{
		At: time.Now(), ActorID: actor.ID, ActorName: actor.Name,
		Action: action, Target: target, ClientIP: auth.ClientIP(r), Detail: detail,
		ActorEmail: email,
	})
}
