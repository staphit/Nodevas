// Package server exposes the engine over HTTP for the web UI and agents.
package server

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"nodevas/internal/agents"
	"nodevas/internal/audit"
	"nodevas/internal/auth"
	"nodevas/internal/autopilot"
	"nodevas/internal/db"
	"nodevas/internal/httpapi/fsbrowse"
	"nodevas/internal/httpapi/httpx"
	"nodevas/internal/httpapi/system"
	"nodevas/internal/members"
	"nodevas/internal/notify"
	"nodevas/internal/project"
	"nodevas/internal/realtime"
	"nodevas/internal/remote"
	"nodevas/internal/runners"
	"nodevas/internal/runnerwire"
	"nodevas/internal/runs"
	"nodevas/internal/store"
	"nodevas/internal/usage"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
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
	agents   *agents.Hub
	// runners serves /api/runners and /api/runner/*; nil until UseRunnersDB.
	runners *runners.Registry
	// runs serves /api/runs and the run traffic on the runner socket; nil
	// until UseRunnersDB.
	runs *runs.Dispatcher
	// usage keeps agent sessions' token usage and the prices that cost it;
	// nil until UseAgentsDB.
	usage *usage.Store
	// llm relays copilot model calls to runners; nil until UseRunnersDB.
	llm *runners.LLMRelay
	// stepUpWindow is how long a step-up lasts (W6); zero is the default.
	stepUpWindow time.Duration
	// members is project membership (members.go); nil until UseMembersDB.
	members *members.Store
	// users is the account store on a server with accounts; nil on loopback.
	users *auth.UserStore

	accessVerifier *auth.AccessVerifier
	accessRequired bool
	abuse          *abuseGuard
	stop           chan struct{}
	stopOnce       sync.Once
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
	// copilot serves /api/copilot when set; see UseCopilot.
	copilot RouteRegistrar
	// router is the last router Handler built, read by the copilot's tool
	// bridge from request goroutines while startup may still be writing it.
	router atomic.Pointer[builtRouter]
}

// builtRouter boxes the router for the atomic pointer.
type builtRouter struct{ handler http.Handler }

// RouteRegistrar is a handler package that adds its own routes under /api.
type RouteRegistrar interface {
	Register(api *gin.RouterGroup)
}

// UseCopilot enables the voice copilot's API (internal/httpapi/copilot).
// Call it before Handler. The copilot service's tool bridge should be given
// BuiltHandler, which is how its tool calls reach this server's own routes.
//
// It takes the handler set as an interface rather than importing it because
// the copilot reaches the MCP tools, and the MCP package's tests build this
// server: a direct import here would be a cycle.
func (s *Server) UseCopilot(api RouteRegistrar) {
	s.copilot = api
}

// BuiltHandler returns the router Handler built, or nil before Handler has
// run. Unlike Handler it builds nothing, so it is safe to call per request.
func (s *Server) BuiltHandler() http.Handler {
	if built := s.router.Load(); built != nil {
		return built.handler
	}
	return nil
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
	s.agents = agents.NewHub(nil, func(projectRoot, eventType string, payload any) {
		if s.hub == nil {
			return
		}
		// A notify message pops up on its recipient's screen, so it travels
		// as its own event naming the message; everything else is one
		// "reload the agent panel" signal.
		if eventType == "agent-notify" {
			id, _ := payload.(string)
			s.hub.BroadcastRoom(projectRoot, "agent-notify", id)
			return
		}
		// An external session's changes are news for one node: the event
		// names it, so only a browser showing that node refetches.
		if eventType == agents.SessionChangesEvent {
			nodeID, _ := payload.(string)
			if projectRoot == "" {
				s.hub.Broadcast(eventType, nodeID)
				return
			}
			s.hub.BroadcastRoom(projectRoot, eventType, nodeID)
			return
		}
		if projectRoot == "" {
			s.hub.Broadcast("agents-changed", "")
			return
		}
		s.hub.BroadcastRoom(projectRoot, "agents-changed", "")
	})
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
	s.users = users
	s.auth = auth.NewSessionAuth(users)
	if s.pm != nil {
		s.pm.SetRemote(true)
	}
}

// UseAccess puts Cloudflare Access in front of every route: with required, a
// request without a valid Access JWT is refused before anything else runs;
// without it, failures are only logged. Call it before Handler.
func (s *Server) UseAccess(verifier *auth.AccessVerifier, required bool) {
	s.accessVerifier = verifier
	s.accessRequired = required
}

// UseAudit points the audit trail at the workspace database. Servers with
// accounts should configure it before building their handler.
func (s *Server) UseAudit(store *audit.Store) {
	s.audit = store
}

