// Package copilot is the voice copilot's server side: conversations and their
// transcripts, the permission gate, and the turn loop that drives a model
// through the same tools an MCP agent gets. See COPILOT.md.
//
// The package knows nothing about HTTP beyond the router it hands to the tool
// bridge. internal/httpapi/copilot turns its calls into routes and its events
// into an NDJSON stream.
package copilot

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"nodevas/internal/copilot/provider"
	"nodevas/internal/copilot/toolbridge"
	"nodevas/internal/db"
	"nodevas/internal/identity"
)

var (
	// ErrInvalid wraps a request the caller got wrong (400).
	ErrInvalid = errors.New("invalid request")
	// ErrBusy is a second turn on a conversation that already has one (409).
	// Two turns interleaving their tool calls and transcript entries would
	// leave a history no model can replay.
	ErrBusy = errors.New("this conversation already has a turn in progress")
	// ErrUnavailable wraps the reasons the copilot cannot answer at all: it is
	// switched off, has no key, or its provider could not be built (503).
	ErrUnavailable = errors.New("the copilot is unavailable")
	// ErrBridge wraps a failure to open the in-process tool session (502).
	ErrBridge = errors.New("the copilot could not reach its tools")
)

// Defaults for the waits and bounds the spec fixes.
const (
	defaultPermissionTimeout = 120 * time.Second
	defaultClientToolTimeout = 60 * time.Second
	defaultMaxToolCalls      = 25
	// maxTurnTextBytes bounds one utterance. Speech recognition produces a
	// sentence or two; a pasted essay is still welcome, a megabyte is not.
	maxTurnTextBytes = 32 << 10
	// maxTitleRunes is how much of the first utterance names a conversation.
	maxTitleRunes = 40
	// MaxTitleBytes bounds a title set through the API.
	MaxTitleBytes = 400
)

// Options are the Service's collaborators. The provider and the bridge are
// functions so tests can run the whole turn loop against fakes, and so this
// package does not decide which vendors exist.
type Options struct {
	Database *db.DB
	// Keys holds the API key an administrator saved. Optional.
	Keys     KeyStore
	Defaults Defaults

	NewProvider func(provider.Config) (provider.Provider, error)
	// Remote reaches model calls made on runners, for KeySourceRunner
	// (remote.go). Nil means only a server-held key works.
	Remote RunnerLLM
	// DefaultModel names a provider's default model when nothing else does.
	DefaultModel func(kind string) string
	OpenBridge   func(context.Context, toolbridge.Options) (toolbridge.Bridge, error)
	// Handler returns the server's own router, which the bridge serves tool
	// calls through. A function because the router is built after the
	// service: the service is one of the things the router routes to.
	Handler func() http.Handler

	AgentRole           identity.AgentClass
	MaxToolCallsPerTurn int

	// Zero means the spec's 120 s and 60 s. Tests shorten them.
	PermissionTimeout time.Duration
	ClientToolTimeout time.Duration
}

// Service is the copilot. One per server.
type Service struct {
	opts     Options
	store    *Store
	settings *settingsStore

	mu sync.Mutex
	// turns are the turns in progress, by turn id; busy maps a conversation
	// to its turn.
	turns map[string]*activeTurn
	busy  map[string]string
	// sessionAllowed remembers "allow for this conversation" answers, by
	// conversation and tool name. In memory on purpose: a restart is a fair
	// moment to start asking again.
	sessionAllowed map[string]map[string]bool
}

// New builds the service.
func New(opts Options) *Service {
	if opts.PermissionTimeout <= 0 {
		opts.PermissionTimeout = defaultPermissionTimeout
	}
	if opts.ClientToolTimeout <= 0 {
		opts.ClientToolTimeout = defaultClientToolTimeout
	}
	if opts.MaxToolCallsPerTurn <= 0 {
		opts.MaxToolCallsPerTurn = defaultMaxToolCalls
	}
	if opts.AgentRole == identity.AgentHuman {
		opts.AgentRole = identity.AgentOrchestrator
	}
	return &Service{
		opts:  opts,
		store: NewStore(opts.Database),
		settings: &settingsStore{
			database: opts.Database, keys: opts.Keys,
			defaults: opts.Defaults, defaultModel: opts.DefaultModel,
		},
		turns:          map[string]*activeTurn{},
		busy:           map[string]string{},
		sessionAllowed: map[string]map[string]bool{},
	}
}

