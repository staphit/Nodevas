package auth

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"log"
	"time"

	"nodevas/internal/db"
	"nodevas/internal/identity"
)

// sessionKey is how a session is named everywhere but the cookie itself: the
// sha256 of the token, hex.
//
// The raw token exists in one place — the browser — and in one moment, the
// request being authenticated. Neither the map nor the table holds a value
// anyone could paste into a cookie jar, which is what makes it safe to write
// sessions to a file that is backed up off the machine.
func sessionKey(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// sessionStore mirrors live sessions into the workspace database so a restart
// does not sign everybody out.
//
// The in-memory map stays the thing requests are answered from; this is a
// mirror of it, read once at startup. A create or renewal that fails is logged
// and otherwise ignored: losing that persistence costs a sign-in after the
// next restart. Destructive writes are different: they return their error and
// callers keep the in-memory session unless the transaction commits, so a
// restart can never revive something this process claimed to remove.
//
// A nil database — a UserStore built without one, as some tests do — turns
// every method into a no-op, and the sessions are simply in-memory as before.
type sessionStore struct {
	database *db.DB
	// beforeSave is a test seam for proving create/revoke ordering. Production
	// leaves it nil; the database operation remains the only side effect.
	beforeSave func()
	// runTx is the transaction boundary used by destructive operations. Tests
	// replace it to exercise statement and commit failures without weakening
	// the production DB type with exported fault-injection controls.
	runTx func(context.Context, func(*sql.Tx) error) error
}

func (s sessionStore) enabled() bool { return s.database != nil }

func (s sessionStore) save(key string, session *Session) {
	if !s.enabled() {
		return
	}
	if s.beforeSave != nil {
		s.beforeSave()
	}
	_, err := s.database.ExecContext(context.Background(), `
INSERT INTO sessions (token_hash, user_id, user_name, user_role, revision, created_at, expires_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (token_hash) DO UPDATE SET
    user_id    = excluded.user_id,
    user_name  = excluded.user_name,
    user_role  = excluded.user_role,
    revision   = excluded.revision,
    expires_at = excluded.expires_at`,
		key, session.actor.ID, session.actor.Name, string(session.actor.Role),
		session.revision, stamp(session.created), stamp(session.expires))
	if err != nil {
		log.Printf("session save: %v", err)
	}
}

// touch records a renewed expiry. Called at most once an hour per session, so
// it is a write the request path can afford.
func (s sessionStore) touch(key string, expires time.Time) {
	if !s.enabled() {
		return
	}
	if _, err := s.database.ExecContext(context.Background(),
		`UPDATE sessions SET expires_at = ? WHERE token_hash = ?`,
		stamp(expires), key); err != nil {
		log.Printf("session renew: %v", err)
	}
}

func (s sessionStore) tx(ctx context.Context, fn func(*sql.Tx) error) error {
	if s.runTx != nil {
		return s.runTx(ctx, fn)
	}
	return s.database.Tx(ctx, fn)
}

// remove deletes a set of persisted sessions atomically. Callers must not
// remove their in-memory copies until this returns nil: an error means the
// transaction did not commit and the database remains the source that a
// restart will load.
func (s sessionStore) remove(keys ...string) error {
	if !s.enabled() || len(keys) == 0 {
		return nil
	}
	ctx := context.Background()
	err := s.tx(ctx, func(tx *sql.Tx) error {
		for _, key := range keys {
			if _, err := tx.ExecContext(ctx,
				`DELETE FROM sessions WHERE token_hash = ?`, key); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		log.Printf("session delete: %v", err)
		return fmt.Errorf("%w: delete session: %w", ErrSessionPersistence, err)
	}
	return nil
}

func (s sessionStore) removeUser(userID string) error {
	if !s.enabled() {
		return nil
	}
	ctx := context.Background()
	err := s.tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`DELETE FROM sessions WHERE user_id = ?`, userID)
		return err
	})
	if err != nil {
		log.Printf("session revoke: %v", err)
		return fmt.Errorf("%w: revoke sessions: %w", ErrSessionPersistence, err)
	}
	return nil
}

// load reads the sessions that have not expired.
//
// Only the newest maxSessions are kept, matching the cap the running server
// holds itself to: a table that grew while nobody swept it must not let a
// restart start over the limit.
func (s sessionStore) load(now time.Time) map[string]*Session {
	sessions := map[string]*Session{}
	if !s.enabled() {
		return sessions
	}
	ctx := context.Background()
	// Older releases could leave expired or over-cap rows behind after a
	// best-effort DELETE failed. Prune them durably before loading anything;
	// otherwise an old row just below LIMIT could become live after a newer
	// session is removed and the process restarts again.
	if err := s.tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
DELETE FROM sessions
WHERE expires_at <= ?
   OR length(token_hash) != ?
   OR token_hash NOT IN (
       SELECT token_hash
       FROM sessions
       WHERE expires_at > ?
       ORDER BY created_at DESC, token_hash DESC
       LIMIT ?
	)`, stamp(now), sha256.Size*2, stamp(now), maxSessions)
		return err
	}); err != nil {
		log.Printf("session startup cleanup: %v", err)
		// Loading after a failed cleanup would preserve a latent resurrection
		// set. Fail closed for this process instead.
		return sessions
	}
	rows, err := s.database.QueryContext(ctx, `
SELECT token_hash, user_id, user_name, user_role, revision, created_at, expires_at
FROM sessions
WHERE expires_at > ?
ORDER BY created_at DESC, token_hash DESC
LIMIT ?`, stamp(now), maxSessions)
	if err != nil {
		log.Printf("session load: %v", err)
		return sessions
	}
	defer rows.Close()
	for rows.Next() {
		var key, userID, name, role, revision, created, expires string
		if err := rows.Scan(&key, &userID, &name, &role, &revision, &created, &expires); err != nil {
			log.Printf("session load: %v", err)
			return sessions
		}
		if len(key) != sha256.Size*2 {
			log.Printf("session load: invalid token hash length")
			continue
		}
		if _, err := hex.DecodeString(key); err != nil {
			log.Printf("session load: invalid token hash")
			continue
		}
		createdAt, createdErr := time.Parse(time.RFC3339, created)
		expiresAt, expiresErr := time.Parse(time.RFC3339, expires)
		// An unreadable row is dropped rather than guessed at: a session with a
		// timestamp this program cannot parse is one it cannot expire either.
		if createdErr != nil || expiresErr != nil {
			log.Printf("session load: unreadable timestamps on %s", key[:8])
			continue
		}
		sessions[key] = &Session{
			actor:    identity.Actor{ID: userID, Name: name, Role: identity.Role(role)},
			revision: revision,
			created:  createdAt,
			expires:  expiresAt,
		}
	}
	if err := rows.Err(); err != nil {
		log.Printf("session load: %v", err)
	}
	return sessions
}

// stamp is the format every timestamp in this database uses.
func stamp(at time.Time) string {
	return at.UTC().Format(time.RFC3339)
}
