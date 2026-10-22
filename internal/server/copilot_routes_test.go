package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"nodevas/internal/realtime"
)

type fakeCopilotRoutes struct{}

func (fakeCopilotRoutes) Register(api *gin.RouterGroup) {
	api.GET("/copilot/status", func(c *gin.Context) { c.JSON(http.StatusOK, map[string]bool{"enabled": true}) })
}

// The copilot's tool bridge serves its calls through the router Handler
// built, so that router has to be reachable afterwards, and the copilot
// routes have to be on it.
func TestHandlerKeepsTheRouterForTheCopilot(t *testing.T) {
	srv := New(nil, realtime.NewHub(), nil)
	t.Cleanup(func() { _ = srv.Shutdown(t.Context()) })
	if srv.BuiltHandler() != nil {
		t.Fatal("a router before Handler was called")
	}
	srv.UseCopilot(fakeCopilotRoutes{})
	handler := srv.Handler()
	if srv.BuiltHandler() != handler {
		t.Fatal("BuiltHandler is not the router Handler returned")
	}
	request := httptest.NewRequest(http.MethodGet, "/api/copilot/status", nil)
	request.Host = "127.0.0.1"
	response := httptest.NewRecorder()
	srv.BuiltHandler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("copilot route = %d %s", response.Code, response.Body)
	}
}
