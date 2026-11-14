// Package prefs keeps each person's app preferences on the hub (ux-design
// §3), so a choice made in one browser, at one address or in the desktop
// app follows them to the others: theme, language, layouts and the like.
//
// The hub does not know what a preference means. A key is a name the web
// app's schema chooses and a value one JSON value it encoded, so a new
// preference needs no change here. What the hub does is bound them, per
// value and per account, so the table never turns into storage, and keep
// one account's apart from another's. On a hub without accounts the person
// at the keyboard is the account "local" (identity.Local).
package prefs

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"maps"
	"regexp"
	"slices"
	"time"
	"unicode/utf8"

	"nodevas/internal/db"
	"nodevas/internal/locale"
)

// The limits. A value is counted as the compact JSON the hub keeps, an
// account's size as its keys and values together.
const (
	MaxKeyBytes        = 64
	MaxValueBytes      = 4 << 10
	MaxKeysPerChange   = 64
	MaxKeysPerAccount  = 200
	MaxBytesPerAccount = 128 << 10
)

// keyPattern is a preference's name: a letter, then letters, digits, dots,
// dashes or underscores, MaxKeyBytes at most.
var keyPattern = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9._-]{0,63}$`)

// ValidKey reports whether key may name a preference.
func ValidKey(key string) bool { return keyPattern.MatchString(key) }

// stampLayout is updated_at: the hub's clock in UTC to the microsecond, at
// a fixed width so the stamps sort as text.
const stampLayout = "2006-01-02T15:04:05.000000Z"

// Snapshot is an account's preferences: each key's value, and when the hub
// last took a change to it.
type Snapshot struct {
	Values    map[string]json.RawMessage `json:"values"`
	UpdatedAt map[string]string          `json:"updatedAt"`
}

// Change is one batch of writes. Set gives keys their new values (a JSON
// null removes the key, as Remove does); Remove names keys to drop. A key
// may appear once in all.
type Change struct {
	Set    map[string]json.RawMessage `json:"set,omitempty"`
	Remove []string                   `json:"remove,omitempty"`
}

// RefusedError is a change the store would not apply. Full is true when
// it would take the account past its limits, false for a malformed change
// (a bad key, a value too long or not JSON, too many keys at once).
type RefusedError struct {
	Full bool
	err  error
}

func (e *RefusedError) Error() string { return e.err.Error() }

// Unwrap is the sentence itself, so a handler renders it in its reader's
// language.
func (e *RefusedError) Unwrap() error { return e.err }

func invalid(english, chinese string, args ...any) error {
	return &RefusedError{err: locale.Errorf(english, chinese, args...)}
}

// Store reads and writes account_preferences.
type Store struct {
	database *db.DB
	clock    func() time.Time
}

// NewStore is a store on database.
func NewStore(database *db.DB) *Store {
	return &Store{database: database, clock: time.Now}
}

var errNoAccount = locale.Errorf("preferences belong to an account; none was given", "偏好設定屬於帳號，但沒有指定帳號")

// Get is accountID's preferences. An account that has none gets empty maps.
func (s *Store) Get(ctx context.Context, accountID string) (Snapshot, error) {
	if accountID == "" {
		return Snapshot{}, errNoAccount
	}
	rows, err := s.database.QueryContext(ctx,
		`SELECT key, value, updated_at FROM account_preferences WHERE account_id = ? ORDER BY key`, accountID)
	if err != nil {
		return Snapshot{}, locale.Errorf("read preferences: %w", "讀取偏好設定：%w", err)
	}
	return scan(rows)
}

// scan reads key, value, updated_at rows into a snapshot. A value that is
// not JSON (written by something other than this store) is left out
// rather than breaking the answer it would be part of.
func scan(rows *sql.Rows) (Snapshot, error) {
	defer rows.Close()
	out := Snapshot{Values: map[string]json.RawMessage{}, UpdatedAt: map[string]string{}}
	for rows.Next() {
		var key, value, at string
		if err := rows.Scan(&key, &value, &at); err != nil {
			return Snapshot{}, locale.Errorf("read preferences: %w", "讀取偏好設定：%w", err)
		}
		if !json.Valid([]byte(value)) {
			continue
		}
		out.Values[key] = json.RawMessage(value)
		out.UpdatedAt[key] = at
	}
	if err := rows.Err(); err != nil {
		return Snapshot{}, locale.Errorf("read preferences: %w", "讀取偏好設定：%w", err)
	}
	return out, nil
}

// normalized is a checked change: each value compact, removals sorted.
type normalized struct {
	set    map[string][]byte
	remove []string
}

// check refuses a malformed change and makes the rest compact.
func check(change Change) (normalized, error) {
	out := normalized{set: map[string][]byte{}}
	if len(change.Set)+len(change.Remove) > MaxKeysPerChange {
		return normalized{}, invalid("one change may name at most %d preferences", "一次變更最多只能有 %d 個偏好設定", MaxKeysPerChange)
	}
	seen := map[string]bool{}
	name := func(key string) error {
		if !ValidKey(key) {
			return invalid("%q is not a preference name (a letter, then letters, digits, . _ or -, at most %d)",
				"「%s」不是偏好設定名稱（字母開頭，之後是字母、數字、. _ 或 -，最多 %d 個字元）", clip(key), MaxKeyBytes)
		}
		if seen[key] {
			return invalid("preference %q is named twice in one change", "偏好設定「%s」在一次變更中出現兩次", key)
		}
		seen[key] = true
		return nil
	}
	for _, key := range slices.Sorted(maps.Keys(change.Set)) {
		if err := name(key); err != nil {
			return normalized{}, err
		}
		raw := bytes.TrimSpace(change.Set[key])
		if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
			out.remove = append(out.remove, key)
			continue
		}
		var compact bytes.Buffer
		if err := json.Compact(&compact, raw); err != nil {
			return normalized{}, invalid("preference %q is not one JSON value", "偏好設定「%s」不是一個 JSON 值", key)
		}
		// JSON's own scanner lets any byte through inside a string; the
		// database takes UTF-8 only.
		if !utf8.Valid(compact.Bytes()) {
			return normalized{}, invalid("preference %q is not valid UTF-8", "偏好設定「%s」不是有效的 UTF-8", key)
		}
		if compact.Len() > MaxValueBytes {
			return normalized{}, invalid("preference %q is longer than %d bytes", "偏好設定「%s」超過 %d 位元組", key, MaxValueBytes)
		}
		out.set[key] = compact.Bytes()
	}
	for _, key := range change.Remove {
		if err := name(key); err != nil {
			return normalized{}, err
		}
		out.remove = append(out.remove, key)
	}
	slices.Sort(out.remove)
	return out, nil
}

// clip keeps a refused key short in the sentence that names it.
func clip(key string) string {
	if len(key) <= MaxKeyBytes {
		return key
	}
	return key[:MaxKeyBytes] + "…"
}

// Apply writes change for accountID in one transaction and answers the
// account's preferences after it. Changes are applied in the order the hub
// takes them, so of two writes to one key the later one stays (last write
// wins, by the hub's clock, never a client's). A value set to what it
// already is keeps its updated_at. A change that would take the account
// past MaxKeysPerAccount or MaxBytesPerAccount is refused whole (a
// RefusedError with Full); one that only shrinks an account already past
// them goes through.
func (s *Store) Apply(ctx context.Context, accountID string, change Change) (Snapshot, error) {
	if accountID == "" {
		return Snapshot{}, errNoAccount
	}
	checked, err := check(change)
	if err != nil {
		return Snapshot{}, err
	}
	now := s.clock().UTC().Format(stampLayout)
	var out Snapshot
	err = s.database.Tx(ctx, func(tx *sql.Tx) error {
		keysBefore, bytesBefore, err := size(ctx, tx, accountID)
		if err != nil {
			return err
		}
		for _, key := range checked.remove {
			if _, err := tx.ExecContext(ctx,
				`DELETE FROM account_preferences WHERE account_id = ? AND key = ?`, accountID, key); err != nil {
				return locale.Errorf("remove preference: %w", "移除偏好設定：%w", err)
			}
		}
		for _, key := range slices.Sorted(maps.Keys(checked.set)) {
			if _, err := tx.ExecContext(ctx, `
