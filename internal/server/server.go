// Package server exposes the engine over HTTP for the web UI and agents.
package server

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"nodevas/internal/audit"
	"nodevas/internal/auth"
	"nodevas/internal/httpapi/fsbrowse"
	"nodevas/internal/httpapi/httpx"
	"nodevas/internal/httpapi/system"
	"nodevas/internal/notify"
	"nodevas/internal/project"
	"nodevas/internal/realtime"
	"nodevas/internal/remote"
	"nodevas/internal/store"
	"strconv"
	"sync"
)

type Server struct {
	pm       *project.ProjectManager
	hub      *realtime.Hub
	webFS    fs.FS // built frontend (may be nil in dev)
	notifier *notify.Notifier
	remotes  *remote.RemoteManager
	openDir  func(string) error
	auth     auth.Authenticator
	mailer   system.PasscodeMailer
	audit    *audit.Store
	abuse    *abuseGuard
	stop     chan struct{}
	stopOnce sync.Once
	// allowedHosts are the Host header names this server answers on, lowercased
	// and without a port. A nil map means nobody has said where this server
	// listens, so there is no name to judge a Host against: that is the zero
	// value, reached only by tests and embedded use. Once UseListenAddress has
	// run the map is always non-empty — "bound everywhere, so allow any Host"
	// is not a state this type can be in, because it silently disables both the
	// rebinding defence and the Origin check that leans on it.
	allowedHosts map[string]bool
	// listenPort is set only once UseListenAddress has run, and is what tells
	// the origin check that it may insist on a port.
	listenPort string
}

func New(pm *project.ProjectManager, hub *realtime.Hub, webFS fs.FS) *Server {
	s := &Server{
		pm:       pm,
		hub:      hub,
		webFS:    webFS,
		notifier: notify.NewNotifier(pm),
		remotes:  remote.NewManager(pm),
		openDir:  fsbrowse.OpenDirectory,
		auth:     auth.LocalOnly{},
		abuse:    newAbuseGuard(),
		stop:     make(chan struct{}),
	}
	go s.notifier.Run(s.stop)
	// The backup loop touches the workspace immediately at startup, so it only
	// runs with a real project manager (a static-only server passes nil).
	if pm != nil {
		go s.remotes.RunBackupLoop(s.stop)
	}
	if hub != nil && pm != nil {
		// Change events go to the clients watching that project, not to every
		// browser: with several projects open, a global broadcast makes every
		// window reload for an edit it cannot see.
		hub.SetProjectResolver(func(name string) (string, error) {
			st, err := pm.StoreFor(name)
			if err != nil {
				return "", err
			}
			return st.Root(), nil
		})
	}
	return s
}

// Shutdown disconnects live collaboration clients, then stops server-owned
// background loops and flushes the remote backup. HTTP request draining is
// owned by cmd/nodevas, so callers should invoke this after http.Server.Shutdown
// has stopped accepting new work.
func (s *Server) Shutdown(ctx context.Context) error {
	var hubErr error
	// The HTTP server has stopped accepting work before Shutdown is called, but
	// upgraded websocket handlers outlive ordinary request draining. Closing the
	// hub makes their leaveAllDocs persistence complete before the project
	// manager (and, in tests, its temporary workspace) goes away.
	if s.hub != nil {
		hubErr = s.hub.Close()
	}
	s.stopOnce.Do(func() { close(s.stop) })
	if s.pm == nil || s.remotes == nil {
		return hubErr
	}
	return errors.Join(hubErr, s.remotes.FinalBackup(ctx))
}

// notifyRoom broadcasts a change to the clients watching the project this
// request wrote to.
func (s *Server) notifyRoom(r *http.Request, evType, id string) {
	st := s.store(r)
	if st == nil {
		s.hub.Broadcast(evType, id)
		return
	}
	s.hub.BroadcastRoom(st.Root(), evType, id)
}

// UseAccounts switches the server from "whoever reaches loopback is the local
// user" to real accounts. Call it before Handler.
func (s *Server) UseAccounts(users *auth.UserStore) {
	s.auth = auth.NewSessionAuth(users)
	if s.pm != nil {
		s.pm.SetRemote(true)
	}
}

