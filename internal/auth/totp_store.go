// Authenticator storage: the account_totp and account_recovery_codes tables
// (migration 0021).
//
// The shared secret is sealed with the machine key (internal/secrets) before
// it reaches the database, bound to the account it belongs to, so neither a
// copy of the database nor a row pasted onto another account yields a working
// authenticator. Recovery codes are stored as digests only; they leave the
// server once, in the response that shows them.
//
// The replay guard and the single use of a recovery code are both conditional
// UPDATEs, so they hold across processes and across a restart, not only inside
// one server's memory.

package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"math/big"
	"strings"
	"time"

	"nodevas/internal/db"
	"nodevas/internal/identity"
	"nodevas/internal/locale"
	"nodevas/internal/secrets"
)

const (
	// RecoveryCodeCount is how many single-use recovery codes an enrollment
	// hands out.
	RecoveryCodeCount = 10
	// recoveryCodeLength symbols of otpAlphabet are 80 bits: enough that the
	// stored sha256 digests are not worth brute-forcing even offline, which is
	// why they need no slow hash.
	recoveryCodeLength = 16
)

var (
	// ErrTOTPAlreadyEnabled refuses a second enrollment over a working one:
	// replacing the factor is disable (which needs a current code) and then
	// enroll.
	ErrTOTPAlreadyEnabled = locale.Errorf("two-factor authentication is already on for this account; turn it off first to enroll a new authenticator", "這個帳號已開啟雙重驗證；要登記新的驗證器請先關閉")
	// ErrTOTPNotEnabled is a disable, a recovery-code reset or a code check on
	// an account without a confirmed authenticator.
	ErrTOTPNotEnabled = locale.Errorf("two-factor authentication is not on for this account", "這個帳號沒有開啟雙重驗證")
	// ErrTOTPNoEnrollment is a confirmation with no enrollment begun.
	ErrTOTPNoEnrollment = locale.Errorf("no authenticator enrollment is in progress; start again", "沒有進行中的驗證器登記；請重新開始")
	// ErrTOTPCode is a wrong, reused or out-of-window authenticator or
	// recovery code.
	ErrTOTPCode = locale.Errorf("that authenticator code is wrong, expired or already used", "驗證器代碼錯誤、已過期或已使用")
	// ErrTOTPUnreadable means the stored secret could not be opened — the
	// machine key changed or the row was edited. It fails closed; an
	// administrator clears it with `nodevas account totp-reset`.
	ErrTOTPUnreadable = locale.Errorf("this account's authenticator secret cannot be read on this server; ask an administrator to reset two-factor authentication", "這個伺服器無法讀取這個帳號的驗證器密鑰；請管理員重設雙重驗證")
)

// TOTPState is what an account's own settings page may know about its
// authenticator. It carries no secret.
type TOTPState struct {
	Enabled bool `json:"enabled"`
	// Pending is an enrollment begun and not yet confirmed.
	Pending   bool   `json:"pending"`
	EnabledAt string `json:"enabledAt,omitempty"`
	// RecoveryCodesLeft counts the unused recovery codes.
	RecoveryCodesLeft int `json:"recoveryCodesLeft"`
}

// UseSecretKey sets the machine key file that seals authenticator secrets.
// The server passes the one beside its other secrets; without it the store
// uses secrets.MachineKeyPath, which is the same file for a default install.
// NODEVAS_SECRET_KEY, when set, overrides either.
func (u *UserStore) UseSecretKey(path string) {
	if u != nil {
		u.keyPath = path
	}
}

func (u *UserStore) secretKeyPath() (string, error) {
	if u.keyPath != "" {
		return u.keyPath, nil
	}
	return secrets.MachineKeyPath()
}

// totpAAD binds a sealed secret to its account.
func totpAAD(userID string) []byte { return []byte("nodevas-totp\x00" + userID) }

func (u *UserStore) sealTOTP(userID string, secret []byte) (string, error) {
	keyPath, err := u.secretKeyPath()
	if err != nil {
		return "", err
	}
	blob, err := secrets.Seal(keyPath, secret, totpAAD(userID))
	if err != nil {
		return "", locale.Errorf("seal authenticator secret: %w", "加密驗證器密鑰：%w", err)
	}
	return base64.StdEncoding.EncodeToString(blob), nil
}

