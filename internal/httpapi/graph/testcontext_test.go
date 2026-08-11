package graph

import (
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"

	projectdomain "nodevas/internal/project"
	"nodevas/internal/realtime"
	"nodevas/internal/store"
)

// ginContext builds the *gin.Context a handler takes, for the tests that call
// one directly instead of driving it through the router. Tests that need path
// parameters must set c.Params themselves.
func ginContext(response http.ResponseWriter, request *http.Request) *gin.Context {
	c, _ := gin.CreateTestContext(response)
	c.Request = request
	return c
}

// graphTestAPI builds the handler set over a workspace holding the freshly
// created default project, and returns its store so a test can assert against
// what actually reached disk.
func graphTestAPI(t *testing.T) (*API, *store.Store) {
	t.Helper()
	pm, err := projectdomain.NewManagerAt(t.TempDir(), realtime.NewHub(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pm.Close)
	return New(pm, realtime.NewHub()), pm.Store()
}
