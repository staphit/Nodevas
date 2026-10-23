// Package agents tracks live AI coding agent sessions, routes agent-to-agent
// and human-to-agent messages, and coordinates human-in-the-loop approvals
// (ask_user). It adapts the reason-network collaboration model to Nodevas's
// server-hub architecture: single-machine multi-agent setups work out of the
// box over loopback, while shared servers automatically scope sessions,
// roster entries, and inboxes by authenticated account and project.
package agents

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"nodevas/internal/db"
)

const (
	// SessionTTL is how long a session stays online without a heartbeat.
	SessionTTL = 90 * time.Second
	// OfflineRetention is how long an idle/disconnected session remains visible
	// with `all=true` before being pruned from memory, counted from when it
	// went offline. It is also how long the session's name stays reserved for
	// its actor: a new session of the same actor asking for the name gets it
	// back (and the mail waiting under it), another actor cannot take it.
	OfflineRetention = 24 * time.Hour

	StateBusy = "busy"
	StateIdle = "idle"

	// InboundOpen delivers mail to the session as it arrives; InboundHold
	// keeps it as "held" until a person approves or rejects it.
	InboundOpen = "open"
	InboundHold = "hold"

	// MaxModelLen bounds the model name a heartbeat reports.
	MaxModelLen = 64
)

var (
	ErrInvalid      = errors.New("invalid argument")
	ErrNotFound     = errors.New("not found")
	ErrRateLimited  = errors.New("rate limit exceeded")
	ErrLoopDetected = errors.New("agent-to-agent thread limit reached; waiting for human input")
	ErrRefused      = errors.New("recipient refused message")
	ErrAskBusy      = errors.New("session already has a pending ask_user request")
	ErrNotHeld      = errors.New("message is not held")
)

// Session describes one connected (or recently seen) coding agent session.
type Session struct {
	ID        string `json:"id"`
	Project   string `json:"project"`
	ActorID   string `json:"actorId"`
	ActorName string `json:"actorName"`
	Name      string `json:"name"`
	Agent     string `json:"agent"`
	Role      string `json:"role,omitempty"`
	Repo      string `json:"repo,omitempty"`
	Branch    string `json:"branch,omitempty"`
	Cwd       string `json:"cwd,omitempty"`
	State     string `json:"state"`
	Since     int64  `json:"since"`
	Doing     string `json:"doing,omitempty"`
	NodeID    string `json:"nodeId,omitempty"`
	LastSeen  int64  `json:"lastSeen"`
	Online    bool   `json:"online"`
	// Inbound is InboundOpen or InboundHold.
	Inbound string `json:"inbound"`
	// Model is what the agent last reported it runs on, if anything.
	Model string `json:"model,omitempty"`
}

// Address returns the canonical `<actor>/<name>` handle for this session.
func (s Session) Address() string {
	actor := shortActor(s.ActorID)
	if actor == "" {
		return s.Name
	}
	return actor + "/" + s.Name
}

// ClaimInfo pairs an active node claim with the node's title so a session's
// `doing` field reflects the board task it holds even if the client never sent
// a custom status string.
type ClaimInfo struct {
	NodeID string
	Title  string
	Owner  string
}

// Heartbeat updates or registers an agent session.
type Heartbeat struct {
	SessionID string `json:"sessionId"`
	Project   string `json:"project"`
	ActorID   string `json:"actorId"`
	ActorName string `json:"actorName"`
	Name      string `json:"name,omitempty"`
	Agent     string `json:"agent,omitempty"`
	Role      string `json:"role,omitempty"`
	Repo      string `json:"repo,omitempty"`
	Branch    string `json:"branch,omitempty"`
	Cwd       string `json:"cwd,omitempty"`
	State     string `json:"state,omitempty"`
	Doing     string `json:"doing,omitempty"`
	NodeID    string `json:"nodeId,omitempty"`
	Model     string `json:"model,omitempty"`
}

