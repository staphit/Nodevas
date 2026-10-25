package runnerwire

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
)

// Linking a new device: a signed-in person who just stepped up asks the hub
// for a one-time link string (POST /api/runners/links), carries it to the new
// machine, and `nodevas runner login --link` redeems it (POST LinkPath) for a
// runner token. The link string is a credential: it holds the one-time code
// and, when an administrator stored one, the Cloudflare Access service token
// runners pass the edge with. Access is never bypassed; the CLI reaches
// LinkPath through the edge with that service token like every other runner
// request.

// LinkPath is where a link code is redeemed. It is under the runner prefix
// but takes no runner token: the code is the credential.
const LinkPath = "/api/runner/link"

// LinkPrefix marks a link string and its format version.
const LinkPrefix = "nvlink1."

// LinkTTLSeconds is how long a link code stays redeemable.
const LinkTTLSeconds = 600

// MaxLinksPerUser bounds the codes one person may have outstanding.
const MaxLinksPerUser = 5

// MaxLinkBytes bounds a link string, so a pasted file is refused before it is
// decoded.
const MaxLinkBytes = 4 << 10

// LinkPayload is what a link string carries, base64url JSON after LinkPrefix.
type LinkPayload struct {
	// Hub is the hub's base URL as the browser that made the link reached it.
	Hub string `json:"hub"`
	// Code is the one-time code: 32 random bytes, base64url.
	Code string `json:"code"`
	// AccessClientID and AccessClientSecret are the Cloudflare Access service
	// token for runners, present only when an administrator stored one.
	AccessClientID     string `json:"accessClientId,omitempty"`
	AccessClientSecret string `json:"accessClientSecret,omitempty"`
}

// ErrLinkFormat is a string that is not a link this version understands.
var ErrLinkFormat = errors.New("that is not a Nodevas device link (it starts with " + LinkPrefix + ")")

// EncodeLink renders a payload as a link string.
func EncodeLink(p LinkPayload) (string, error) {
	raw, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	return LinkPrefix + base64.RawURLEncoding.EncodeToString(raw), nil
}

// DecodeLink parses a link string. It checks the shape only; the hub URL is
// the caller's to validate.
func DecodeLink(link string) (LinkPayload, error) {
	link = strings.TrimSpace(link)
	if len(link) > MaxLinkBytes || !strings.HasPrefix(link, LinkPrefix) {
		return LinkPayload{}, ErrLinkFormat
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(link[len(LinkPrefix):], "="))
	if err != nil {
		return LinkPayload{}, ErrLinkFormat
	}
	var p LinkPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return LinkPayload{}, ErrLinkFormat
	}
	p.Hub, p.Code = strings.TrimSpace(p.Hub), strings.TrimSpace(p.Code)
	p.AccessClientID = strings.TrimSpace(p.AccessClientID)
	p.AccessClientSecret = strings.TrimSpace(p.AccessClientSecret)
	if p.Hub == "" || p.Code == "" || (p.AccessClientID == "") != (p.AccessClientSecret == "") {
		return LinkPayload{}, ErrLinkFormat
	}
	return p, nil
}

// CreateLinkRequest is the body of POST /api/runners/links. Name becomes the
// runner's name on the hub.
type CreateLinkRequest struct {
	Name string `json:"name"`
}

// CreateLinkResponse is returned once: Link is never shown again.
type CreateLinkResponse struct {
	Link string `json:"link"`
	Name string `json:"name"`
	Hub  string `json:"hub"`
	// ExpiresAt is RFC3339.
	ExpiresAt string `json:"expiresAt"`
	// Access reports whether the link carries the Access service token.
	Access bool `json:"access"`
}

// RedeemLinkRequest is the body of POST LinkPath.
type RedeemLinkRequest struct {
	Code string `json:"code"`
	// Name is the device's own label (its hostname, or runner login --name).
	// The runner's name on the hub is the one the link was made with; this is
	// recorded beside it.
	Name string `json:"name,omitempty"`
	OS   string `json:"os,omitempty"`
}

// RedeemLinkResponse is returned once, on redeem: Token is never shown again.
type RedeemLinkResponse struct {
	RunnerID string `json:"runnerId"`
	Name     string `json:"name"`
	Token    string `json:"token"`
}

// AccessTokenStatus is GET /api/runners/access-token: the stored service
// token's client ID and whether a secret is set, never the secret.
type AccessTokenStatus struct {
	ClientID string `json:"clientId"`
	Set      bool   `json:"set"`
}

// SetAccessTokenRequest is the body of PUT /api/runners/access-token.
type SetAccessTokenRequest struct {
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
}
