package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// signedIn opens a real session and returns its cookie value.
func signedIn(t *testing.T, sessions *SessionAuth) string {
	t.Helper()
	_, token, _, err := sessions.Login(context.Background(), "ann", "correct-horse-battery")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	return token
}

func requestWith(token string) *http.Request {
	request := httptest.NewRequest(http.MethodGet, "/api/projects", nil)
	request.AddCookie(&http.Cookie{Name: SessionCookieName, Value: token})
	return request
}

func TestSessionSurvivesARestart(t *testing.T) {
	sessions, users := otpStoreForTest(t)
	token := signedIn(t, sessions)

	// A restart is a second SessionAuth over the same accounts database: the
	// process is gone, the file is not.
	restarted := NewSessionAuth(users)
	actor, err := restarted.Authenticate(requestWith(token))
	if err != nil {
		t.Fatalf("Authenticate after restart: %v", err)
	}
	if actor.Name != "ann" {
		t.Fatalf("actor = %+v, want ann", actor)
	}
}

func TestRestartedSessionStillAnswersToTheAccount(t *testing.T) {
	sessions, users := otpStoreForTest(t)
	token := signedIn(t, sessions)

	// The account's password changes while the server is down. The persisted
	// session must not outlive the authority it was opened against — this is
	// what makes writing sessions to a file safe at all.
	if err := users.SetPassword(context.Background(), "ann", "a-different-long-password"); err != nil {
		t.Fatalf("set password: %v", err)
	}
	restarted := NewSessionAuth(users)
	if _, err := restarted.Authenticate(requestWith(token)); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("Authenticate after password change = %v, want unauthenticated", err)
	}
}

func TestSignOutIsRememberedAcrossARestart(t *testing.T) {
	sessions, users := otpStoreForTest(t)
	token := signedIn(t, sessions)
	if err := sessions.Logout(token); err != nil {
		t.Fatalf("logout: %v", err)
	}

	restarted := NewSessionAuth(users)
	if _, err := restarted.Authenticate(requestWith(token)); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("Authenticate after logout = %v, want unauthenticated", err)
	}
}

func TestExpiredSessionsAreNotReloaded(t *testing.T) {
	sessions, users := otpStoreForTest(t)
	token := signedIn(t, sessions)

	// Age the stored row past its TTL, the way a server left off overnight
	// would find it.
	if _, err := users.database.ExecContext(context.Background(),
		`UPDATE sessions SET expires_at = ?`,
		stamp(time.Now().Add(-time.Minute))); err != nil {
		t.Fatalf("age session: %v", err)
	}
	restarted := NewSessionAuth(users)
	if len(restarted.sessions) != 0 {
		t.Fatalf("loaded %d expired sessions", len(restarted.sessions))
	}
	if _, err := restarted.Authenticate(requestWith(token)); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("Authenticate on expired session = %v, want unauthenticated", err)
	}
}

func TestTheStoredRowIsNotAUsableCookie(t *testing.T) {
	sessions, users := otpStoreForTest(t)
	token := signedIn(t, sessions)

	var stored string
	if err := users.database.QueryRowContext(context.Background(),
		`SELECT token_hash FROM sessions`).Scan(&stored); err != nil {
		t.Fatalf("read session row: %v", err)
	}
	if stored == token {
		t.Fatal("the cookie itself was written to the database")
	}
	if stored != sessionKey(token) {
		t.Fatalf("stored key = %q, want the token digest", stored)
	}
	// The point of hashing: what a backup contains cannot be replayed.
	if _, err := sessions.Authenticate(requestWith(stored)); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("the stored value authenticated: %v", err)
	}
}