// EventFunc is called whenever sessions, messages, or asks change in a project
// so the server can broadcast a WebSocket frame to watching browsers.
type EventFunc func(project, eventType string, payload any)

// Hub coordinates agent sessions, mailboxes, and human-in-the-loop asks.
type Hub struct {
	database *db.DB
	notify   EventFunc
	now      func() time.Time

	mu       sync.Mutex
	sessions map[string]*Session // key: project + "\x00" + sessionID

	// In-memory fallback stores when database == nil (used in lightweight tests)
	memMessages []Message
	memFiles    map[string][]storedFile // key: message ID
	memAsks     map[string]*Ask

	// Waiters for inbox long-polling: key is project
	inboxWaiters map[string]map[chan struct{}]struct{}
	// Waiters for pending ask_user requests: key is ask ID
	askWaiters map[string]chan Ask

	// Loop guard state
	senderHistory map[string][]int64 // key: project+"\x00"+from -> timestamps
	threadTurns   map[string]int     // key: project+"\x00"+threadID -> consecutive agent turns
}

// NewHub constructs an agent network hub backed by database (which may be nil
// in unit tests that exercise only in-memory behavior).
func NewHub(database *db.DB, notify EventFunc) *Hub {
	return &Hub{
		database:      database,
		notify:        notify,
		now:           time.Now,
		sessions:      make(map[string]*Session),
		memAsks:       make(map[string]*Ask),
		memFiles:      make(map[string][]storedFile),
		inboxWaiters:  make(map[string]map[chan struct{}]struct{}),
		askWaiters:    make(map[string]chan Ask),
		senderHistory: make(map[string][]int64),
		threadTurns:   make(map[string]int),
	}
}

// SetDatabase attaches a persistent workspace database to the hub.
func (h *Hub) SetDatabase(database *db.DB) {
	h.mu.Lock()
	h.database = database
	h.mu.Unlock()
}

func (h *Hub) emit(project, eventType string, payload any) {
	if h.notify != nil {
		h.notify(project, eventType, payload)
	}
}

func (h *Hub) nowMillis() int64 {
	return h.now().UnixMilli()
}

func newID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		panic(fmt.Sprintf("agents: random id: %v", err))
	}
	return hex.EncodeToString(raw[:])
}

func shortActor(actor string) string {
	actor = strings.TrimSpace(actor)
	actor = strings.TrimPrefix(actor, "mcp:")
	if actor == "" {
		return "local"
	}
	return actor
}

