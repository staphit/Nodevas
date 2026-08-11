package remote

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"nodevas/internal/secrets"
	"path/filepath"
	"strings"
	"time"
)

// Google Drive is reached with an OAuth 2.0 authorization-code flow. The token
// is stored encrypted on the server (plan.md P3: "token 加密存伺服器端"); it never
// travels to the browser. Nodevas uses drive.metadata.readonly to browse
// folders, plus drive.file to read and write its own backup bundles.

const (
	driveFileScope     = "https://www.googleapis.com/auth/drive.file"
	driveMetadataScope = "https://www.googleapis.com/auth/drive.metadata.readonly"
	driveScope         = driveFileScope + " " + driveMetadataScope
)

// oauthCreds is an OAuth client plus its endpoints. The endpoints are fields
// rather than constants so tests can point the flow at an httptest server.
type oauthCreds struct {
	ClientID     string
	ClientSecret string
	AuthURL      string
	TokenURL     string
	Scope        string
}

func newOAuthCreds(id, secret string) (oauthCreds, error) {
	if id == "" || secret == "" {
		return oauthCreds{}, fmt.Errorf("Google Drive OAuth client credentials are not configured")
	}
	return oauthCreds{
		ClientID:     id,
		ClientSecret: secret,
		AuthURL:      "https://accounts.google.com/o/oauth2/v2/auth",
		TokenURL:     "https://oauth2.googleapis.com/token",
		Scope:        driveScope,
	}, nil
}

type driveClientConfig struct {
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
}

// driveCredentialStore keeps the OAuth application credential out of the
// workspace and repository. It is encrypted with the same machine-local
// master key used for the refresh token, and only the server reads it.
type driveCredentialStore struct {
	Path    string
	keyPath string
	store   *secrets.Store
}

func NewDriveCredentialStore(catalogRoot string) *driveCredentialStore {
	secretDir := filepath.Join(catalogRoot, "secrets")
	path := filepath.Join(secretDir, "drive-client.enc")
	keyPath := filepath.Join(secretDir, "master.key")
	return &driveCredentialStore{
		Path:    path,
		keyPath: keyPath,
		store:   secrets.New(path, keyPath),
	}
}

func (s *driveCredentialStore) Load() (oauthCreds, error) {
	plain, err := s.store.Load()
	if err != nil {
		return oauthCreds{}, fmt.Errorf("decrypt Drive client credentials: %w", err)
	}
	if len(plain) == 0 {
		return oauthCreds{}, nil
	}
	var stored driveClientConfig
	if err := json.Unmarshal(plain, &stored); err != nil {
		return oauthCreds{}, err
	}
	return newOAuthCreds(stored.ClientID, stored.ClientSecret)
}

func (s *driveCredentialStore) Configured() bool {
	creds, err := s.Load()
	return err == nil && creds.ClientID != "" && creds.ClientSecret != ""
}

func (s *driveCredentialStore) Save(clientID, clientSecret string) error {
	creds, err := newOAuthCreds(clientID, clientSecret)
	if err != nil {
		return err
	}
	plain, err := json.Marshal(driveClientConfig{
		ClientID:     creds.ClientID,
		ClientSecret: creds.ClientSecret,
	})
	if err != nil {
		return err
	}
	return s.store.Save(plain)
}

func (s *driveCredentialStore) Clear() error {
	return s.store.Clear()
}

// pkceChallenge is the S256 form of a verifier: the only part of the PKCE pair
// that is safe to put in a redirect the browser can read.
func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// AuthCodeURL builds the consent URL the browser is sent to. offline access +
// consent prompt are what make Google return a refresh token.
//
// PKCE is not optional here. README tells users to register a "Desktop app"
// client with a 127.0.0.1 callback, so the client secret is shipped or typed
// locally and the loopback port can be claimed by any other process on the
// machine (RFC 8252 §8.1). The verifier never leaves this server, so it — not
// the secret — is what proves the code is being redeemed by whoever asked for
// it.
func (c oauthCreds) AuthCodeURL(redirectURI, state, verifier string) string {
	q := url.Values{
		"client_id":             {c.ClientID},
		"redirect_uri":          {redirectURI},
		"response_type":         {"code"},
		"scope":                 {c.Scope},
		"access_type":           {"offline"},
		"prompt":                {"consent"},
		"state":                 {state},
		"code_challenge":        {pkceChallenge(verifier)},
		"code_challenge_method": {"S256"},
	}
	return c.AuthURL + "?" + q.Encode()
}

// DriveToken is the stored credential. Expiry is absolute so a restarted server
// still knows when to refresh.
type DriveToken struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	TokenType    string    `json:"token_type"`
	Scope        string    `json:"scope,omitempty"`
	Expiry       time.Time `json:"expiry"`
	// OwnerID is the actor that completed the consent flow. The token file is
	// named after the workspace alone, so on a networked server this is what
	// says whose Drive account the workspace is linked to; it stays empty on a
	// loopback server, where there is only one actor.
	OwnerID string `json:"ownerId,omitempty"`
}

func (t DriveToken) fresh() bool {
	// 60s of slack so a token does not expire mid-request.
	return t.AccessToken != "" && time.Now().Before(t.Expiry.Add(-60*time.Second))
}

func (t DriveToken) canBrowse() bool {
	for _, scope := range strings.Fields(t.Scope) {
		if scope == driveMetadataScope || scope == "https://www.googleapis.com/auth/drive" {
			return true
		}
	}
	return false
}