func (u *UserStore) openTOTP(userID, sealed string) ([]byte, error) {
	keyPath, err := u.secretKeyPath()
	if err != nil {
		return nil, err
	}
	blob, err := base64.StdEncoding.DecodeString(sealed)
	if err != nil {
		return nil, ErrTOTPUnreadable
	}
	secret, err := secrets.Open(keyPath, blob, totpAAD(userID))
	if err != nil || len(secret) == 0 {
		return nil, ErrTOTPUnreadable
	}
	return secret, nil
}

// totpRow is one account_totp row.
type totpRow struct {
	sealed      string
	enabledAt   string
	lastCounter int64
}

func readTOTPRow(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, userID string) (totpRow, bool, error) {
	var row totpRow
	err := q.QueryRowContext(ctx,
		`SELECT secret, enabled_at, last_counter FROM account_totp WHERE user_id = ?`, userID).
		Scan(&row.sealed, &row.enabledAt, &row.lastCounter)
	if errors.Is(err, sql.ErrNoRows) {
		return totpRow{}, false, nil
	}
	if err != nil {
		return totpRow{}, false, locale.Errorf("read authenticator: %w", "讀取驗證器：%w", err)
	}
	return row, true, nil
}

// TOTPEnabled reports whether the account must give an authenticator code.
// A failed read is an error, never a false: a sign-in that cannot tell
// whether the second factor is required must not proceed without it.
func (u *UserStore) TOTPEnabled(ctx context.Context, userID string) (bool, error) {
	if !u.ready() || userID == "" {
		return false, nil
	}
	row, found, err := readTOTPRow(ctx, u.database, userID)
	if err != nil {
		return false, err
	}
	return found && row.enabledAt != "", nil
}

// TOTPStatus is the account's authenticator state for its settings page.
func (u *UserStore) TOTPStatus(ctx context.Context, userID string) (TOTPState, error) {
	if !u.ready() || userID == "" {
		return TOTPState{}, nil
	}
	row, found, err := readTOTPRow(ctx, u.database, userID)
	if err != nil || !found {
		return TOTPState{}, err
	}
	state := TOTPState{Enabled: row.enabledAt != "", Pending: row.enabledAt == "", EnabledAt: row.enabledAt}
	if state.Enabled {
		if err := u.database.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM account_recovery_codes WHERE user_id = ? AND used_at = ''`,
			userID).Scan(&state.RecoveryCodesLeft); err != nil {
			return TOTPState{}, locale.Errorf("count recovery codes: %w", "計算復原碼：%w", err)
		}
	}
	return state, nil
}

// beginTOTP stores a fresh, unconfirmed secret for the account and returns
// it. A previous unconfirmed one is replaced; a confirmed one is refused.
func (u *UserStore) beginTOTP(ctx context.Context, userID string) ([]byte, error) {
	if !u.ready() {
		return nil, locale.Errorf("no account database", "沒有帳號資料庫")
	}
	secret, err := newTOTPSecret()
	if err != nil {
		return nil, err
	}
	sealed, err := u.sealTOTP(userID, secret)
	if err != nil {
		return nil, err
	}
	err = u.database.Tx(ctx, func(tx *sql.Tx) error {
		row, found, err := readTOTPRow(ctx, tx, userID)
		if err != nil {
			return err
		}
		if found && row.enabledAt != "" {
			return ErrTOTPAlreadyEnabled
		}
		_, err = tx.ExecContext(ctx, `
INSERT INTO account_totp (user_id, secret, created_at, enabled_at, last_counter)
VALUES (?, ?, ?, '', 0)
ON CONFLICT (user_id) DO UPDATE SET secret = excluded.secret, created_at = excluded.created_at,
    enabled_at = '', last_counter = 0`, userID, sealed, db.Now())
		if err != nil {
			return locale.Errorf("store authenticator: %w", "儲存驗證器：%w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return secret, nil
}

// confirmTOTP turns an enrollment on with a code from the app, and returns a
// fresh set of recovery codes — the only time they exist in plain text. The
// confirming code's step becomes the replay guard's starting point, so the
// same code cannot then sign in.
func (u *UserStore) confirmTOTP(ctx context.Context, userID, code string, now time.Time) ([]string, error) {
	if !u.ready() {
		return nil, locale.Errorf("no account database", "沒有帳號資料庫")
	}
	codes, hashes, err := newRecoveryCodes(userID)
	if err != nil {
		return nil, err
	}
	err = u.database.Tx(ctx, func(tx *sql.Tx) error {
		row, found, err := readTOTPRow(ctx, tx, userID)
		if err != nil {
			return err
		}
		if !found {
			return ErrTOTPNoEnrollment
		}
		if row.enabledAt != "" {
			return ErrTOTPAlreadyEnabled
		}
		secret, err := u.openTOTP(userID, row.sealed)
		if err != nil {
			return err
		}
		counter, ok := matchTOTP(secret, code, now)
		if !ok {
			return ErrTOTPCode
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE account_totp SET enabled_at = ?, last_counter = ? WHERE user_id = ? AND enabled_at = ''`,
			db.Now(), int64(counter), userID); err != nil { // #nosec G115 -- a time step fits int64 for billions of years.
			return locale.Errorf("enable authenticator: %w", "啟用驗證器：%w", err)
		}
		return replaceRecoveryCodes(ctx, tx, userID, hashes)
	})
	if err != nil {
		return nil, err
	}
	return codes, nil
}

