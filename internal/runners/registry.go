// Package runners is the hub's record of runners: the machines, each running
// `nodevas runner`, that execute agent runs for their owner.
//
// The hub only records and talks to runners. It never starts a process: this
// package must not import os/exec (noexec_test.go holds it to that), because
// the server it lives in is reachable from the internet and a run is code
// execution. Only the runner, on its owner's machine, executes anything.
package runners

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"nodevas/internal/auth"
	"nodevas/internal/db"
	"nodevas/internal/identity"
	"nodevas/internal/runnerwire"
)

var (
	// ErrNotFound covers a runner that does not exist, is revoked, or is not
	// the caller's to see: a member probing IDs learns nothing either way.
	ErrNotFound = errors.New("runner not found")
	// ErrInvalid is a name or hello that breaks the limits.
	ErrInvalid = errors.New("invalid runner request")
	// ErrUnauthenticated is a missing, malformed, unknown or revoked token.
	ErrUnauthenticated = errors.New("runner token required")
)

// MaxNameRunes bounds a runner's display name.
const MaxNameRunes = 64

// String limits on a hello. Generous for real CLIs and paths, small enough
// that a hello cannot turn the runners table into storage.
const (
	maxShortField = 256
	maxPathField  = 1024
)

// Conn is a live runner connection as the registry sees it: something it can
// send messages to and tell to go away.
type Conn interface {
	// Bye tells the runner why it is being disconnected and closes the
	// connection. It must not block for long; the caller may hold no locks.
	Bye(reason string)
	// Send writes one message to the runner. It blocks at most for the
	// connection's write timeout, so a stalled runner costs the caller
	// seconds, not a hung request.
	Send(env runnerwire.Envelope) error
}

// ErrOffline is Send to a runner with no live connection.
var ErrOffline = errors.New("runner is offline")

// Registry stores runners and their tokens and tracks which are connected.
type Registry struct {
	db     *db.DB
	notify func()
	now    func() time.Time

	mu   sync.Mutex
	live map[string]Conn
}

// NewRegistry builds a registry over the workspace database. notify, when set,
// is called after anything a runners list would show has changed.
func NewRegistry(database *db.DB, notify func()) *Registry {
	return &Registry{
		db:     database,
		notify: notify,
		now:    time.Now,
		live:   make(map[string]Conn),
	}
}

func (r *Registry) changed() {
	if r.notify != nil {
		r.notify()
	}
}

func (r *Registry) stamp() string {
	return r.now().UTC().Format(time.RFC3339)
}

// HashToken is how a token is stored and looked up.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func newID() (string, error) {
	var raw [12]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

// CleanName trims a runner name and checks it: 1..MaxNameRunes runes of
// printable text.
func CleanName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || !utf8.ValidString(name) || utf8.RuneCountInString(name) > MaxNameRunes {
		return "", fmt.Errorf("%w: name must be 1-%d characters", ErrInvalid, MaxNameRunes)
	}
	for _, c := range name {
		if !unicode.IsPrint(c) {
			return "", fmt.Errorf("%w: name must be printable text", ErrInvalid)
		}
	}
	return name, nil
}

