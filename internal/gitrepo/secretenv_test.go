package gitrepo

import (
	"strings"
	"testing"
)

// Git runs a repository's content (filters, attributes, hooks it is told
// to skip but might not): it gets none of the hub's secrets.
func TestGitGetsNoHubSecrets(t *testing.T) {
	for _, name := range []string{"NODEVAS_SECRET_KEY", "NODEVAS_SECRET_KEY_FILE", "NODEVAS_DATABASE_URL", "NODEVAS_SMTP_PASSWORD", "PGPASSWORD"} {
		t.Setenv(name, "hub-secret-"+name)
	}
	r := &Repo{gitDir: t.TempDir(), workTree: t.TempDir()}
	for _, kv := range r.env(nil) {
		if strings.Contains(kv, "hub-secret-") {
			t.Errorf("git inherits %q", kv)
		}
	}
}