// UseAudit points the audit trail at the workspace database. Servers with
// accounts should configure it before building their handler.
func (s *Server) UseAudit(store *audit.Store) {
	s.audit = store
}

// UseMailer enables one-time passcode delivery. Call it before Handler.
// Without it, a server with accounts can hand out no passcodes and therefore
// admits nobody, which the passcode endpoint reports as 503 rather than
// leaving people waiting for mail.
func (s *Server) UseMailer(mailer system.PasscodeMailer) {
	s.mailer = mailer
}

// SetMaxActiveUsers caps how many distinct accounts may be signed in at once.
// Zero means no cap. Call it after UseAccounts; on a server without accounts
// there is nobody to count and it does nothing.
func (s *Server) SetMaxActiveUsers(limit int) {
	if accounts, ok := s.auth.(*auth.SessionAuth); ok {
		accounts.SetMaxActiveUsers(limit)
	}
}

// SetVisitor configures the shared read-only credential. Both halves empty
// turns it off, which is the default. Call it after UseAccounts; a server
// without accounts has no sign-in for it to belong to.
//
// The credential is stored in the workspace database, not in this process, so
// `nodevas visitor on|off` changes it while the server runs. This entry point
// exists for tests and embedders that already hold a Server.
func (s *Server) SetVisitor(pin, otp string) error {
	accounts, ok := s.auth.(*auth.SessionAuth)
	if !ok {
		return nil
	}
	return accounts.SetVisitor(context.Background(), pin, otp)
}

// ErrWildcardNeedsHostName is returned when a wildcard bind arrives with no
// host name to answer on. Accepting it would mean accepting every Host, which
// hands a rebinding attacker a matching Host/Origin pair.
var ErrWildcardNeedsHostName = errors.New(
	"a wildcard listen address needs at least one Host name to answer on " +
		"(pass --hostname); without one the server cannot tell its own name " +
		"from an attacker's")

// UseListenAddress tells the server which address it was bound to, which is
// what lets it refuse a request whose Host header names somebody else's
// domain. Without it a page on evil.example whose DNS answers 127.0.0.1
// reaches this API with a matching Origin and Host pair. extraHosts are the
// further names the deployment legitimately answers on, e.g. the public name
// in front of a reverse proxy. Call it before Handler.
//
// A wildcard bind with no name in extraHosts is an error: the reverse-proxy
// shape forwards whatever name the browser used, so there is nothing left to
// check. The server falls back to the loopback names in that case, so a caller
// that ignores the error ends up over-strict rather than wide open.
func (s *Server) UseListenAddress(host string, port int, extraHosts []string) error {
	s.listenPort = strconv.Itoa(port)
	allowed := make(map[string]bool, len(extraHosts)+3)
	for _, extra := range extraHosts {
		if name := normalizeHostName(extra); name != "" {
			allowed[name] = true
		}
	}
	name := normalizeHostName(host)
	var err error
	switch {
	case isLoopbackName(name):
		// The whole loopback range is one machine, so the usual three names
		// stay valid whichever of them was bound; the bound one is added on
		// its own account because 127.0.0.2 is loopback but is not 127.0.0.1.
		addLoopbackNames(allowed)
		allowed[name] = true
	case name == "" || name == "0.0.0.0" || name == "::":
		if len(allowed) == 0 {
			err = ErrWildcardNeedsHostName
		}
		// A wildcard also listens on loopback, so the local names stay valid.
		addLoopbackNames(allowed)
	default:
		allowed[name] = true
	}
	s.allowedHosts = allowed
	return err
}

// Authenticator reports how the server identifies callers.
func (s *Server) Authenticator() auth.Authenticator { return s.auth }

// store returns the store this request targets: the project named by
// ?project= / X-Nodevas-Project, or the active sub-project when neither is set.
func (s *Server) store(r *http.Request) *store.Store {
	return httpx.StoreFor(r, s.pm)
}
