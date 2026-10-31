package auth

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"nodevas/internal/locale"
)

// Cloudflare Access puts the application token in this header on every
// request it forwards, and in this cookie in the browser.
const (
	AccessHeaderName = "Cf-Access-Jwt-Assertion"
	AccessCookieName = "CF_Authorization"
)

const (
	accessKeysTTL       = time.Hour
	accessRefetchPeriod = 30 * time.Second
	accessLeeway        = 60 * time.Second
	accessMaxKeysBody   = 1 << 20
)

var (
	// ErrAccessMissing means the request carried no Access token at all.
	ErrAccessMissing = locale.Errorf("no cloudflare access token", "沒有 Cloudflare Access 權杖")
	// ErrAccessInvalid covers every way a present token can fail.
	ErrAccessInvalid = locale.Errorf("invalid cloudflare access token", "Cloudflare Access 權杖無效")
	// ErrAccessAudience and ErrAccessExpired are two of those ways, named so
	// the log can tell a token for another application, or one that ran out,
	// from a forgery. Both are ErrAccessInvalid to errors.Is.
	ErrAccessAudience = locale.Errorf("%w: wrong audience", "%w：audience 不符", ErrAccessInvalid)
	ErrAccessExpired  = locale.Errorf("%w: expired", "%w：已過期", ErrAccessInvalid)
)

// AccessClaims are the parts of a verified Access token Nodevas reads. A
// person's token carries Email; a service token's carries CommonName (its
// client ID) and no email.
type AccessClaims struct {
	Email      string
	Subject    string
	Type       string
	CommonName string
	Country    string
	ExpiresAt  time.Time
}

// AccessOptions configure an AccessVerifier.
type AccessOptions struct {
	// TeamDomain is the Zero Trust team domain, "myteam.cloudflareaccess.com",
	// with or without the https:// scheme.
	TeamDomain string
	// AUD is the application audience tag from the Access application.
	AUD string
	// HTTPClient fetches the signing keys; nil uses a 10-second client.
	HTTPClient *http.Client
	// Now is the clock; nil uses time.Now.
	Now func() time.Time
}

// AccessVerifier checks Cloudflare Access application tokens against the
// team's published signing keys. It is safe for concurrent use.
type AccessVerifier struct {
	issuer   string
	certsURL string
	aud      string
	client   *http.Client
	now      func() time.Time

	mu        sync.Mutex
	keys      map[string]*rsa.PublicKey
	fetchedAt time.Time
	triedAt   time.Time
}

// NewAccessVerifier validates the options and returns a verifier. Keys are
// fetched lazily on the first token.
func NewAccessVerifier(opts AccessOptions) (*AccessVerifier, error) {
	issuer, err := normaliseTeamDomain(opts.TeamDomain)
	if err != nil {
		return nil, err
	}
	aud := strings.TrimSpace(opts.AUD)
	if aud == "" {
		return nil, locale.Errorf("cloudflare access: aud is required", "Cloudflare Access：需要 aud")
	}
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &AccessVerifier{
		issuer:   issuer,
		certsURL: issuer + "/cdn-cgi/access/certs",
		aud:      aud,
		client:   client,
		now:      now,
	}, nil
}

func normaliseTeamDomain(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", locale.Errorf("cloudflare access: team domain is required", "Cloudflare Access：需要團隊網域")
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", locale.Errorf("cloudflare access: bad team domain %q", "Cloudflare Access：團隊網域「%s」無效", raw)
	}
	if u.Scheme != "https" {
		return "", locale.Errorf("cloudflare access: team domain must be https, got %q", "Cloudflare Access：團隊網域須為 https，收到「%s」", raw)
	}
	if (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.User != nil {
		return "", locale.Errorf("cloudflare access: team domain must be a bare host, got %q", "Cloudflare Access：團隊網域須為單純的主機名稱，收到「%s」", raw)
	}
	return "https://" + strings.ToLower(u.Host), nil
}

// Issuer is the https://<team> URL tokens must name.
func (v *AccessVerifier) Issuer() string { return v.issuer }

// TokenFrom returns the Access token a request carries: the header Access
// adds when it forwards a request, else the browser cookie.
func TokenFrom(r *http.Request) string {
	if r == nil {
		return ""
	}
	if token := strings.TrimSpace(r.Header.Get(AccessHeaderName)); token != "" {
		return token
	}
	if cookie, err := r.Cookie(AccessCookieName); err == nil {
		return strings.TrimSpace(cookie.Value)
	}
	return ""
}

