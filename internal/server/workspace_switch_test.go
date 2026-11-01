package server

import (
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
)

// Switching workspaces tells every socket to reload, members included; the
// event must not carry the new workspace's folder on the server's disk.
func TestWorkspaceSwitchDoesNotBroadcastItsPath(t *testing.T) {
	f := newMembersFixture(t)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/ws", f.server.pm.Hub().Handle)
	live := httptest.NewServer(router)
	t.Cleanup(live.Close)
	peer := dialHub(t, live)
	peer.await("hello", nil)

	second, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	previous := f.server.pm.Workspace()
	if err := f.server.pm.SwitchWorkspace(second); err != nil {
		t.Fatalf("SwitchWorkspace: %v", err)
	}
	t.Cleanup(func() { _ = f.server.pm.SwitchWorkspace(previous) })
	if event := peer.await("project-changed", nil); event.ID != "" {
		t.Fatalf("every socket was told the workspace path %q", event.ID)
	}
}