// Status is what the panel needs to know before it offers anything.
type Status struct {
	Enabled     bool   `json:"enabled"`
	Provider    string `json:"provider"`
	Model       string `json:"model"`
	Modes       []Mode `json:"modes"`
	AllowBypass bool   `json:"allowBypass"`
	Reason      string `json:"reason,omitempty"`
	// KeySource is where this person's model calls run: server or runner.
	KeySource string `json:"keySource"`
	// Runner names the runner that makes them, with KeySourceRunner.
	Runner string `json:"runner,omitempty"`
	// NeedsRunner: everything is in place but this person's runner, which
	// is offline or has no copilot key. The panel says how to start it
	// rather than hiding the copilot.
	NeedsRunner bool `json:"needsRunner,omitempty"`
}

// Status reports whether the copilot can answer this person, and with what.
func (s *Service) Status(ctx context.Context, actor identity.Actor) (Status, error) {
	settings, key, _, err := s.settings.resolve(ctx)
	if err != nil {
		return Status{}, err
	}
	source := effectiveKeySource(settings, key)
	status := Status{
		Provider:    settings.Provider,
		Model:       settings.Model,
		Modes:       availableModes(settings.AllowBypass),
		AllowBypass: settings.AllowBypass,
		KeySource:   source,
	}
	if source == KeySourceRunner {
		status.Reason = s.baseUnavailableReason()
		if status.Reason == "" {
			runner, info, err := s.runnerTarget(ctx, actor.ID)
			if err != nil {
				status.Reason = err.Error()
				status.NeedsRunner = s.opts.Remote != nil
			} else {
				status.Provider, status.Model, status.Runner = info.Provider, info.Model, runner.Name
			}
		}
	} else {
		status.Reason = s.unavailableReason(settings, key)
	}
	status.Enabled = status.Reason == ""
	return status, nil
}

// baseUnavailableReason is what stops every turn whatever holds the key.
func (s *Service) baseUnavailableReason() string {
	switch {
	case !s.opts.Defaults.Enabled:
		return "the copilot is turned off in the server configuration (copilot.enabled)"
	case s.opts.OpenBridge == nil:
		return "this server was built without the copilot's tool bridge"
	}
	return ""
}

// unavailableReason says why no turn can run, or "" when one can.
func (s *Service) unavailableReason(settings Settings, key string) string {
	switch {
	case !s.opts.Defaults.Enabled:
		return "the copilot is turned off in the server configuration (copilot.enabled)"
	case s.opts.NewProvider == nil || s.opts.OpenBridge == nil:
		return "this server was built without the copilot's provider or tool bridge"
	case settings.Model == "":
		return "no model is configured for the copilot"
	// A local OpenAI-compatible server (Ollama, LM Studio, vLLM) usually
	// wants no key at all, so a base URL is enough there.
	case key == "" && !(settings.Provider == "openai" && settings.BaseURL != ""):
		return "no API key is configured: set NODEVAS_COPILOT_API_KEY or add one in the copilot settings"
	}
	return ""
}

// Settings returns the admin view of the settings.
func (s *Service) Settings(ctx context.Context) (SettingsView, error) {
	return s.settings.view(ctx)
}

// UpdateSettings applies an administrator's change and returns the result.
func (s *Service) UpdateSettings(ctx context.Context, update SettingsUpdate) (SettingsView, error) {
	if err := s.settings.update(ctx, update); err != nil {
		return SettingsView{}, err
	}
	return s.settings.view(ctx)
}

// ListConversations returns the caller's conversations, newest first.
func (s *Service) ListConversations(ctx context.Context, actor identity.Actor, project string) ([]Conversation, error) {
	return s.store.ListConversations(ctx, actor.ID, strings.TrimSpace(project))
}

// CreateConversation starts a conversation for the caller.
func (s *Service) CreateConversation(ctx context.Context, actor identity.Actor, project string, mode Mode) (Conversation, error) {
	if mode == "" {
		mode = ModeDefault
	}
	if !mode.Valid() {
		return Conversation{}, fmt.Errorf("%w: unknown mode %q", ErrInvalid, mode)
	}
	return s.store.CreateConversation(ctx, actor.ID, strings.TrimSpace(project), mode)
}

// Conversation returns one of the caller's conversations with its transcript.
func (s *Service) Conversation(ctx context.Context, actor identity.Actor, id string) (Conversation, []Entry, error) {
	conversation, err := s.store.Conversation(ctx, actor.ID, id)
	if err != nil {
		return Conversation{}, nil, err
	}
	entries, err := s.store.Entries(ctx, id)
	return conversation, entries, err
}