// VerifyRequest verifies the token a request carries.
func (v *AccessVerifier) VerifyRequest(r *http.Request) (AccessClaims, error) {
	token := TokenFrom(r)
	if token == "" {
		return AccessClaims{}, ErrAccessMissing
	}
	ctx := context.Background()
	if r != nil {
		ctx = r.Context()
	}
	return v.Verify(ctx, token)
}

type accessHeader struct {
	Alg string `json:"alg"`
	Kid string `json:"kid"`
}

type accessPayload struct {
	Aud        audience `json:"aud"`
	Iss        string   `json:"iss"`
	Sub        string   `json:"sub"`
	Email      string   `json:"email"`
	Type       string   `json:"type"`
	CommonName string   `json:"common_name"`
	Country    string   `json:"country"`
	Exp        *float64 `json:"exp"`
	Nbf        *float64 `json:"nbf"`
	Iat        *float64 `json:"iat"`
}

// audience accepts the JWT "aud" claim as a string or a list of strings.
type audience []string

func (a *audience) UnmarshalJSON(raw []byte) error {
	var one string
	if err := json.Unmarshal(raw, &one); err == nil {
		*a = audience{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(raw, &many); err != nil {
		return err
	}
	*a = many
	return nil
}

// Verify checks a compact JWS token: RS256 only, signed by a current team key,
// for this application's audience and issuer, and within its validity window.
func (v *AccessVerifier) Verify(ctx context.Context, token string) (AccessClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return AccessClaims{}, locale.Errorf("%w: malformed", "%w：格式錯誤", ErrAccessInvalid)
	}
	var header accessHeader
	if err := decodeSegment(parts[0], &header); err != nil {
		return AccessClaims{}, locale.Errorf("%w: header: %v", "%w：標頭：%v", ErrAccessInvalid, err)
	}
	// Only the algorithm Access signs with. "none" and the HMAC family would
	// let a token vouch for itself, or be keyed with the public key.
	if header.Alg != "RS256" {
		return AccessClaims{}, locale.Errorf("%w: algorithm %q not accepted", "%w：不接受演算法「%s」", ErrAccessInvalid, header.Alg)
	}
	if header.Kid == "" {
		return AccessClaims{}, locale.Errorf("%w: no key id", "%w：沒有金鑰代號", ErrAccessInvalid)
	}
	key, err := v.key(ctx, header.Kid)
	if err != nil {
		return AccessClaims{}, err
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return AccessClaims{}, locale.Errorf("%w: signature encoding", "%w：簽章編碼錯誤", ErrAccessInvalid)
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], signature); err != nil {
		return AccessClaims{}, locale.Errorf("%w: bad signature", "%w：簽章不符", ErrAccessInvalid)
	}

	var payload accessPayload
	if err := decodeSegment(parts[1], &payload); err != nil {
		return AccessClaims{}, locale.Errorf("%w: payload: %v", "%w：內容：%v", ErrAccessInvalid, err)
	}
	if strings.TrimRight(payload.Iss, "/") != v.issuer {
		return AccessClaims{}, locale.Errorf("%w: issuer %q", "%w：簽發者「%s」", ErrAccessInvalid, payload.Iss)
	}
	audOK := false
	for _, a := range payload.Aud {
		if a == v.aud {
			audOK = true
			break
		}
	}
	if !audOK {
		return AccessClaims{}, ErrAccessAudience
	}
	now := v.now()
	if payload.Exp == nil {
		return AccessClaims{}, locale.Errorf("%w: no expiry", "%w：沒有到期時間", ErrAccessInvalid)
	}
	expires := unixTime(*payload.Exp)
	if now.After(expires.Add(accessLeeway)) {
		return AccessClaims{}, ErrAccessExpired
	}
	if payload.Nbf != nil && now.Add(accessLeeway).Before(unixTime(*payload.Nbf)) {
		return AccessClaims{}, locale.Errorf("%w: not yet valid", "%w：尚未生效", ErrAccessInvalid)
	}
	if payload.Iat != nil && now.Add(accessLeeway).Before(unixTime(*payload.Iat)) {
		return AccessClaims{}, locale.Errorf("%w: issued in the future", "%w：簽發時間在未來", ErrAccessInvalid)
	}
	return AccessClaims{
		Email:      strings.TrimSpace(payload.Email),
		Subject:    payload.Sub,
		Type:       payload.Type,
		CommonName: payload.CommonName,
		Country:    payload.Country,
		ExpiresAt:  expires,
	}, nil
}