func sanitizeToken(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	var b strings.Builder
	lastDash := false
	for _, r := range raw {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_':
			b.WriteRune(r)
			lastDash = false
		case r == '-' || r == '.' || r == '/' || r == ' ' || r == ':':
			if b.Len() > 0 && !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

// Touch registers or refreshes a session and returns its current snapshot.
func (h *Hub) Touch(hb Heartbeat) (Session, error) {
	sessionID := strings.TrimSpace(hb.SessionID)
	if sessionID == "" {
		return Session{}, fmt.Errorf("%w: sessionId is required", ErrInvalid)
	}
	project := strings.Trim(strings.TrimSpace(hb.Project), "/")
	now := h.nowMillis()
	model := strings.TrimSpace(hb.Model)
	if len(model) > MaxModelLen {
		return Session{}, fmt.Errorf("%w: model exceeds %d characters", ErrInvalid, MaxModelLen)
	}

	state := strings.ToLower(strings.TrimSpace(hb.State))
	if state != StateBusy && state != StateIdle {
		state = StateIdle
	}
	agentKind := sanitizeToken(hb.Agent)
	if agentKind == "" {
		agentKind = "agent"
	}
	actorID := strings.TrimSpace(hb.ActorID)
	if actorID == "" {
		actorID = "local"
	}
	actorName := strings.TrimSpace(hb.ActorName)
	if actorName == "" {
		actorName = shortActor(actorID)
	}

	h.mu.Lock()
	h.pruneExpiredLocked(now)
	key := project + "\x00" + sessionID
	existing, ok := h.sessions[key]
	if !ok {
		baseName := sanitizeToken(hb.Name)
		if baseName == "" {
			prefix := sanitizeToken(filepath.Base(hb.Repo))
			if prefix == "" || prefix == "." {
				prefix = sanitizeToken(filepath.Base(project))
			}
			if prefix == "" || prefix == "." {
				prefix = "work"
			}
			baseName = prefix + "-" + agentKind
		}
		name := h.uniqueNameLocked(project, sessionID, actorID, baseName)
		existing = &Session{
			Inbound:   h.inheritedInboundLocked(project, sessionID, actorID, name),
			Model:     model,
			ID:        sessionID,
			Project:   project,
			ActorID:   actorID,
			ActorName: actorName,
			Name:      name,
			Agent:     agentKind,
			Role:      strings.TrimSpace(hb.Role),
			Repo:      strings.TrimSpace(hb.Repo),
			Branch:    strings.TrimSpace(hb.Branch),
			Cwd:       strings.TrimSpace(hb.Cwd),
			State:     state,
			Since:     now,
			Doing:     strings.TrimSpace(hb.Doing),
			NodeID:    strings.TrimSpace(hb.NodeID),
			LastSeen:  now,
			Online:    true,
		}
		h.sessions[key] = existing
	} else {
		// A session ID is bound to the actor that first registered it, so on a
		// shared server one account cannot take over another's session (and,
		// with it, its name and inbox) by replaying the ID.
		if !strings.EqualFold(existing.ActorID, actorID) {
			h.mu.Unlock()
			return Session{}, fmt.Errorf("%w: session %q belongs to another actor", ErrRefused, sessionID)
		}
		if hb.Name != "" {
			if wanted := sanitizeToken(hb.Name); wanted != "" && wanted != existing.Name {
				existing.Name = h.uniqueNameLocked(project, sessionID, actorID, wanted)
			}
		}
		if hb.ActorID != "" {
			existing.ActorID = actorID
		}
		if hb.ActorName != "" {
			existing.ActorName = actorName
		}
		if hb.Agent != "" {
			existing.Agent = agentKind
		}
		if hb.Role != "" {
			existing.Role = strings.TrimSpace(hb.Role)
		}
		if hb.Repo != "" {
			existing.Repo = strings.TrimSpace(hb.Repo)
		}
		if hb.Branch != "" {
			existing.Branch = strings.TrimSpace(hb.Branch)
		}
		if hb.Cwd != "" {
			existing.Cwd = strings.TrimSpace(hb.Cwd)
		}
		if hb.State != "" && state != existing.State {
			existing.State = state
			existing.Since = now
		}
		// Only an explicit idle clears the task: a heartbeat that names no state
		// (a periodic refresh, an agent hook after a tool call) keeps it.
		explicitIdle := strings.EqualFold(strings.TrimSpace(hb.State), StateIdle)
		if hb.Doing != "" || explicitIdle {
			existing.Doing = strings.TrimSpace(hb.Doing)
		}
		if hb.NodeID != "" || explicitIdle {
			existing.NodeID = strings.TrimSpace(hb.NodeID)
		}
		if model != "" {
			existing.Model = model
		}
		existing.LastSeen = now
		existing.Online = true
	}
	snapshot := *existing
	h.mu.Unlock()

	h.emit(project, "agents-changed", nil)
	return snapshot, nil
}

// Leave marks a session offline at once (its agent said the session ended),
// instead of waiting out SessionTTL. The entry stays for OfflineRetention so
// mail queued for it still has a name to wait under. Unknown sessions are a
// no-op; another actor's session is refused, as in Touch.
func (h *Hub) Leave(project, sessionID, actorID string) error {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return fmt.Errorf("%w: sessionId is required", ErrInvalid)
	}
	project = strings.Trim(strings.TrimSpace(project), "/")
	actorID = strings.TrimSpace(actorID)
	if actorID == "" {
		actorID = "local"
	}
	now := h.nowMillis()

	h.mu.Lock()
	s, ok := h.sessions[project+"\x00"+sessionID]
	if !ok {
		h.mu.Unlock()
		return nil
	}
	if !strings.EqualFold(s.ActorID, actorID) {
		h.mu.Unlock()
		return fmt.Errorf("%w: session %q belongs to another actor", ErrRefused, sessionID)
	}
	s.LastSeen = now - SessionTTL.Milliseconds() - 1
	s.Online = false
	s.State = StateIdle
	s.Since = now
	s.Doing = ""
	s.NodeID = ""
	h.mu.Unlock()

	h.emit(project, "agents-changed", nil)
	return nil
}

// inheritedInboundLocked is the inbound policy a new session starts with: the
// policy of the offline session of the same actor whose name it is taking
// over (the agent carrying on in a new conversation keeps its mail policy),
// or InboundOpen.
func (h *Hub) inheritedInboundLocked(project, sessionID, actorID, name string) string {
	var newest *Session
	for _, s := range h.sessions {
		if s.Project != project || s.ID == sessionID || s.Online ||
			!strings.EqualFold(s.Name, name) || !strings.EqualFold(s.ActorID, actorID) {
			continue
		}
		if newest == nil || s.LastSeen > newest.LastSeen {
			newest = s
		}
	}
	if newest != nil && newest.Inbound == InboundHold {
		return InboundHold
	}
	return InboundOpen
}

// sessionByNameLocked finds the session that answers to name in project: the
// online one if any, else the one seen most recently. A name can sit on two
// entries for a while, the offline session and the new one that took it over.
func (h *Hub) sessionByNameLocked(project, name string) *Session {
	var best *Session
	for _, s := range h.sessions {
		if s.Project != project || !strings.EqualFold(s.Name, name) {
			continue
		}
		if best == nil || (s.Online && !best.Online) ||
			(s.Online == best.Online && s.LastSeen > best.LastSeen) {
			best = s
		}
	}
	return best
}

// SetInbound sets a session's inbound policy. Only the session's own actor
// or an admin may; to anyone else the session does not exist.
func (h *Hub) SetInbound(project, sessionID, inbound, callerID string, callerAdmin bool) (Session, error) {
	project = strings.Trim(strings.TrimSpace(project), "/")
	sessionID = strings.TrimSpace(sessionID)
	inbound = strings.ToLower(strings.TrimSpace(inbound))
	if inbound != InboundOpen && inbound != InboundHold {
		return Session{}, fmt.Errorf("%w: inbound must be %q or %q", ErrInvalid, InboundOpen, InboundHold)
	}
	h.mu.Lock()
	h.pruneExpiredLocked(h.nowMillis())
	s, ok := h.sessions[project+"\x00"+sessionID]
	if !ok || !(callerAdmin || strings.EqualFold(s.ActorID, strings.TrimSpace(callerID))) {
		h.mu.Unlock()
		return Session{}, fmt.Errorf("%w: session %q", ErrNotFound, sessionID)
	}
	s.Inbound = inbound
	snapshot := *s
	h.mu.Unlock()

	h.emit(project, "agents-changed", nil)
	return snapshot, nil
}

// uniqueNameLocked picks base, or base-N when another session holds it. An
// offline session of the same actor gives its name up: that is the agent
// continuing under a new conversation ID (Claude Code's /clear), and its mail
// should follow it.
func (h *Hub) uniqueNameLocked(project, sessionID, actorID, base string) string {
	taken := func(candidate string) bool {
		for _, s := range h.sessions {
			if s.Project != project || s.ID == sessionID || !strings.EqualFold(s.Name, candidate) {
				continue
			}
			if s.Online || !strings.EqualFold(s.ActorID, actorID) {
				return true
			}
		}
		return false
	}
	if !taken(base) {
		return base
	}
	for i := 2; i < 1000; i++ {
		candidate := fmt.Sprintf("%s-%d", base, i)
		if !taken(candidate) {
			return candidate
		}
	}
	return base + "-" + newID()[:6]
}

// nameHeldByOtherLocked reports whether a known session in project goes by
// name but belongs to an actor other than actorID.
func (h *Hub) nameHeldByOtherLocked(project, name, actorID string) bool {
	if name == "" {
		return false
	}
	for _, s := range h.sessions {
		if s.Project == project && strings.EqualFold(s.Name, name) {
			return !strings.EqualFold(s.ActorID, actorID)
		}
	}
	return false
}

func (h *Hub) pruneExpiredLocked(now int64) {
	ttlCutoff := now - SessionTTL.Milliseconds()
	for k, s := range h.sessions {
		// A session goes offline SessionTTL after its last heartbeat (Leave
		// backdates LastSeen to make that now), and keeps its name for
		// OfflineRetention after that.
		if s.LastSeen+SessionTTL.Milliseconds() < now-OfflineRetention.Milliseconds() {
			delete(h.sessions, k)
			continue
		}
		s.Online = s.LastSeen >= ttlCutoff
	}
}

// ListSessions returns active sessions in project (plus recently offline ones
// when includeOffline is true), enriched with any live node claims.
func (h *Hub) ListSessions(project string, filter string, includeOffline bool, claims []ClaimInfo) []Session {
	project = strings.Trim(strings.TrimSpace(project), "/")
	filter = strings.ToLower(strings.TrimSpace(filter))
	now := h.nowMillis()

	claimsByOwner := make(map[string]ClaimInfo, len(claims))
	for _, c := range claims {
		owner := strings.ToLower(strings.TrimSpace(c.Owner))
		if owner != "" {
			claimsByOwner[owner] = c
			claimsByOwner[strings.TrimPrefix(owner, "mcp:")] = c
		}
	}

	h.mu.Lock()
	h.pruneExpiredLocked(now)
	out := make([]Session, 0, len(h.sessions))
	for _, s := range h.sessions {
		if s.Project != project {
			continue
		}
		if !includeOffline && !s.Online {
			continue
		}
		copySess := *s
		// Enrich from board claims if the session has not explicitly reported a task.
		if copySess.NodeID == "" {
			if cl, ok := claimsByOwner[strings.ToLower(copySess.Name)]; ok {
				copySess.NodeID = cl.NodeID
				if copySess.Doing == "" {
					copySess.Doing = cl.Title
				}
				copySess.State = StateBusy
			} else if cl, ok := claimsByOwner[strings.ToLower(copySess.ActorID)]; ok {
				copySess.NodeID = cl.NodeID
				if copySess.Doing == "" {
					copySess.Doing = cl.Title
				}
			} else if cl, ok := claimsByOwner[strings.ToLower(shortActor(copySess.ActorID))]; ok {
				copySess.NodeID = cl.NodeID
				if copySess.Doing == "" {
					copySess.Doing = cl.Title
				}
			}
		}
		if filter != "" && !matchesFilter(copySess, filter) {
			continue
		}
		out = append(out, copySess)
	}
	h.mu.Unlock()

	sort.Slice(out, func(i, j int) bool {
		if out[i].Online != out[j].Online {
			return out[i].Online
		}
		if out[i].ActorID != out[j].ActorID {
			return out[i].ActorID < out[j].ActorID
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func matchesFilter(s Session, filter string) bool {
	haystack := strings.ToLower(strings.Join([]string{
		s.Name, s.ActorID, s.ActorName, s.Agent, s.Role, s.Repo, s.Branch, s.Doing, s.NodeID, s.State,
	}, " "))
	return strings.Contains(haystack, filter)
}

// SessionByID looks up a session by its raw session ID within a project.
func (h *Hub) SessionByID(project, sessionID string) (Session, bool) {
	project = strings.Trim(strings.TrimSpace(project), "/")
	sessionID = strings.TrimSpace(sessionID)
	h.mu.Lock()
	defer h.mu.Unlock()
	h.pruneExpiredLocked(h.nowMillis())
	s, ok := h.sessions[project+"\x00"+sessionID]
	if !ok {
		return Session{}, false
	}
	return *s, true
}