// UpdateConversation changes a conversation's mode or title.
func (s *Service) UpdateConversation(ctx context.Context, actor identity.Actor, id string, patch ConversationPatch) (Conversation, error) {
	if patch.Mode != nil && !patch.Mode.Valid() {
		return Conversation{}, fmt.Errorf("%w: unknown mode %q", ErrInvalid, *patch.Mode)
	}
	if patch.Title != nil {
		title := strings.TrimSpace(*patch.Title)
		if len(title) > MaxTitleBytes || !utf8.ValidString(title) || hasControl(title) {
			return Conversation{}, fmt.Errorf("%w: title is too long or not text", ErrInvalid)
		}
		patch.Title = &title
	}
	return s.store.UpdateConversation(ctx, actor.ID, id, patch)
}

// DeleteConversation removes one of the caller's conversations, stopping a
// turn that is still running on it first.
func (s *Service) DeleteConversation(ctx context.Context, actor identity.Actor, id string) error {
	if _, err := s.store.Conversation(ctx, actor.ID, id); err != nil {
		return err
	}
	s.mu.Lock()
	if turnID, ok := s.busy[id]; ok {
		if turn := s.turns[turnID]; turn != nil {
			turn.cancel()
		}
	}
	delete(s.sessionAllowed, id)
	s.mu.Unlock()
	return s.store.DeleteConversation(ctx, actor.ID, id)
}

// activeTurn is a turn in progress: how to stop it, and the questions it is
// waiting on the browser to answer.
type activeTurn struct {
	id             string
	conversationID string
	accountID      string
	cancel         context.CancelFunc

	mu          sync.Mutex
	permissions map[string]chan permissionAnswer
	clientCalls map[string]chan toolbridge.Result
}

type permissionAnswer struct {
	decision Decision
	message  string
}

// register claims the conversation for a turn.
func (s *Service) register(turn *activeTurn) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, taken := s.busy[turn.conversationID]; taken {
		return ErrBusy
	}
	s.turns[turn.id] = turn
	s.busy[turn.conversationID] = turn.id
	return nil
}

func (s *Service) unregister(turn *activeTurn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.turns, turn.id)
	if s.busy[turn.conversationID] == turn.id {
		delete(s.busy, turn.conversationID)
	}
}

// lookup finds a turn the caller owns. Another account's turn, a turn on
// another conversation and a finished turn all answer ErrNotFound.
func (s *Service) lookup(actor identity.Actor, conversationID, turnID string) (*activeTurn, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	turn := s.turns[turnID]
	if turn == nil || turn.accountID != actor.ID || turn.conversationID != conversationID {
		return nil, ErrNotFound
	}
	return turn, nil
}

// Decide answers a permission request.
func (s *Service) Decide(actor identity.Actor, conversationID, turnID, requestID string, decision Decision, message string) error {
	if !decision.Valid() {
		return fmt.Errorf("%w: decision must be allow, allow_session or deny", ErrInvalid)
	}
	turn, err := s.lookup(actor, conversationID, turnID)
	if err != nil {
		return err
	}
	turn.mu.Lock()
	answer := turn.permissions[requestID]
	delete(turn.permissions, requestID)
	turn.mu.Unlock()
	if answer == nil {
		return ErrNotFound
	}
	// Buffered and taken out of the map above, so this never blocks and a
	// second answer to the same request is a 404 rather than a second send.
	answer <- permissionAnswer{decision: decision, message: message}
	return nil
}

// ClientResult answers a client tool call.
func (s *Service) ClientResult(actor identity.Actor, conversationID, turnID, callID, output string, isError bool) error {
	turn, err := s.lookup(actor, conversationID, turnID)
	if err != nil {
		return err
	}
	turn.mu.Lock()
	answer := turn.clientCalls[callID]
	delete(turn.clientCalls, callID)
	turn.mu.Unlock()
	if answer == nil {
		return ErrNotFound
	}
	answer <- toolbridge.Result{Output: output, IsError: isError}
	return nil
}

// Cancel stops a turn. The turn finishes what it is storing and reports
// turn_finished with stop "cancelled".
func (s *Service) Cancel(actor identity.Actor, conversationID, turnID string) error {
	turn, err := s.lookup(actor, conversationID, turnID)
	if err != nil {
		return err
	}
	turn.cancel()
	return nil
}

func (s *Service) allowedForSession(conversationID, tool string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessionAllowed[conversationID][tool]
}

func (s *Service) allowForSession(conversationID, tool string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sessionAllowed[conversationID] == nil {
		s.sessionAllowed[conversationID] = map[string]bool{}
	}
	s.sessionAllowed[conversationID][tool] = true
}