// Create registers a runner for owner and returns it with its token. The
// token is returned only here: the database keeps its hash.
func (r *Registry) Create(ctx context.Context, owner identity.Actor, name string) (runnerwire.Runner, string, error) {
	name, err := CleanName(name)
	if err != nil {
		return runnerwire.Runner{}, "", err
	}
	if strings.TrimSpace(owner.ID) == "" {
		return runnerwire.Runner{}, "", fmt.Errorf("%w: no owner", ErrInvalid)
	}
	id, err := newID()
	if err != nil {
		return runnerwire.Runner{}, "", err
	}
	secret, err := auth.RandomToken()
	if err != nil {
		return runnerwire.Runner{}, "", err
	}
	token := runnerwire.TokenPrefix + secret
	ownerName := owner.Name
	if ownerName == "" {
		ownerName = owner.ID
	}
	now := r.stamp()
	err = r.db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO runners (id, owner_id, owner_name, name, created_at) VALUES (?, ?, ?, ?, ?)`,
			id, owner.ID, ownerName, name, now); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx,
			`INSERT INTO runner_tokens (token_hash, runner_id, created_at) VALUES (?, ?, ?)`,
			HashToken(token), id, now)
		return err
	})
	if err != nil {
		return runnerwire.Runner{}, "", err
	}
	r.changed()
	return runnerwire.Runner{
		ID: id, Name: name, OwnerID: owner.ID, OwnerName: ownerName, CreatedAt: now,
	}, token, nil
}

const runnerColumns = `id, owner_id, owner_name, name, created_at, last_seen_at, info`

func (r *Registry) scan(row interface{ Scan(...any) error }) (runnerwire.Runner, error) {
	var out runnerwire.Runner
	var info string
	if err := row.Scan(&out.ID, &out.OwnerID, &out.OwnerName, &out.Name, &out.CreatedAt, &out.LastSeenAt, &info); err != nil {
		return runnerwire.Runner{}, err
	}
	if info != "" {
		var parsed runnerwire.Info
		if json.Unmarshal([]byte(info), &parsed) == nil {
			out.Info = &parsed
		}
	}
	out.Online = r.Online(out.ID)
	return out, nil
}

// List returns the runners viewer may see: an administrator sees all, anyone
// else their own. Revoked runners are left out.
func (r *Registry) List(ctx context.Context, viewer identity.Actor) ([]runnerwire.Runner, error) {
	query := `SELECT ` + runnerColumns + ` FROM runners WHERE revoked_at = ''`
	var args []any
	if !viewer.IsAdmin() {
		query += ` AND owner_id = ?`
		args = append(args, viewer.ID)
	}
	query += ` ORDER BY created_at, id`
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []runnerwire.Runner{}
	for rows.Next() {
		runner, err := r.scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, runner)
	}
	return out, rows.Err()
}

// Get returns one live (unrevoked) runner, whoever owns it.
func (r *Registry) Get(ctx context.Context, id string) (runnerwire.Runner, error) {
	runner, err := r.scan(r.db.QueryRowContext(ctx,
		`SELECT `+runnerColumns+` FROM runners WHERE id = ? AND revoked_at = ''`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return runnerwire.Runner{}, ErrNotFound
	}
	return runner, err
}

// Revoke retires a runner and all its tokens, and disconnects it if it is
// connected. Only its owner or an administrator may.
func (r *Registry) Revoke(ctx context.Context, viewer identity.Actor, id string) error {
	runner, err := r.Get(ctx, id)
	if err != nil {
		return err
	}
	if !viewer.IsAdmin() && runner.OwnerID != viewer.ID {
		return ErrNotFound
	}
	now := r.stamp()
	err = r.db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`UPDATE runners SET revoked_at = ? WHERE id = ? AND revoked_at = ''`, now, id); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx,
			`UPDATE runner_tokens SET revoked_at = ? WHERE runner_id = ? AND revoked_at = ''`, now, id)
		return err
	})
	if err != nil {
		return err
	}
	r.kick(id, runnerwire.ByeRevoked)
	r.changed()
	return nil
}

// Authenticate resolves a runner token to its runner. Lookup is by hash, so
// the comparison never touches the secret itself.
func (r *Registry) Authenticate(ctx context.Context, token string) (runnerwire.Runner, error) {
	if !strings.HasPrefix(token, runnerwire.TokenPrefix) || len(token) > 256 {
		return runnerwire.Runner{}, ErrUnauthenticated
	}
	runner, err := r.scan(r.db.QueryRowContext(ctx,
		`SELECT r.id, r.owner_id, r.owner_name, r.name, r.created_at, r.last_seen_at, r.info
		   FROM runner_tokens t JOIN runners r ON r.id = t.runner_id
		  WHERE t.token_hash = ? AND t.revoked_at = '' AND r.revoked_at = ''`,
		HashToken(token)))
	if errors.Is(err, sql.ErrNoRows) {
		return runnerwire.Runner{}, ErrUnauthenticated
	}
	return runner, err
}

// BearerToken reads "Authorization: Bearer <token>" off a request.
func BearerToken(req *http.Request) string {
	value := strings.TrimSpace(req.Header.Get("Authorization"))
	scheme, token, ok := strings.Cut(value, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return ""
	}
	return strings.TrimSpace(token)
}

// ActorFor is who a runner's requests act as: its owner, marked as a worker
// agent so nothing mistakes it for the person at a browser.
func ActorFor(runner runnerwire.Runner) identity.Actor {
	return identity.Actor{ID: runner.OwnerID, Name: runner.OwnerName, Agent: identity.AgentWorker}
}

// ValidateInfo enforces the limits a hello must respect.
func ValidateInfo(info runnerwire.Info) error {
	bad := func(what string) error { return fmt.Errorf("%w: hello %s", ErrInvalid, what) }
	if len(info.Agents) > runnerwire.MaxAgents {
		return bad("lists too many agents")
	}
	if len(info.Repos) > runnerwire.MaxRepos {
		return bad("lists too many repos")
	}
	if len(info.Capabilities) > 32 {
		return bad("lists too many capabilities")
	}
	short := []string{info.Version, info.Hostname, info.OS, info.Arch}
	short = append(short, info.Capabilities...)
	models := 0
	for _, agent := range info.Agents {
		short = append(short, agent.Kind, agent.Version, agent.Error)
		models += len(agent.Models)
		for _, model := range agent.Models {
			if strings.TrimSpace(model.ID) == "" {
				return bad("has a model without an id")
			}
			short = append(short, model.ID, model.Label)
		}
	}
	if models > runnerwire.MaxModels {
		return bad("lists too many models")
	}
	var long []string
	for _, repo := range info.Repos {
		if strings.TrimSpace(repo.ID) == "" {
			return bad("has a repo without an id")
		}
		short = append(short, repo.ID, repo.Name)
		long = append(long, repo.Path, repo.Remote)
	}
	for _, value := range short {
		if len(value) > maxShortField || !utf8.ValidString(value) {
			return bad("has an over-long or invalid field")
		}
	}
	for _, value := range long {
		if len(value) > maxPathField || !utf8.ValidString(value) {
			return bad("has an over-long or invalid path")
		}
	}
	return nil
}

// SetHello stores a runner's latest description and marks it seen.
func (r *Registry) SetHello(ctx context.Context, id string, info runnerwire.Info) error {
	if err := ValidateInfo(info); err != nil {
		return err
	}
	raw, err := json.Marshal(info)
	if err != nil {
		return err
	}
	result, err := r.db.ExecContext(ctx,
		`UPDATE runners SET info = ?, last_seen_at = ? WHERE id = ? AND revoked_at = ''`,
		string(raw), r.stamp(), id)
	if err != nil {
		return err
	}
	if n, err := result.RowsAffected(); err == nil && n == 0 {
		return ErrNotFound
	}
	r.changed()
	return nil
}

// Touch records that a runner is still there. It does not notify: a ping is
// not news to anyone watching the list.
func (r *Registry) Touch(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE runners SET last_seen_at = ? WHERE id = ? AND revoked_at = ''`, r.stamp(), id)
	return err
}

