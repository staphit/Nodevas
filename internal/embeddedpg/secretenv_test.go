package embeddedpg

import (
	"strings"
	"testing"
)

func TestPostgresProgramsGetNoHubSecrets(t *testing.T) {
	for _, name := range []string{"NODEVAS_SECRET_KEY", "NODEVAS_SECRET_KEY_FILE", "NODEVAS_SMTP_PASSWORD", "NODEVAS_CONFIG"} {
		t.Setenv(name, "hub-secret-"+name)
	}
	for _, kv := range pgEnv() {
		if strings.Contains(kv, "hub-secret-") {
			t.Errorf("a PostgreSQL program inherits %q", kv)
		}
	}
}
