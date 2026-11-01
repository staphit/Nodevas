package server

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
)

// A TLS-terminating proxy missing from --trusted-proxy may send only
// X-Forwarded-Proto. The hub then ignores it, so its cookies lose Secure and
// its responses HSTS; the operator hears about that too.
func TestForwardedProtoFromAnUntrustedPeerIsReported(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var noted []string
	srv := &Server{abuse: newAbuseGuard(), forwarding: newForwardingNotes(func(peer string) {
		mu.Lock()
		noted = append(noted, peer)
		mu.Unlock()
	})}
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(srv.preAuthAbuseLimits)
	router.GET("/api/state", func(c *gin.Context) { c.Status(http.StatusOK) })
	request := httptest.NewRequest(http.MethodGet, "/api/state", nil)
	request.RemoteAddr = "172.31.250.20:40000"
	request.Header.Set("X-Forwarded-Proto", "https")
	router.ServeHTTP(httptest.NewRecorder(), request)
	if len(noted) != 1 || noted[0] != "172.31.250.20" {
		t.Fatalf("reported %v, want the untrusted peer that sent X-Forwarded-Proto", noted)
	}
}