// Attach records conn as the runner's live connection. A runner has at most
// one: an older connection is told it was replaced, so two copies of one
// runner never both take work. The returned func detaches conn, and only
// conn: a replaced connection detaching late must not remove its successor.
func (r *Registry) Attach(id string, conn Conn) (detach func()) {
	r.mu.Lock()
	previous := r.live[id]
	r.live[id] = conn
	r.mu.Unlock()
	if previous != nil {
		previous.Bye(runnerwire.ByeReplaced)
	}
	r.changed()
	var once sync.Once
	return func() {
		once.Do(func() {
			r.mu.Lock()
			removed := r.live[id] == conn
			if removed {
				delete(r.live, id)
			}
			r.mu.Unlock()
			if removed {
				r.changed()
			}
		})
	}
}

// Send delivers a message to the runner's live connection. No lock is held
// while it writes.
func (r *Registry) Send(id string, env runnerwire.Envelope) error {
	r.mu.Lock()
	conn := r.live[id]
	r.mu.Unlock()
	if conn == nil {
		return ErrOffline
	}
	return conn.Send(env)
}

// Online reports whether the runner has a live connection.
func (r *Registry) Online(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.live[id] != nil
}

func (r *Registry) kick(id, reason string) {
	r.mu.Lock()
	conn := r.live[id]
	delete(r.live, id)
	r.mu.Unlock()
	if conn != nil {
		conn.Bye(reason)
	}
}
