package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"nodevas/internal/runnerwire"
)

// Strangers' device-link redeems are writes nobody signed in to. However
// many addresses they come from, they must not spend the write budget that
// signed-in members' writes share.
func TestAnonymousRedeemFloodLeavesMembersWrites(t *testing.T) {
	s := newRunnerTestServer(t)
	bea := s.signIn(t, "bea@example.test")
	for i := 0; i < 600; i++ {
		remote := fmt.Sprintf("198.51.100.%d:40000", i%8+1)
		s.fromAddress(t, nil, remote, http.MethodPost, runnerwire.LinkPath, fmt.Sprintf(`{"code":"junk-%d"}`, i))
	}
	if response := s.do(t, &bea, http.MethodPost, "/api/auth/step-up/request", `{}`, nil); response.Code != http.StatusAccepted {
		t.Fatalf("bea's write after an anonymous redeem flood = %d %s", response.Code, response.Body)
	}
}

// floodAnonymously sends 600 requests without a credential from 15
// addresses, enough to empty the hub-wide pre-auth bucket.
func floodAnonymously(t *testing.T, s *runnerTestServer) {
	t.Helper()
	refused := 0
	for i := 0; i < 600; i++ {
		remote := fmt.Sprintf("198.51.100.%d:40000", i%15+1)
		if s.fromAddress(t, nil, remote, http.MethodGet, "/api/projects", "").Code == http.StatusTooManyRequests {
			refused++
		}
	}
	if refused == 0 {
		t.Fatal("the flood never reached the hub-wide pre-auth limit")
	}
}

// A runner token is a member's credential, as a session cookie is: a flood
// of strangers must not refuse the runners members run. A made-up token is
// charged as a stranger's request once it is refused.
func TestAnonymousFloodLeavesRunnerTokens(t *testing.T) {
	s := newRunnerTestServer(t)
	bea := s.signIn(t, "bea@example.test")
	created := s.create(t, bea, "bea-pc")
	floodAnonymously(t, s)
	header := map[string]string{"Authorization": "Bearer " + created.Token}
	if response := s.do(t, nil, http.MethodGet, runnerwire.VersionPath, "", header); response.Code != http.StatusOK {
		t.Fatalf("bea's runner after the flood = %d %s", response.Code, response.Body)
	}
	refused := 0
	for i := 0; i < 200; i++ {
		request := httptest.NewRequest(http.MethodGet, runnerwire.VersionPath, nil)
		request.RemoteAddr = "203.0.113.9:40000"
		request.Header.Set("Authorization", fmt.Sprintf("Bearer %smade-up-%d", runnerwire.TokenPrefix, i))
		response := httptest.NewRecorder()
		s.handler.ServeHTTP(response, request)
		if response.Code == http.StatusTooManyRequests {
			refused++
		}
	}
	if refused == 0 {
		t.Fatal("made-up runner tokens from one address were never refused as a flood")
	}
}
