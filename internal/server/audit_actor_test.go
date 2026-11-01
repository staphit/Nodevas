package server

import (
	"context"
	"net/http"
	"testing"

	"nodevas/internal/audit"
	"nodevas/internal/identity"
)

// Redeeming a device link takes no session: nobody is signed in on that
// request, so its row in the trail must not say the local administrator
// made it.
func TestAnonymousWriteIsNotRecordedAsTheLocalAdministrator(t *testing.T) {
	s := newRunnerTestServer(t)
	bea := s.signIn(t, "bea@example.test")
	stepUp(t, s, bea)
	_, payload := s.makeLink(t, bea, "laptop")
	if code, _ := s.redeem(t, payload.Code); code != http.StatusCreated {
		t.Fatalf("redeem = %d", code)
	}
	rows, err := s.server.audit.Query(context.Background(), audit.Filter{ActionPrefix: "POST runner/link"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) == 0 {
		t.Fatal("the redeem left no request row")
	}
	for _, row := range rows {
		if row.ActorID == identity.Local.ID || row.ActorName == identity.Local.Name {
			t.Fatalf("the redeem is recorded as the local administrator: %+v", row)
		}
	}
}