func unixTime(seconds float64) time.Time {
	whole := int64(seconds)
	return time.Unix(whole, int64((seconds-float64(whole))*1e9))
}

func decodeSegment(segment string, out any) error {
	raw, err := base64.RawURLEncoding.DecodeString(segment)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

// key returns the signing key for kid, refreshing the set when it is stale or
// when a token names a key it does not hold (Access rotates keys). Refetches
// for unknown keys are spaced so a stream of forged kids cannot turn the
// server into a fetch loop against Cloudflare.
func (v *AccessVerifier) key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	now := v.now()
	stale := v.keys == nil || now.Sub(v.fetchedAt) >= accessKeysTTL
	if !stale {
		if key, ok := v.keys[kid]; ok {
			return key, nil
		}
	}
	if stale || now.Sub(v.triedAt) >= accessRefetchPeriod {
		v.triedAt = now
		keys, err := v.fetchKeys(ctx)
		if err == nil {
			v.keys = keys
			v.fetchedAt = now
		} else if v.keys == nil {
			return nil, locale.Errorf("%w: signing keys unavailable: %v", "%w：無法取得簽章金鑰：%v", ErrAccessInvalid, err)
		}
	}
	if key, ok := v.keys[kid]; ok {
		return key, nil
	}
	return nil, locale.Errorf("%w: unknown key id", "%w：不明的金鑰代號", ErrAccessInvalid)
}

type jwkSet struct {
	Keys []struct {
		Kty string `json:"kty"`
		Kid string `json:"kid"`
		Alg string `json:"alg"`
		Use string `json:"use"`
		N   string `json:"n"`
		E   string `json:"e"`
	} `json:"keys"`
}

func (v *AccessVerifier) fetchKeys(ctx context.Context) (map[string]*rsa.PublicKey, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.certsURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := v.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", v.certsURL, resp.Status)
	}
	var set jwkSet
	if err := json.NewDecoder(io.LimitReader(resp.Body, accessMaxKeysBody)).Decode(&set); err != nil {
		return nil, locale.Errorf("decode signing keys: %w", "解讀簽章金鑰：%w", err)
	}
	keys := make(map[string]*rsa.PublicKey, len(set.Keys))
	for _, k := range set.Keys {
		if k.Kty != "RSA" || k.Kid == "" || (k.Use != "" && k.Use != "sig") || (k.Alg != "" && k.Alg != "RS256") {
			continue
		}
		n, err := base64.RawURLEncoding.DecodeString(k.N)
		if err != nil || len(n) < 256 {
			continue
		}
		e, err := base64.RawURLEncoding.DecodeString(k.E)
		if err != nil || len(e) == 0 || len(e) > 4 {
			continue
		}
		exponent := int(new(big.Int).SetBytes(e).Int64())
		if exponent < 3 {
			continue
		}
		keys[k.Kid] = &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: exponent}
	}
	if len(keys) == 0 {
		return nil, locale.Errorf("no usable RSA signing keys", "沒有可用的 RSA 簽章金鑰")
	}
	return keys, nil
}

type accessEmailKey struct{}

// WithAccessEmail records the verified Access identity on a request context,
// for the audit trail. It never signs anyone in.
func WithAccessEmail(ctx context.Context, email string) context.Context {
	return context.WithValue(ctx, accessEmailKey{}, email)
}

// AccessEmailFrom returns the verified Access identity, if any: the person's
// email, or "service:<client id>" for a service token.
func AccessEmailFrom(ctx context.Context) string {
	email, _ := ctx.Value(accessEmailKey{}).(string)
	return email
}

// DirectLoopback reports whether a request came straight from this machine:
// a loopback peer that relayed nobody else's request. A proxy on the same
// host (cloudflared beside the server) adds forwarding headers, so its
// requests do not count.
func DirectLoopback(r *http.Request) bool {
	peer, ok := remoteAddress(r)
	if !ok || !peer.IsLoopback() {
		return false
	}
	for _, h := range []string{"X-Forwarded-For", "X-Real-Ip", "Forwarded", "Cf-Connecting-Ip", AccessHeaderName} {
		if r.Header.Get(h) != "" {
			return false
		}
	}
	return true
}