// tokenResponse is Google's token endpoint payload.
type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	Scope        string `json:"scope"`
	ExpiresIn    int64  `json:"expires_in"`
	Error        string `json:"error"`
	ErrorDesc    string `json:"error_description"`
}

func postTokenForm(
	ctx context.Context, client *http.Client, tokenURL string, form url.Values,
) (DriveToken, error) {
	req, err := http.NewRequestWithContext(
		ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return DriveToken{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return DriveToken{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return DriveToken{}, err
	}
	var parsed tokenResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		// The body is deliberately not quoted: a 5xx from here is logged in
		// full, and a response that failed to parse may still have carried a
		// token. The status is the actionable part.
		return DriveToken{}, fmt.Errorf("token endpoint returned %d with an unreadable body", resp.StatusCode)
	}
	if parsed.Error != "" {
		return DriveToken{}, fmt.Errorf("token endpoint: %s %s", parsed.Error, parsed.ErrorDesc)
	}
	if resp.StatusCode != http.StatusOK || parsed.AccessToken == "" {
		return DriveToken{}, fmt.Errorf("token endpoint returned %d", resp.StatusCode)
	}
	return DriveToken{
		AccessToken:  parsed.AccessToken,
		RefreshToken: parsed.RefreshToken,
		TokenType:    parsed.TokenType,
		Scope:        parsed.Scope,
		Expiry:       time.Now().Add(time.Duration(parsed.ExpiresIn) * time.Second),
	}, nil
}

// ExchangeAuthCode trades an authorization code for the first token pair. The
// verifier is the other half of the PKCE pair AuthCodeURL committed to; without
// it an intercepted code is useless.
func ExchangeAuthCode(
	ctx context.Context, client *http.Client, creds oauthCreds, code, redirectURI, verifier string,
) (DriveToken, error) {
	tok, err := postTokenForm(ctx, client, creds.TokenURL, url.Values{
		"code":          {code},
		"client_id":     {creds.ClientID},
		"client_secret": {creds.ClientSecret},
		"redirect_uri":  {redirectURI},
		"grant_type":    {"authorization_code"},
		"code_verifier": {verifier},
	})
	if err != nil {
		return DriveToken{}, err
	}
	if tok.Scope == "" {
		tok.Scope = creds.Scope
	}
	return tok, nil
}

// refreshAccessToken renews an access token. Google omits the refresh token on
// refresh, so the caller keeps the existing one.
func refreshAccessToken(
	ctx context.Context, client *http.Client, creds oauthCreds, refreshToken string,
) (DriveToken, error) {
	tok, err := postTokenForm(ctx, client, creds.TokenURL, url.Values{
		"client_id":     {creds.ClientID},
		"client_secret": {creds.ClientSecret},
		"refresh_token": {refreshToken},
		"grant_type":    {"refresh_token"},
	})
	if err != nil {
		return DriveToken{}, err
	}
	if tok.RefreshToken == "" {
		tok.RefreshToken = refreshToken
	}
	return tok, nil
}

// driveTokenStore keeps the workspace's token encrypted at rest with AES-GCM.
// The master key is a machine-local file (or NODEVAS_SECRET_KEY), so the token
// is never readable from a plain file read, a stray backup, or a synced folder.
type driveTokenStore struct {
	Path    string
	keyPath string
	store   *secrets.Store
}

func NewDriveTokenStore(catalogRoot, workspace string) *driveTokenStore {
	sum := sha256.Sum256([]byte(filepath.Clean(workspace)))
	name := "drive-" + hex.EncodeToString(sum[:])[:16] + ".enc"
	secretDir := filepath.Join(catalogRoot, "secrets")
	path := filepath.Join(secretDir, name)
	keyPath := filepath.Join(secretDir, "master.key")
	return &driveTokenStore{
		Path:    path,
		keyPath: keyPath,
		store:   secrets.New(path, keyPath),
	}
}

func (s *driveTokenStore) Connected() bool {
	tok, err := s.Load()
	return err == nil && tok.RefreshToken != ""
}

// BrowseReady reports whether the connected token has the scope required to
// browse and download existing Drive bundles.
func (s *driveTokenStore) BrowseReady() bool {
	tok, err := s.Load()
	return err == nil && tok.RefreshToken != "" && tok.canBrowse()
}

func (s *driveTokenStore) Load() (DriveToken, error) {
	plain, err := s.store.Load()
	if err != nil {
		return DriveToken{}, fmt.Errorf("decrypt Drive token: %w", err)
	}
	if len(plain) == 0 {
		return DriveToken{}, nil
	}
	var tok DriveToken
	if err := json.Unmarshal(plain, &tok); err != nil {
		return DriveToken{}, err
	}
	return tok, nil
}

func (s *driveTokenStore) Save(tok DriveToken) error {
	// A refresh rebuilds the token from the token-endpoint response alone,
	// which knows nothing about who linked the account. Carry the recorded
	// owner across so a routine refresh cannot quietly unbind the workspace.
	if tok.OwnerID == "" {
		if existing, err := s.Load(); err == nil {
			tok.OwnerID = existing.OwnerID
		}
	}
	plain, err := json.Marshal(tok)
	if err != nil {
		return err
	}
	return s.store.Save(plain)
}

func (s *driveTokenStore) Clear() error {
	return s.store.Clear()
}