// verifyTOTPCode checks an authenticator code for an enabled account and, if
// it is right and from a step later than any accepted before, records that
// step. The conditional UPDATE is the replay guard: two requests racing with
// the same code cannot both see one row changed.
func (u *UserStore) verifyTOTPCode(ctx context.Context, userID, code string, now time.Time) (bool, error) {
	if !u.ready() {
		return false, ErrTOTPNotEnabled
	}
	row, found, err := readTOTPRow(ctx, u.database, userID)
	if err != nil {
		return false, err
	}
	if !found || row.enabledAt == "" {
		return false, ErrTOTPNotEnabled
	}
	secret, err := u.openTOTP(userID, row.sealed)
	if err != nil {
		return false, err
	}
	counter, ok := matchTOTP(secret, code, now)
	if !ok {
		return false, nil
	}
	step := int64(counter) // #nosec G115 -- see confirmTOTP.
	result, err := u.database.ExecContext(ctx,
		`UPDATE account_totp SET last_counter = ? WHERE user_id = ? AND enabled_at != '' AND last_counter < ?`,
		step, userID, step)
	if err != nil {
		return false, locale.Errorf("record authenticator step: %w", "記錄驗證器時間步：%w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return false, locale.Errorf("record authenticator step: %w", "記錄驗證器時間步：%w", err)
	}
	return changed == 1, nil
}

// useRecoveryCode spends one recovery code. It reports how many remain after
// it.
func (u *UserStore) useRecoveryCode(ctx context.Context, userID, code string) (int, bool, error) {
	if !u.ready() {
		return 0, false, ErrTOTPNotEnabled
	}
	normalized := normalizeOTP(code)
	if len(normalized) != recoveryCodeLength {
		return 0, false, nil
	}
	enabled, err := u.TOTPEnabled(ctx, userID)
	if err != nil {
		return 0, false, err
	}
	if !enabled {
		return 0, false, ErrTOTPNotEnabled
	}
	result, err := u.database.ExecContext(ctx,
		`UPDATE account_recovery_codes SET used_at = ? WHERE user_id = ? AND code_hash = ? AND used_at = ''`,
		db.Now(), userID, recoveryDigest(userID, normalized))
	if err != nil {
		return 0, false, locale.Errorf("spend recovery code: %w", "使用復原碼：%w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return 0, false, locale.Errorf("spend recovery code: %w", "使用復原碼：%w", err)
	}
	if changed != 1 {
		return 0, false, nil
	}
	var left int
	if err := u.database.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM account_recovery_codes WHERE user_id = ? AND used_at = ''`,
		userID).Scan(&left); err != nil {
		return 0, true, locale.Errorf("count recovery codes: %w", "計算復原碼：%w", err)
	}
	return left, true, nil
}

// regenerateRecoveryCodes replaces every recovery code, used or not.
func (u *UserStore) regenerateRecoveryCodes(ctx context.Context, userID string) ([]string, error) {
	if !u.ready() {
		return nil, ErrTOTPNotEnabled
	}
	codes, hashes, err := newRecoveryCodes(userID)
	if err != nil {
		return nil, err
	}
	err = u.database.Tx(ctx, func(tx *sql.Tx) error {
		row, found, err := readTOTPRow(ctx, tx, userID)
		if err != nil {
			return err
		}
		if !found || row.enabledAt == "" {
			return ErrTOTPNotEnabled
		}
		return replaceRecoveryCodes(ctx, tx, userID, hashes)
	})
	if err != nil {
		return nil, err
	}
	return codes, nil
}

// disableTOTP removes the account's authenticator and recovery codes. It
// reports whether there was an enabled one to remove.
func (u *UserStore) disableTOTP(ctx context.Context, userID string) (bool, error) {
	if !u.ready() {
		return false, nil
	}
	wasEnabled := false
	err := u.database.Tx(ctx, func(tx *sql.Tx) error {
		row, found, err := readTOTPRow(ctx, tx, userID)
		if err != nil {
			return err
		}
		wasEnabled = found && row.enabledAt != ""
		return deleteTOTPRows(ctx, tx, userID)
	})
	return wasEnabled, err
}

// ResetTOTP is the lockout recovery behind `nodevas account totp-reset`: it
// turns two-factor authentication off for the account named by its ID, its
// sign-in address or its name, whatever the account holder can or cannot
// prove. It runs on the server host only — there is no HTTP route to it.
//
// It reports the account and whether an authenticator was on.
func (u *UserStore) ResetTOTP(ctx context.Context, who string) (identity.Actor, bool, error) {
	who = strings.TrimSpace(who)
	if !u.ready() || who == "" || len(who) > maxEmailBytes {
		return identity.Actor{}, false, errNoAccount
	}
	var users []UserRecord
	var err error
	if strings.Contains(who, "@") {
		users, err = u.accounts(ctx, accountByEmail, 1, who)
	} else {
		users, err = u.accounts(ctx, accountByID, 1, who)
		if err == nil && len(users) == 0 {
			users, err = u.accounts(ctx, accountByName, 1, who)
		}
	}
	if err != nil {
		return identity.Actor{}, false, err
	}
	if len(users) == 0 {
		return identity.Actor{}, false, errNoAccount
	}
	found := users[0]
	actor := identity.Actor{ID: found.ID, Name: found.Name, Role: found.Role}
	wasEnabled, err := u.disableTOTP(ctx, found.ID)
	if err != nil {
		return actor, wasEnabled, err
	}
	// Whoever locked the owner out may hold a live session. The CLI cannot
	// reach the running server's memory, but it can drop the persisted copies,
	// so the next restart of the server forgets them.
	_, err = u.database.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, found.ID)
	return actor, wasEnabled, err
}

// ErrNoSuchAccountToReset is ResetTOTP's "nothing matched", for the CLI.
var ErrNoSuchAccountToReset = errNoAccount

func deleteTOTPRows(ctx context.Context, tx *sql.Tx, userID string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM account_recovery_codes WHERE user_id = ?`, userID); err != nil {
		return locale.Errorf("delete recovery codes: %w", "刪除復原碼：%w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM account_totp WHERE user_id = ?`, userID); err != nil {
		return locale.Errorf("delete authenticator: %w", "刪除驗證器：%w", err)
	}
	return nil
}

func replaceRecoveryCodes(ctx context.Context, tx *sql.Tx, userID string, hashes []string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM account_recovery_codes WHERE user_id = ?`, userID); err != nil {
		return locale.Errorf("replace recovery codes: %w", "更換復原碼：%w", err)
	}
	now := db.Now()
	for _, hash := range hashes {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO account_recovery_codes (user_id, code_hash, created_at) VALUES (?, ?, ?)`,
			userID, hash, now); err != nil {
			return locale.Errorf("store recovery code: %w", "儲存復原碼：%w", err)
		}
	}
	return nil
}