// UseAgentsDB attaches the workspace database to the agent collaboration hub so
// messages and ask_user requests persist across restarts.
func (s *Server) UseAgentsDB(database *db.DB) {
	if s.agents != nil {
		s.agents.SetDatabase(database)
	}
	if database != nil {
		s.usage = usage.NewStore(database)
	}
}

// UseRunnersDB enables runners (nodevas runner) against the workspace
// database. Call it before Handler. Without it the runner endpoints do not
// exist and a runner token opens nothing.
func (s *Server) UseRunnersDB(database *db.DB) {
	s.runners = runners.NewRegistry(database, func() {
		if s.hub != nil {
			s.hub.Broadcast(runnerwire.ChangedEvent, "")
		}
	})
	s.runs = runs.NewDispatcher(runs.NewStore(database), s.runners,
		func(project, nodeID string) { s.broadcastProject(project, runnerwire.RunsChangedEvent, nodeID) },
		func(project, runID string) { s.broadcastProject(project, runnerwire.RunEventsEvent, runID) })
	s.runs.SetNotify(func(project, runID string) { s.broadcastProject(project, runnerwire.RunNotifyEvent, runID) })
	s.runs.SetAudit(s.auditRun)
	s.llm = runners.NewLLMRelay(s.runners)
	// W7: the autopilot's LLM advisor asks the owner's advisor model through
	// the same relay.
	autopilot.RegisterLLMAdvisor(autopilot.NewLLMAdvisor(s.llm))
}

// RunnerLLM is the relay for copilot model calls made on runners, or nil
// before UseRunnersDB.
func (s *Server) RunnerLLM() *runners.LLMRelay { return s.llm }

// SetStepUpWindow sets how long a step-up lasts (serve --step-up-minutes).
// Zero keeps the default. Call it before Handler.
func (s *Server) SetStepUpWindow(window time.Duration) {
	s.stepUpWindow = window
}

// configureStepUp applies the step-up setting to whoever needs it, once the
// authenticator is settled: on a server with accounts, sessions step up and
// runs record the dispatcher's step-up for their offers; on loopback there is
// nothing to step up and every offer vouches for its dispatcher.
func (s *Server) configureStepUp(runsAPI interface {
	UseStepUp(func(*http.Request) (time.Time, bool))
}) {
	accounts, remote := s.auth.(*auth.SessionAuth)
	window := s.stepUpWindow
	if remote {
		accounts.SetStepUpWindow(window)
		window = accounts.StepUpWindow()
	}
	if s.runs != nil {
		s.runs.SetStepUp(remote, window)
	}
	if remote && runsAPI != nil {
		runsAPI.UseStepUp(accounts.SteppedUpAt)
	}
}

// auditRun records a merge approval or result. Results arrive from a runner's
// socket, not a request, so the request audit trail never sees them.
func (s *Server) auditRun(entry runs.AuditEntry) {
	if s.audit == nil {
		return
	}
	project := ""
	if s.pm != nil {
		if st, err := s.pm.StoreFor(entry.Project); err == nil {
			project = st.Root()
		}
	}
	s.audit.RecordOrLog(context.Background(), audit.Event{
		At: time.Now(), Project: project, ActorID: entry.ActorID, ActorName: entry.ActorName,
		Action: entry.Action, Target: entry.Target, Detail: entry.Detail,
	})
}

// broadcastProject tells the clients watching a project, named as ?project=
// names it, about a change. Run changes arrive from a runner's socket, not a
// request, so there is no request store to take the room from.
func (s *Server) broadcastProject(name, evType, id string) {
	if s.hub == nil {
		return
	}
	if s.pm == nil {
		s.hub.Broadcast(evType, id)
		return
	}
	st, err := s.pm.StoreFor(name)
	if err != nil {
		// The project is gone; nobody is watching it.
		return
	}
	s.hub.BroadcastRoom(st.Root(), evType, id)
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

// SetAbuseOptions configures custom rate limits, bursts, and concurrency bounds.
func (s *Server) SetAbuseOptions(opts AbuseGuardOptions) {
	s.abuse = newAbuseGuardWithOptions(opts)
}

// AbuseStats returns current rate limiter health and rejection counts.
func (s *Server) AbuseStats() AbuseStats {
	if s.abuse == nil {
		return AbuseStats{}
	}
	return s.abuse.Stats()
}

// store returns the store this request targets: the project named by
// ?project= / X-Nodevas-Project, or the active sub-project when neither is set.
func (s *Server) store(r *http.Request) *store.Store {
	return httpx.StoreFor(r, s.pm)
}
