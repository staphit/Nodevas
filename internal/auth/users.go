package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"golang.org/x/crypto/argon2"

	"nodevas/internal/db"
	"nodevas/internal/identity"
)

// Passwords are hashed with argon2id. The parameters follow the OWASP
// baseline (64 MiB, 3 passes); they are stored with each hash so raising them
// later does not invalidate existing accounts.
const (
	argonTime    = 3
	argonMemory  = 64 * 1024
	argonThreads = 4
	argonKeyLen  = 32
	argonSaltLen = 16

	minPasswordLength = 10
	// Login passwords are never persisted in plaintext and Argon2 does not
	// benefit from accepting an attacker-controlled multi-megabyte input.
	maxLoginPasswordBytes = 4 << 10
	maxArgonConcurrent    = 2
)

var userNamePattern = regexp.MustCompile(`^[\p{L}\p{N}][\p{L}\p{N}._-]{0,63}$`)
var argonSlots = make(chan struct{}, maxArgonConcurrent)

// UserRecord is one account as exposed to account-management callers.
type UserRecord struct {
	ID        string        `json:"id"`
	Name      string        `json:"name"`
	Hash      string        `json:"hash"` // PHC-format argon2id string
	Role      identity.Role `json:"role"`
	CreatedAt string        `json:"createdAt"`
	// PinHash is the argon2id hash of the sign-in PIN, in the same PHC format
	// as Hash. Empty means the account cannot sign in to the web UI. The PIN
	// itself is never stored, here or anywhere else — see pin.go.
	PinHash string `json:"pinHash,omitempty"`
	// Email is where this account's one-time passcodes are sent. It is not a
	// contact field: changing it changes who can complete a sign-in, which is
	// why SetPin rotates the two together.
	Email string `json:"email,omitempty"`
}

// UserStore holds the accounts table of the workspace database.
//
// It caches nothing. Every method reads the rows it needs, because the
// guarantee this store owes the rest of the server is that an account change —
// from the CLI, from another process, or from an operator typing into DB
// Browser — revokes the sessions it authorised at once rather than at the next
// restart. The file store bought that with a digest check on every
// authentication; a query against a local SQLite page cache is cheaper than
// that was, so the guarantee is kept by simply not remembering anything.
type UserStore struct {
	database *db.DB
	// owned records that this store opened the database and may close it. A
	// handle passed in by a caller belongs to that caller.
	owned bool
}

// NewUserStore opens the workspace database and prepares the accounts table.
//
// NewUserStoreDB is preferred wherever the caller already has a handle: SQLite
// admits one writer, and a second *db.DB is a second connection pool queueing
// against the first for it instead of behind it in Go.
func NewUserStore(workspace string) (*UserStore, error) {
	database, err := db.Open(workspace)
	if err != nil {
		return nil, fmt.Errorf("open account database: %w", err)
	}
	store := NewUserStoreDB(database)
	store.owned = true
	if err := store.EnsureAdmin(); err != nil {
		database.Close()
		return nil, err
	}
	return store, nil
}

// Close releases the database if this store opened it. A store built with
// NewUserStoreDB does not close the caller's handle out from under it.
func (u *UserStore) Close() error {
	if !u.ready() || !u.owned {
		return nil
	}
	return u.database.Close()
}

// NewUserStoreDB wraps an already-open database. The caller keeps ownership of
// the handle and should call EnsureAdmin once at startup.
func NewUserStoreDB(database *db.DB) *UserStore {
	return &UserStore{database: database}
}

// accountColumns is the row shape scanAccount reads, in order.
const accountColumns = `id, name, role, password_hash, pin_hash, email, created_at`

// Accounts are ordered by rowid throughout: it is the order they were created
// in, which is what "the first account" means when a role has to be repaired,
// and what the file store's array order meant before it.
const orderByCreation = ` ORDER BY rowid`

// accountFilter is a WHERE clause written in this file. It is a named type, not
// a string, because accounts interpolates it into the statement: a plain string
// parameter is one careless call away from interpolating something a caller
// chose, and a clause is the one part of a query a placeholder cannot stand in
// for. The complete set is here, so a reader can check the allowlist at once.
type accountFilter string

