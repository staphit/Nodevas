package runners

import (
	"encoding/json"
	"strings"
	"sync"

	"nodevas/internal/locale"
	"nodevas/internal/secrets"
)

// AccessTokens keeps the Cloudflare Access service token runners pass the
// edge with, set once by an administrator so device links can carry it. The
// whole pair is encrypted at rest (internal/secrets); the client ID is not
// secret, but keeping both in one file means they can never disagree.
type AccessTokens struct {
	keys *secrets.Store
	mu   sync.Mutex
}

// NewAccessTokens keeps the token in keys.
func NewAccessTokens(keys *secrets.Store) *AccessTokens {
	return &AccessTokens{keys: keys}
}

type storedAccessToken struct {
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
}

// Bounds on what an administrator may store. Cloudflare's IDs and secrets are
// far shorter; these only stop the file from becoming storage.
const (
	maxAccessClientIDBytes     = 256
	maxAccessClientSecretBytes = 1024
)

func cleanAccessField(name, value string, limit int) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > limit {
		return "", locale.Errorf("%w: %s must be 1-%d bytes", "%w：%s 須為 1 到 %d 位元組", ErrInvalid, name, limit)
	}
	for _, c := range value {
		// Header values: printable ASCII without spaces.
		if c <= ' ' || c > '~' {
			return "", locale.Errorf("%w: %s must be printable ASCII without spaces", "%w：%s 須為不含空白的可列印 ASCII", ErrInvalid, name)
		}
	}
	return value, nil
}

// Get returns the stored token; empty strings when none is stored.
func (a *AccessTokens) Get() (clientID, clientSecret string, err error) {
	if a == nil || a.keys == nil {
		return "", "", nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	raw, err := a.keys.Load()
	if err != nil || raw == nil {
		return "", "", err
	}
	var stored storedAccessToken
	if err := json.Unmarshal(raw, &stored); err != nil {
		return "", "", locale.Errorf("the stored runner access token is unreadable; set it again", "已儲存的 runner 存取權杖無法讀取；請重新設定")
	}
	if stored.ClientID == "" || stored.ClientSecret == "" {
		return "", "", nil
	}
	return stored.ClientID, stored.ClientSecret, nil
}

// Set replaces the stored token.
func (a *AccessTokens) Set(clientID, clientSecret string) error {
	if a == nil || a.keys == nil {
		return locale.Errorf("this server has nowhere to keep a runner access token", "這個伺服器沒有地方保存 runner 存取權杖")
	}
	id, err := cleanAccessField("clientId", clientID, maxAccessClientIDBytes)
	if err != nil {
		return err
	}
	secret, err := cleanAccessField("clientSecret", clientSecret, maxAccessClientSecretBytes)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(storedAccessToken{ClientID: id, ClientSecret: secret})
	if err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.keys.Save(raw)
}

// Clear forgets the stored token.
func (a *AccessTokens) Clear() error {
	if a == nil || a.keys == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.keys.Clear()
}