// newRecoveryCodes draws RecoveryCodeCount codes, formatted XXXX-XXXX-XXXX-XXXX
// for reading off a printout, and their digests.
func newRecoveryCodes(userID string) ([]string, []string, error) {
	limit := big.NewInt(int64(len(otpAlphabet)))
	codes := make([]string, 0, RecoveryCodeCount)
	hashes := make([]string, 0, RecoveryCodeCount)
	seen := map[string]bool{}
	for len(codes) < RecoveryCodeCount {
		raw := make([]byte, recoveryCodeLength)
		for index := range raw {
			pick, err := rand.Int(rand.Reader, limit)
			if err != nil {
				return nil, nil, err
			}
			raw[index] = otpAlphabet[pick.Int64()]
		}
		plain := string(raw)
		if seen[plain] {
			continue
		}
		seen[plain] = true
		codes = append(codes, plain[0:4]+"-"+plain[4:8]+"-"+plain[8:12]+"-"+plain[12:16])
		hashes = append(hashes, recoveryDigest(userID, plain))
	}
	return codes, hashes, nil
}

// recoveryDigest is what is stored for a recovery code: sha256 over the
// account and the normalized code, so equal codes on two accounts do not
// share a digest.
func recoveryDigest(userID, normalized string) string {
	sum := sha256.Sum256([]byte("nodevas-recovery\x00" + userID + "\x00" + normalized))
	return hex.EncodeToString(sum[:])
}