INSERT INTO account_preferences (account_id, key, value, updated_at) VALUES (?, ?, ?, ?)
ON CONFLICT (account_id, key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at
WHERE account_preferences.value <> excluded.value`,
				accountID, key, string(checked.set[key]), now); err != nil {
				return locale.Errorf("save preference: %w", "儲存偏好設定：%w", err)
			}
		}
		keysAfter, bytesAfter, err := size(ctx, tx, accountID)
		if err != nil {
			return err
		}
		if (keysAfter > MaxKeysPerAccount && keysAfter > keysBefore) ||
			(bytesAfter > MaxBytesPerAccount && bytesAfter > bytesBefore) {
			return &RefusedError{Full: true, err: locale.Errorf(
				"preferences are limited to %d keys and %d KiB per account; this change would pass that",
				"每個帳號的偏好設定最多 %d 個、%d KiB；這次變更會超過", MaxKeysPerAccount, MaxBytesPerAccount>>10)}
		}
		rows, err := tx.QueryContext(ctx,
			`SELECT key, value, updated_at FROM account_preferences WHERE account_id = ? ORDER BY key`, accountID)
		if err != nil {
			return locale.Errorf("read preferences: %w", "讀取偏好設定：%w", err)
		}
		out, err = scan(rows)
		return err
	})
	if err != nil {
		return Snapshot{}, err
	}
	return out, nil
}

// size is how many preferences accountID has and how many bytes their
// keys and values take.
func size(ctx context.Context, tx *sql.Tx, accountID string) (int, int, error) {
	var keys, total int
	err := tx.QueryRowContext(ctx, `
SELECT count(*), COALESCE(sum(octet_length(key) + octet_length(value)), 0)
FROM account_preferences WHERE account_id = ?`, accountID).Scan(&keys, &total)
	if err != nil {
		return 0, 0, locale.Errorf("count preferences: %w", "計算偏好設定：%w", err)
	}
	return keys, total, nil
}

// DeleteAccount removes accountID's preferences in tx: the account removal
// path calls it beside deleting the account, so an account made later with
// the same id never starts with somebody else's choices.
func DeleteAccount(ctx context.Context, tx *sql.Tx, accountID string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM account_preferences WHERE account_id = ?`, accountID); err != nil {
		return locale.Errorf("delete the account's preferences: %w", "刪除帳號的偏好設定：%w", err)
	}
	return nil
}