func TestRevokingAnAccountClearsItsStoredSessions(t *testing.T) {
	sessions, users := otpStoreForTest(t)
	token := signedIn(t, sessions)
	if err := sessions.RevokeUser(accountID(t, users, "ann")); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	var count int
	if err := users.database.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM sessions`).Scan(&count); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if count != 0 {
		t.Fatalf("sessions left after revoke = %d", count)
	}
	restarted := NewSessionAuth(users)
	if _, err := restarted.Authenticate(requestWith(token)); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("revoked session survived a restart: %v", err)
	}
}

func TestFailedSessionDeletionKeepsProcessAndRestartConsistent(t *testing.T) {
	tests := []struct {
		name   string
		inject func(*testing.T, *SessionAuth, *UserStore) func()
	}{
		{
			name: "delete statement",
			inject: func(t *testing.T, _ *SessionAuth, users *UserStore) func() {
				t.Helper()
				if _, err := users.database.ExecContext(context.Background(), `
CREATE TRIGGER fail_session_delete
BEFORE DELETE ON sessions
BEGIN
    SELECT RAISE(ABORT, 'injected session delete failure');
END`); err != nil {
					t.Fatalf("create failure trigger: %v", err)
				}
				return func() {
					if _, err := users.database.ExecContext(context.Background(),
						`DROP TRIGGER fail_session_delete`); err != nil {
						t.Fatalf("drop failure trigger: %v", err)
					}
				}
			},
		},
		{
			name: "commit",
			inject: func(t *testing.T, sessions *SessionAuth, users *UserStore) func() {
				t.Helper()
				injected := errors.New("injected session commit failure")
				sessions.store.runTx = func(ctx context.Context, body func(*sql.Tx) error) error {
					tx, err := users.database.BeginTx(ctx, nil)
					if err != nil {
						return err
					}
					if err := body(tx); err != nil {
						_ = tx.Rollback()
						return err
					}
					if err := tx.Rollback(); err != nil {
						return err
					}
					return injected
				}
				return func() { sessions.store.runTx = nil }
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sessions, users := otpStoreForTest(t)
			token := signedIn(t, sessions)
			key := sessionKey(token)
			restore := tc.inject(t, sessions, users)

			if err := sessions.Logout(token); !errors.Is(err, ErrSessionPersistence) {
				t.Fatalf("logout error = %v, want session persistence error", err)
			}
			if _, ok := sessions.sessions[key]; !ok {
				t.Fatal("failed logout deleted the process session")
			}
			if _, err := sessions.Authenticate(requestWith(token)); err != nil {
				t.Fatalf("process session after failed logout: %v", err)
			}

			// A new authenticator is the process-restart boundary: the failed
			// transaction must have left the same session in durable storage.
			restarted := NewSessionAuth(users)
			if _, err := restarted.Authenticate(requestWith(token)); err != nil {
				t.Fatalf("session after restart: %v", err)
			}

			restore()
			if err := sessions.Logout(token); err != nil {
				t.Fatalf("logout retry: %v", err)
			}
			if _, ok := sessions.sessions[key]; ok {
				t.Fatal("successful logout kept the process session")
			}
			if _, err := NewSessionAuth(users).Authenticate(requestWith(token)); !errors.Is(err, ErrUnauthenticated) {
				t.Fatalf("session after successful retry = %v, want unauthenticated", err)
			}
		})
	}
}

func TestFailedPasscodeRevocationKeepsSessionAndAuditIdentity(t *testing.T) {
	sessions, users := otpStoreForTest(t)
	token := signedIn(t, sessions)
	actorID := accountID(t, users, "ann")
	sessions.otps[actorID] = &pendingOTP{
		digest: digestOTP("OLD-CODE"), expires: time.Now().Add(time.Minute),
	}
	injected := errors.New("injected passcode revocation failure")
	sessions.store.runTx = func(context.Context, func(*sql.Tx) error) error {
		return injected
	}

	challenge, err := sessions.RequestOTP(nil, "ann-pin-long-enough")
	if !errors.Is(err, ErrSessionPersistence) || !errors.Is(err, injected) {
		t.Fatalf("RequestOTP error = %v, want wrapped persistence failure", err)
	}
	if challenge.Actor != "ann" {
		t.Fatalf("audit actor = %q, want ann", challenge.Actor)
	}
	if _, exists := sessions.otps[actorID]; exists {
		t.Fatal("failed resend left the previous passcode usable")
	}
	if _, err := sessions.Authenticate(requestWith(token)); err != nil {
		t.Fatalf("process session after failed revocation: %v", err)
	}
	if _, err := NewSessionAuth(users).Authenticate(requestWith(token)); err != nil {
		t.Fatalf("session after restart: %v", err)
	}
}

func TestStartupDurablyPrunesLegacySessionOverflow(t *testing.T) {
	_, users := otpStoreForTest(t)
	actor, revision, ok := users.ActorRevision(context.Background(), accountID(t, users, "ann"))
	if !ok {
		t.Fatal("ann account missing")
	}
	base := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	expires := stamp(time.Now().Add(SessionTTL))
	tokens := make([]string, maxSessions+1)
	err := users.database.Tx(context.Background(), func(tx *sql.Tx) error {
		for index := range tokens {
			tokens[index] = fmt.Sprintf("legacy-overflow-%04d", index)
			if _, err := tx.ExecContext(context.Background(), `
INSERT INTO sessions
    (token_hash, user_id, user_name, user_role, revision, created_at, expires_at)
VALUES (?, ?, ?, ?, ?, ?, ?)`,
				sessionKey(tokens[index]), actor.ID, actor.Name, string(actor.Role), revision,
				stamp(base.Add(time.Duration(index)*time.Second)), expires); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seed legacy sessions: %v", err)
	}

	sessions := NewSessionAuth(users)
	if len(sessions.sessions) != maxSessions {
		t.Fatalf("loaded sessions = %d, want %d", len(sessions.sessions), maxSessions)
	}
	var rows int
	if err := users.database.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM sessions`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != maxSessions {
		t.Fatalf("durable sessions = %d, want %d", rows, maxSessions)
	}
	oldest := tokens[0]
	if _, err := sessions.Authenticate(requestWith(oldest)); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("pruned legacy token = %v, want unauthenticated", err)
	}
	if err := sessions.Logout(tokens[len(tokens)-1]); err != nil {
		t.Fatalf("remove newest session: %v", err)
	}
	if _, err := NewSessionAuth(users).Authenticate(requestWith(oldest)); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("legacy token after capacity opened = %v, want unauthenticated", err)
	}
}

