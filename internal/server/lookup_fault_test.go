package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"syscall"
	"testing"
	"time"

	"nodevas/internal/realtime"
)

// A project lookup the disk could not answer is a passing fault, not a
// refusal: a request gets a retryable 503, not "not a member" or "no such
// project", and handed-off work is told to try again instead of stopping.
func TestAProjectLookupFaultIsNotARefusal(t *testing.T) {
	f := newMembersFixture(t)
	// other is restricted, and dan is not among its members: without the
	// fault he gets 403, an outsider's answer.
	if response := f.setMember(t, f.bea, "cal", "viewer"); response.Code != http.StatusOK {
		t.Fatalf("restrict other: %d %s", response.Code, response.Body)
	}
	bea := userID(t, f, "bea")
	restore := f.server.pm.InjectLookupFault(syscall.EIO)
	defer restore()
	for name, b := range map[string]*browser{"a member": &f.dan, "an administrator": &f.ann} {
		response := f.do(t, b, http.MethodGet, "/api/graph?project=other", "", nil)
		if response.Code != http.StatusServiceUnavailable || errorCode(t, response) != "PROJECT_LOOKUP_FAILED" {
			t.Errorf("%s during a lookup fault: %d %s; want 503 PROJECT_LOOKUP_FAILED", name, response.Code, response.Body)
		}
	}
	if may, err := f.server.AccountMayWrite(context.Background(), "main", bea); may || err == nil {
		t.Errorf("AccountMayWrite during a lookup fault = %v, %v; want an error to retry on", may, err)
	}
	restore()
	if code := f.code(t, f.bea, http.MethodGet, "/api/graph?project=other", ""); code != http.StatusOK {
		t.Errorf("bea after the fault: %d", code)
	}
	if code := f.code(t, f.dan, http.MethodGet, "/api/graph?project=other", ""); code != http.StatusForbidden {
		t.Errorf("dan after the fault: %d", code)
	}
}

// A socket subscribing while the lookup fails is not told it lost access.
func TestSubscribeDuringALookupFaultIsNotDenied(t *testing.T) {
	f := newMembersFixture(t)
	live := httptest.NewServer(f.handler)
	t.Cleanup(live.Close)
	dan := f.dialAs(t, live, f.dan)
	dan.send(map[string]any{"type": "subscribe", "project": "main"})
	dan.await("presence", func(event realtime.Event) bool { return event.Project == "main" })

	restore := f.server.pm.InjectLookupFault(syscall.EIO)
	dan.send(map[string]any{"type": "subscribe", "project": "other"})
	// Messages are handled in order: once a later subscribe is answered,
	// the first one has been.
	time.Sleep(100 * time.Millisecond)
	restore()
	dan.send(map[string]any{"type": "subscribe", "project": "main"})
	timeout := time.After(5 * time.Second)
	for {
		select {
		case event, ok := <-dan.events:
			if !ok {
				t.Fatal("connection closed")
			}
			if event.Type == "access-denied" {
				t.Fatalf("a lookup fault was answered as a refusal: %+v", event)
			}
			if event.Type == "presence" && event.Project == "main" {
				return
			}
		case <-timeout:
			t.Fatal("no presence after the fault")
		}
	}
}