const (
	accountByName    accountFilter = ` WHERE name = ? COLLATE NOCASE`
	accountByID      accountFilter = ` WHERE id = ?`
	accountsWithPins accountFilter = ` WHERE pin_hash != ''`
)

// accountAssignment is the SET list of an account update, under the same rule
// as accountFilter and for the same reason.
type accountAssignment string

const (
	setPasswordHash accountAssignment = `password_hash = ?`
	setPinAndEmail  accountAssignment = `pin_hash = ?, email = ?`
	clearPinHash    accountAssignment = `pin_hash = ''`
)

// maxAccountIDBytes bounds what a session cookie or a stored OAuth capability
// may aim at the accounts table.
const maxAccountIDBytes = 128

// ready reports whether there is a database to talk to. A zero-value UserStore
// is a valid thing for a caller to hold (the session limiter tests build one),
// and it must read as an empty account set rather than panic.
func (u *UserStore) ready() bool { return u != nil && u.database != nil }

type rowScanner interface {
	Scan(dest ...any) error
}

func scanAccount(row rowScanner) (UserRecord, error) {
	var user UserRecord
	var role string
	if err := row.Scan(&user.ID, &user.Name, &role, &user.Hash, &user.PinHash,
		&user.Email, &user.CreatedAt); err != nil {
		return UserRecord{}, err
	}
	// The schema constrains this column, but constraints are only checked on
	// write: a hand-edited row can hold anything, and a role nobody can
	// interpret must fail closed rather than default to some authority.
	switch identity.Role(role) {
	case identity.RoleAdmin, identity.RoleMember:
		user.Role = identity.Role(role)
	default:
		return UserRecord{}, fmt.Errorf("user %q has invalid role %q", user.Name, role)
	}
	return user, nil
}

// accounts reads matching rows, newest last. The limit is required rather than
// optional: every caller here knows how many rows it can use, and a SELECT with
// no ceiling is one hand-edited table away from loading a workspace's whole
// account list into a login path.
func (u *UserStore) accounts(ctx context.Context, where accountFilter, limit int, args ...any) ([]UserRecord, error) {
	if !u.ready() {
		return nil, nil
	}
	if limit <= 0 {
		// SQLite reads a negative LIMIT as no limit at all, which is the exact
		// opposite of what anyone passing one would mean. Fail closed: a caller
		// that asked for no rows gets none rather than the whole table.
		return nil, nil
	}
	args = append(args, limit)
	rows, err := u.database.QueryContext(ctx,
		`SELECT `+accountColumns+` FROM accounts`+string(where)+orderByCreation+` LIMIT ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("read accounts: %w", err)
	}
	defer rows.Close()
	var users []UserRecord
	for rows.Next() {
		user, err := scanAccount(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, user)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read accounts: %w", err)
	}
	return users, nil
}

// Count reports how many accounts exist. A remote listener with none would
// accept nobody, so the server refuses to start in that state.
func (u *UserStore) Count(ctx context.Context) int {
	if !u.ready() {
		return 0
	}
	var count int
	if err := u.database.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM accounts`).Scan(&count); err != nil {
		return 0
	}
	return count
}

// List returns the account names, sorted by creation order.
func (u *UserStore) List(ctx context.Context) []string {
	if !u.ready() {
		return nil
	}
	rows, err := u.database.QueryContext(ctx, `SELECT name FROM accounts`+orderByCreation)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil
		}
		names = append(names, name)
	}
	if rows.Err() != nil {
		return nil
	}
	return names
}

// Records returns the accounts for account-management UIs and the CLI. The
// password and PIN hashes are not selected at all: an offline guessing attack
// starts with a copy of one, so they do not leave this file.
func (u *UserStore) Records(ctx context.Context) []UserRecord {
	if !u.ready() {
		return nil
	}
	rows, err := u.database.QueryContext(ctx,
		`SELECT id, name, role, email, created_at FROM accounts`+orderByCreation)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var records []UserRecord
	for rows.Next() {
		var record UserRecord
		var role string
		if err := rows.Scan(&record.ID, &record.Name, &role, &record.Email, &record.CreatedAt); err != nil {
			return nil
		}
		record.Role = identity.Role(role)
		records = append(records, record)
	}
	if rows.Err() != nil {
		return nil
	}
	return records
}

