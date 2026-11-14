package main

import (
	"strings"
	"testing"
)

// hubSecretEnv sets every kind of hub secret serve may hold in its
// environment, each with a value the assertions can spot.
func hubSecretEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		"NODEVAS_SECRET_KEY", "NODEVAS_SECRET_KEY_FILE", "NODEVAS_DATABASE_URL", "NODEVAS_DATABASE_URL_FILE",
		"NODEVAS_SMTP_PASSWORD", "NODEVAS_SMTP_PASSWORD_FILE", "NODEVAS_COPILOT_API_KEY_FILE",
		"NODEVAS_SERVE_TLS_KEY", "NODEVAS_CONFIG", "PGPASSWORD",
	} {
		t.Setenv(name, "hub-secret-"+name)
	}
}

func TestLocalRunnerGetsNoHubSecrets(t *testing.T) {
	hubSecretEnv(t)
	t.Setenv("NODEVAS_COPILOT_API_KEY", "runner-copilot-key")
	env := localRunnerEnv(false)
	for _, kv := range env {
		if strings.Contains(kv, "hub-secret-") {
			t.Errorf("the local runner inherits %q", kv)
		}
	}
	joined := strings.Join(env, "\n")
	if !strings.Contains(joined, localRunnerUnderServeEnv+"=1") {
		t.Error("the local runner is not marked as started by serve")
	}
	// The runner-side copilot reads the same key by design.
	if !strings.Contains(joined, "NODEVAS_COPILOT_API_KEY=runner-copilot-key") {
		t.Error("the local runner lost NODEVAS_COPILOT_API_KEY")
	}
	// Only a standalone hub's runner is told it is the single-user
	// edition's.
	if strings.Contains(joined, localRunnerEditionEnv) {
		t.Error("a runner for a hub that is not standalone is marked single-user")
	}
	single := strings.Join(localRunnerEnv(true), "\n")
	if !strings.Contains(single, localRunnerEditionEnv+"=single") || !strings.Contains(single, localRunnerUnderServeEnv+"=1") ||
		strings.Contains(single, "hub-secret-") {
		t.Error("the standalone hub's runner is not marked single-user, or got hub secrets")
	}
}

func TestPostgresToolsGetNoHubSecrets(t *testing.T) {
	hubSecretEnv(t)
	for _, kv := range scrubbedEnv() {
		if strings.Contains(kv, "hub-secret-") {
			t.Errorf("pg_dump/pg_restore inherit %q", kv)
		}
	}
}