func TestStartupRejectsMalformedSessionKeysWithoutPanicking(t *testing.T) {
	_, users := otpStoreForTest(t)
	actor, revision, ok := users.ActorRevision(context.Background(), accountID(t, users, "ann"))
	if !ok {
		t.Fatal("ann account missing")
	}
	if _, err := users.database.ExecContext(context.Background(), `
INSERT INTO sessions
    (token_hash, user_id, user_name, user_role, revision, created_at, expires_at)
VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"x", actor.ID, actor.Name, string(actor.Role), revision,
		"not-a-timestamp", stamp(time.Now().Add(SessionTTL))); err != nil {
		t.Fatalf("seed malformed session: %v", err)
	}

	sessions := NewSessionAuth(users)
	if len(sessions.sessions) != 0 {
		t.Fatalf("malformed sessions loaded: %v", sessions.sessions)
	}
	var rows int
	if err := users.database.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM sessions`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatalf("malformed durable rows = %d, want 0", rows)
	}
}

func TestSessionCreationCannotLandAfterAccountRevocation(t *testing.T) {
	sessions, users := otpStoreForTest(t)
	actor, revision, ok := users.ActorRevision(context.Background(), accountID(t, users, "ann"))
	if !ok {
		t.Fatal("ann account missing")
	}

	saveStarted := make(chan struct{})
	releaseSave := make(chan struct{})
	sessions.store.beforeSave = func() {
		close(saveStarted)
		<-releaseSave
	}
	t.Cleanup(func() { sessions.store.beforeSave = nil })

	type opened struct {
		token string
		err   error
	}
	created := make(chan opened, 1)
	go func() {
		_, token, _, err := sessions.openSession(actor, revision)
		created <- opened{token: token, err: err}
	}()
	<-saveStarted
	if sessions.mu.TryLock() {
		sessions.mu.Unlock()
		close(releaseSave)
		<-created
		t.Fatal("in-flight session save did not hold the revoke ordering lock")
	}
	close(releaseSave)
	result := <-created
	if result.err != nil {
		t.Fatalf("open session: %v", result.err)
	}
	if err := sessions.RevokeUser(actor.ID); err != nil {
		t.Fatalf("revoke after save: %v", err)
	}
	if _, err := sessions.Authenticate(requestWith(result.token)); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("revoked process session = %v, want unauthenticated", err)
	}
	if _, err := NewSessionAuth(users).Authenticate(requestWith(result.token)); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("revoked session after restart = %v, want unauthenticated", err)
	}
}

func accountID(t *testing.T, users *UserStore, name string) string {
	t.Helper()
	var id string
	if err := users.database.QueryRowContext(context.Background(),
		`SELECT id FROM accounts WHERE name = ?`, name).Scan(&id); err != nil {
		t.Fatalf("look up %q: %v", name, err)
	}
	return id
}