// Add creates an account. Names are unique case-insensitively, because a
// Login prompt that distinguishes "Ann" from "ann" only creates lockouts.
func (u *UserStore) Add(ctx context.Context, name, password string) error {
	return u.AddWithRole(ctx, name, password, "")
}

// AddWithRole creates an account with an explicit role. An empty role chooses
// admin for the first account and member thereafter. The first account is
// always an admin even if a caller asks for member.
func (u *UserStore) AddWithRole(ctx context.Context, name, password string, role identity.Role) error {
	name = strings.TrimSpace(name)
	if !userNamePattern.MatchString(name) {
		return fmt.Errorf("invalid user name %q", name)
	}
	if err := validateNewPassword(password); err != nil {
		return err
	}
	if role != "" && role != identity.RoleAdmin && role != identity.RoleMember {
		return fmt.Errorf("invalid role %q", role)
	}
	hash, err := hashPassword(password)
	if err != nil {
		return err
	}
	id, err := RandomToken()
	if err != nil {
		return err
	}
	if !u.ready() {
		return errors.New("no account database")
	}

	return u.database.Tx(ctx, func(tx *sql.Tx) error {
		// Counting and inserting in one transaction is what makes "the first
		// account is an admin" true when two are created at once.
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM accounts`).Scan(&count); err != nil {
			return fmt.Errorf("count accounts: %w", err)
		}
		var exists int
		err := tx.QueryRowContext(ctx,
			`SELECT 1 FROM accounts WHERE name = ? COLLATE NOCASE`, name).Scan(&exists)
		if err == nil {
			return fmt.Errorf("user %q already exists", name)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("look up account: %w", err)
		}
		if count == 0 {
			role = identity.RoleAdmin
		} else if role == "" {
			role = identity.RoleMember
		}
		_, err = tx.ExecContext(ctx,
			`INSERT INTO accounts (`+accountColumns+`) VALUES (?, ?, ?, ?, '', '', ?)`,
			id[:16], name, string(role), hash, db.Now())
		if err != nil {
			return insertError(name, err)
		}
		return nil
	})
}

// insertError maps the unique index on name to the message the file store gave,
// so the CLI and the API keep saying the same thing about a duplicate. The
// index is the real check: the SELECT above loses a race that this cannot.
func insertError(name string, err error) error {
	if strings.Contains(err.Error(), "UNIQUE constraint failed") {
		return fmt.Errorf("user %q already exists", name)
	}
	return fmt.Errorf("create account: %w", err)
}

// Remove deletes an account. Session revision checks revoke its sessions on
// their next request. The final administrator cannot be removed.
func (u *UserStore) Remove(ctx context.Context, name string) error {
	if !u.ready() {
		return fmt.Errorf("user %q does not exist", name)
	}
	return u.database.Tx(ctx, func(tx *sql.Tx) error {
		var id, role string
		err := tx.QueryRowContext(ctx,
			`SELECT id, role FROM accounts WHERE name = ? COLLATE NOCASE`, name).Scan(&id, &role)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("user %q does not exist", name)
		}
		if err != nil {
			return fmt.Errorf("look up account: %w", err)
		}
		admins, err := adminCount(ctx, tx)
		if err != nil {
			return err
		}
		if identity.Role(role) == identity.RoleAdmin && admins == 1 {
			return errors.New("cannot remove the last administrator")
		}
		// Nothing else in this database points at an account row: audit_events
		// keeps actor_id as plain text on purpose, so deleting an account does
		// not delete the record of what it did.
		if _, err := tx.ExecContext(ctx, `DELETE FROM accounts WHERE id = ?`, id); err != nil {
			return fmt.Errorf("delete account: %w", err)
		}
		return nil
	})
}

func adminCount(ctx context.Context, tx *sql.Tx) (int, error) {
	var admins int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM accounts WHERE role = ?`, string(identity.RoleAdmin)).Scan(&admins); err != nil {
		return 0, fmt.Errorf("count administrators: %w", err)
	}
	return admins, nil
}

// SetPassword replaces an existing account's password.
func (u *UserStore) SetPassword(ctx context.Context, name, password string) error {
	if err := validateNewPassword(password); err != nil {
		return err
	}
	hash, err := hashPassword(password)
	if err != nil {
		return err
	}
	if err := u.updateAccount(ctx, name, setPasswordHash, hash); err != nil {
		if errors.Is(err, errNoAccount) {
			return fmt.Errorf("user %q does not exist", name)
		}
		return err
	}
	return nil
}

// errNoAccount says the name matched no row. Callers word it for their own
// audience: the account commands and the PIN commands have always phrased a
// missing account differently, and their output is documented.
var errNoAccount = errors.New("no such account")

// updateAccount writes one column set onto the named account.
func (u *UserStore) updateAccount(ctx context.Context, name string, assignments accountAssignment, args ...any) error {
	if !u.ready() {
		return errNoAccount
	}
	args = append(args, name)
	result, err := u.database.ExecContext(ctx,
		`UPDATE accounts SET `+string(assignments)+` WHERE name = ? COLLATE NOCASE`, args...)
	if err != nil {
		return fmt.Errorf("update account: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("update account: %w", err)
	}
	if changed == 0 {
		return errNoAccount
	}
	return nil
}

// SetRole changes an account's role while preserving at least one admin.
func (u *UserStore) SetRole(ctx context.Context, name string, role identity.Role) error {
	if role != identity.RoleAdmin && role != identity.RoleMember {
		return fmt.Errorf("invalid role %q", role)
	}
	if !u.ready() {
		return fmt.Errorf("user %q does not exist", name)
	}
	return u.database.Tx(ctx, func(tx *sql.Tx) error {
		var id, current string
		err := tx.QueryRowContext(ctx,
			`SELECT id, role FROM accounts WHERE name = ? COLLATE NOCASE`, name).Scan(&id, &current)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("user %q does not exist", name)
		}
		if err != nil {
			return fmt.Errorf("look up account: %w", err)
		}
		admins, err := adminCount(ctx, tx)
		if err != nil {
			return err
		}
		if identity.Role(current) == identity.RoleAdmin && role == identity.RoleMember && admins == 1 {
			return errors.New("cannot demote the last administrator")
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE accounts SET role = ? WHERE id = ?`, string(role), id); err != nil {
			return fmt.Errorf("update role: %w", err)
		}
		return nil
	})
}

// Verify checks a name/password pair. It always runs a hash comparison, so an
// unknown name and a wrong password take the same time to reject.
func (u *UserStore) Verify(ctx context.Context, name, password string) (identity.Actor, bool) {
	actor, _, ok := u.VerifyWithRevision(ctx, name, password)
	return actor, ok
}

// VerifyWithRevision returns a credential revision bound to password, name,
// and role. Sessions retain it so any account edit revokes old authority.
//
// A false answer means "not authenticated", and that covers the read failing
// as well as the password being wrong — including ctx being cancelled because
// the client hung up. The two are not distinguishable here and deliberately
// are not: adding an error return would tempt a caller into reporting which
// one happened. A caller on a request path that treats a false as evidence of
// a bad credential — charging a throttle, writing an audit line — must consult
// ctx.Err() first; see login and LoginWithOTP.
func (u *UserStore) VerifyWithRevision(ctx context.Context, name, password string) (identity.Actor, string, bool) {
	name = strings.TrimSpace(name)
	if !validLoginInput(name, password) {
		return identity.Actor{}, "", false
	}
	// One row: the name column is uniquely indexed case-insensitively, so a
	// second match cannot exist and reading one is not a shortcut.
	users, err := u.accounts(ctx, accountByName, 1, name)
	if err != nil {
		return identity.Actor{}, "", false
	}

	// An unknown name still costs one argon2 verification, so that "no such
	// account" cannot be told from "wrong password" by a stopwatch.
	hash := decoyHash
	found := UserRecord{}
	foundUser := len(users) > 0
	if foundUser {
		found = users[0]
		hash = found.Hash
	}
	ok, err := verifyPassword(hash, password)
	if err != nil || !ok || !foundUser {
		return identity.Actor{}, "", false
	}
	return identity.Actor{ID: found.ID, Name: found.Name, Role: found.Role}, userRevision(found), true
}

// Revision returns the current revision for an account ID. Failure is treated
// as absence so an unreadable or malformed account row fails closed.
func (u *UserStore) Revision(ctx context.Context, id string) (string, bool) {
	_, revision, ok := u.ActorRevision(ctx, id)
	return revision, ok
}

// ActorRevision resolves current authority and its credential revision in one
// read. OAuth capabilities use this to die with password/role/account changes
// just like sessions do.
func (u *UserStore) ActorRevision(ctx context.Context, id string) (identity.Actor, string, bool) {
	// The id arrives from a session cookie or a stored capability, which is to
	// say from a client. It is a bound parameter and cannot inject, but there is
	// no reason to carry an arbitrarily long one as far as the database.
	if id == "" || len(id) > maxAccountIDBytes {
		return identity.Actor{}, "", false
	}
	users, err := u.accounts(ctx, accountByID, 1, id)
	if err != nil || len(users) == 0 {
		return identity.Actor{}, "", false
	}
	user := users[0]
	return identity.Actor{ID: user.ID, Name: user.Name, Role: user.Role}, userRevision(user), true
}

// userRevision binds a session to the credential that opened it. The PIN and
// the passcode address are part of it: rotating either must end the sessions
// that the old ones authorised, which is the point of rotating them.
func userRevision(user UserRecord) string {
	digest := sha256.Sum256([]byte(user.ID + "\x00" + user.Name + "\x00" + user.Hash +
		"\x00" + string(user.Role) + "\x00" + user.PinHash + "\x00" + user.Email))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}

func validLoginInput(name, password string) bool {
	return userNamePattern.MatchString(name) && len(password) > 0 && len(password) <= maxLoginPasswordBytes
}

func validateNewPassword(password string) error {
	if len([]rune(password)) < minPasswordLength {
		return fmt.Errorf("password must be at least %d characters", minPasswordLength)
	}
	if len(password) > maxLoginPasswordBytes {
		return fmt.Errorf("password must be at most %d bytes", maxLoginPasswordBytes)
	}
	return nil
}

// decoyHash is compared against when the account does not exist, so a failed
// Login costs the same work either way.
var decoyHash = mustHash("nodevas-decoy-password")

func mustHash(password string) string {
	hash, err := hashPassword(password)
	if err != nil {
		panic(err)
	}
	return hash
}

func hashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	argonSlots <- struct{}{}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	<-argonSlots
	return fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

func verifyPassword(encoded, password string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, fmt.Errorf("unsupported password hash")
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, fmt.Errorf("unsupported argon2 version")
	}
	var memory, iterations uint32
	var threads uint8
	if _, err := fmt.Sscanf(
		parts[3], "m=%d,t=%d,p=%d", &memory, &iterations, &threads); err != nil {
		return false, fmt.Errorf("unsupported argon2 parameters")
	}
	if memory < 8*1024 || memory > 256*1024 || iterations < 1 || iterations > 10 || threads < 1 || threads > 16 {
		return false, fmt.Errorf("unsafe argon2 parameters")
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, err
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false, err
	}
	if len(salt) < 8 || len(salt) > 64 || len(want) < 16 || len(want) > 64 {
		return false, fmt.Errorf("unsafe argon2 hash size")
	}
	argonSlots <- struct{}{}
	// len(want) was bounded to 16..64 above, so this conversion cannot wrap.
	keyLength := uint32(len(want)) // #nosec G115
	got := argon2.IDKey([]byte(password), salt, iterations, memory, threads, keyLength)
	<-argonSlots
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}
