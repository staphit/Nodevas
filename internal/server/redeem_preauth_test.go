package server

import (
	"encoding/json"
	"net/http"
	"testing"

	"nodevas/internal/runnerwire"
)

// A device-link redeem is bounded per source (and its code per failures),
// not by the hub-wide pre-auth budget: a flood of strangers must not keep
// a member from linking the machine in front of them.
func TestAnonymousFloodLeavesDeviceLinkRedeems(t *testing.T) {
	s := newRunnerTestServer(t)
	bea := s.signIn(t, "bea@example.test")
	stepUp(t, s, bea)
	_, payload := s.makeLink(t, bea, "laptop")
	floodAnonymously(t, s)
	body, _ := json.Marshal(runnerwire.RedeemLinkRequest{Code: payload.Code, Name: "new-laptop", OS: "linux"})
	if response := s.fromAddress(t, nil, "192.0.2.60:40000", http.MethodPost, runnerwire.LinkPath, string(body)); response.Code != http.StatusCreated {
		t.Fatalf("a good code after the flood = %d %s", response.Code, response.Body)
	}
}
