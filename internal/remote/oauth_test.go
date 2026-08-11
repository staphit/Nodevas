package remote

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

// tokenEndpoint stands up a fake token endpoint that records the form it was
// posted, so a test can assert on what the exchange actually sent. body is
// written verbatim, which is how the malformed-response path is exercised.
func tokenEndpoint(t *testing.T, body string) (creds oauthCreds, posted *url.Values) {
	t.Helper()
	posted = &url.Values{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse token form: %v", err)
		}
		*posted = r.PostForm
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return oauthCreds{
		ClientID:     "id",
		ClientSecret: "secret",
		AuthURL:      server.URL + "/auth",
		TokenURL:     server.URL + "/token",
		Scope:        driveScope,
	}, posted
}

func okTokenBody(t *testing.T) string {
	t.Helper()
	data, err := json.Marshal(map[string]any{
		"access_token":  "a",
		"refresh_token": "r",
		"token_type":    "Bearer",
		"expires_in":    3600,
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestAuthCodeURLCarriesPKCEChallenge(t *testing.T) {
	manager := NewManager(nil)
	state, verifier, err := manager.NewOAuthState("actor-1")
	if err != nil {
		t.Fatal(err)
	}
	// The challenge must match the verifier the state entry kept, not some
	// value only the redirect knows: that entry is what the callback replays.
	pending, ok := manager.ConsumeOAuthState(state)
	if !ok {
		t.Fatal("a freshly issued state should validate")
	}
	if pending.Verifier != verifier || pending.ActorID != "actor-1" {
		t.Fatalf("state entry lost the flow context: %+v", pending)
	}
	creds, _ := tokenEndpoint(t, okTokenBody(t))
	parsed, err := url.Parse(creds.AuthCodeURL("http://127.0.0.1:8080/cb", state, verifier))
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	if query.Get("code_challenge_method") != "S256" {
		t.Fatalf("code_challenge_method = %q, want S256", query.Get("code_challenge_method"))
	}
	sum := sha256.Sum256([]byte(pending.Verifier))
	want := base64.RawURLEncoding.EncodeToString(sum[:])
	if query.Get("code_challenge") != want {
		t.Fatalf("code_challenge = %q, want %q", query.Get("code_challenge"), want)
	}
	if strings.Contains(parsed.RawQuery, url.QueryEscape(verifier)) {
		t.Fatal("the verifier itself must not travel in the redirect")
	}
}

func TestExchangeAuthCodePostsCodeVerifier(t *testing.T) {
	manager := NewManager(nil)
	state, verifier, err := manager.NewOAuthState("actor-1")
	if err != nil {
		t.Fatal(err)
	}
	creds, posted := tokenEndpoint(t, okTokenBody(t))
	challenge := url.Values{}
	if parsed, err := url.Parse(creds.AuthCodeURL("http://127.0.0.1:8080/cb", state, verifier)); err == nil {
		challenge = parsed.Query()
	} else {
		t.Fatal(err)
	}
	if _, err := ExchangeAuthCode(
		context.Background(), http.DefaultClient, creds, "the-code",
		"http://127.0.0.1:8080/cb", verifier,
	); err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if got := posted.Get("code_verifier"); got != verifier {
		t.Fatalf("code_verifier = %q, want %q", got, verifier)
	}
	sum := sha256.Sum256([]byte(posted.Get("code_verifier")))
	if want := base64.RawURLEncoding.EncodeToString(sum[:]); challenge.Get("code_challenge") != want {
		t.Fatalf("posted verifier does not match the challenge %q", challenge.Get("code_challenge"))
	}
}

func TestOAuthStateIsSingleUseAndExpires(t *testing.T) {
	manager := NewManager(nil)
	state, _, err := manager.NewOAuthState("actor-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := manager.ConsumeOAuthState(state); !ok {
		t.Fatal("a freshly issued state should validate")
	}
	if _, ok := manager.ConsumeOAuthState(state); ok {
		t.Fatal("a state must not validate twice")
	}
	if _, ok := manager.ConsumeOAuthState("never-issued"); ok {
		t.Fatal("an unknown state must not validate")
	}
	stale, _, err := manager.NewOAuthState("actor-1")
	if err != nil {
		t.Fatal(err)
	}
	entry := manager.states[stale]
	entry.Issued = time.Now().Add(-oauthStateTTL - time.Minute)
	manager.states[stale] = entry
	if _, ok := manager.ConsumeOAuthState(stale); ok {
		t.Fatal("a state past its TTL must not validate")
	}
}

func TestOAuthStateLimitsPerActorAndGlobally(t *testing.T) {
	manager := NewManager(nil)
	for i := 0; i < maxOAuthStatesPerActor; i++ {
		if _, _, err := manager.NewOAuthState("actor-1"); err != nil {
			t.Fatalf("state %d: %v", i, err)
		}
	}
	if _, _, err := manager.NewOAuthState("actor-1"); !errors.Is(err, ErrOAuthStateLimit) {
		t.Fatalf("per-actor limit returned %v", err)
	}
	if _, _, err := manager.NewOAuthState("actor-2"); err != nil {
		t.Fatalf("one actor must not consume another actor's allowance: %v", err)
	}

	global := NewManager(nil)
	for i := 0; i < maxOAuthStatesTotal; i++ {
		actor := fmt.Sprintf("actor-%d", i/maxOAuthStatesPerActor)
		if _, _, err := global.NewOAuthState(actor); err != nil {
			t.Fatalf("global state %d: %v", i, err)
		}
	}
	if _, _, err := global.NewOAuthState("one-more-actor"); !errors.Is(err, ErrOAuthStateLimit) {
		t.Fatalf("global limit returned %v", err)
	}
}

func TestMalformedTokenResponseKeepsBodyOutOfTheError(t *testing.T) {
	// A 200 that fails to parse can still be carrying a token; the error from
	// it reaches the log, so it must not quote what came back.
	creds, _ := tokenEndpoint(t, `{"access_token": "leaked-token-value"`)
	_, err := ExchangeAuthCode(
		context.Background(), http.DefaultClient, creds, "the-code",
		"http://127.0.0.1:8080/cb", "verifier",
	)
	if err == nil {
		t.Fatal("a malformed token response should be an error")
	}
	if strings.Contains(err.Error(), "leaked-token-value") || strings.Contains(err.Error(), "access_token") {
		t.Fatalf("token endpoint body reached the error: %v", err)
	}
}

func TestDriveTokenEncryptedAtRest(t *testing.T) {
	catalog := t.TempDir()
	st := NewDriveTokenStore(catalog, "/some/workspace")
	tok := DriveToken{AccessToken: "access-secret", RefreshToken: "refresh-secret-xyz"}
	if err := st.Save(tok); err != nil {
		t.Fatalf("save: %v", err)
	}
	raw, err := os.ReadFile(st.Path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("refresh-secret-xyz")) || bytes.Contains(raw, []byte("access-secret")) {
		t.Fatal("token stored in the clear")
	}
	loaded, err := st.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.RefreshToken != tok.RefreshToken || loaded.AccessToken != tok.AccessToken {
		t.Fatal("round-trip lost data")
	}
	if !st.Connected() {
		t.Fatal("connected() should report a stored refresh token")
	}
}

func TestDriveClientCredentialsEncryptedAtRest(t *testing.T) {
	store := NewDriveCredentialStore(t.TempDir())
	clientID := "123456789.apps.googleusercontent.com"
	clientSecret := "client-secret-value"
	if err := store.Save(clientID, clientSecret); err != nil {
		t.Fatalf("save: %v", err)
	}
	raw, err := os.ReadFile(store.Path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(clientID)) || bytes.Contains(raw, []byte(clientSecret)) {
		t.Fatal("OAuth client credentials stored in the clear")
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.ClientID != clientID || loaded.ClientSecret != clientSecret {
		t.Fatalf("round-trip lost credentials: %+v", loaded)
	}
	if !store.Configured() {
		t.Fatal("configured() should report stored credentials")
	}
	if err := store.Clear(); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if store.Configured() {
		t.Fatal("clear() left credentials configured")
	}
}

func TestDriveTokenUsesEnvKey(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	t.Setenv("NODEVAS_SECRET_KEY", base64.StdEncoding.EncodeToString(key))
	st := NewDriveTokenStore(t.TempDir(), "ws")
	if err := st.Save(DriveToken{RefreshToken: "r"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	// No master.key file is written when the env key is used.
	if _, err := os.Stat(st.keyPath); !os.IsNotExist(err) {
		t.Fatalf("env key should not spill a key file: %v", err)
	}
	if tok, err := st.Load(); err != nil || tok.RefreshToken != "r" {
		t.Fatalf("load with env key: %+v %v", tok, err)
	}
}

func TestDriveBrowseReadyRequiresReadScope(t *testing.T) {
	store := NewDriveTokenStore(t.TempDir(), "workspace")
	if err := store.Save(DriveToken{RefreshToken: "file-only", Scope: driveFileScope}); err != nil {
		t.Fatal(err)
	}
	if store.BrowseReady() {
		t.Fatal("a file-only Drive token must not browse existing Drive files")
	}
	if err := store.Save(DriveToken{RefreshToken: "current", Scope: driveScope}); err != nil {
		t.Fatal(err)
	}
	if !store.BrowseReady() {
		t.Fatal("current Drive token should browse folders and bundles")
	}
}
