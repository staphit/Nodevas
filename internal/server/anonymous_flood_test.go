package server

import (
	"fmt"
	"net/http"
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
