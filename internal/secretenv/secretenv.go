// Package secretenv names the hub's own secrets among environment variables
// and removes them from the environment of every program the hub starts.
//
// The hub reads its master key, database URL, SMTP password and the like
// from the environment. Nothing it starts needs them: not the local runner
// that `serve --with-runner` starts (and through it the agents, their hooks
// and verify commands), not git, not PostgreSQL's own programs. Each of
// those children works on content an attacker may control, so each gets an
// environment without them.
package secretenv

import "strings"

// hubSecrets are the variables that hold a hub secret or point at one.
// secretenv_test.go walks the source for every variable the hub reads that
// looks like a secret, so a new one cannot be left off this list unnoticed.
var hubSecrets = []string{
	// The key sealing every stored secret, and the file holding it.
	"NODEVAS_SECRET_KEY",
	"NODEVAS_SECRET_KEY_FILE",
	// The database: its URL carries the password.
	"NODEVAS_DATABASE_URL",
	"NODEVAS_DATABASE_URL_FILE",
	"NODEVAS_TEST_DATABASE_URL",
	// SMTP.
	"NODEVAS_SMTP_PASSWORD",
	"NODEVAS_SMTP_PASSWORD_FILE",
	// `nodevas user` reads a new account's password here.
	"NODEVAS_PASSWORD",
	// The hub's TLS private key and its config file.
	"NODEVAS_SERVE_TLS_KEY",
	"NODEVAS_CONFIG",
	// The copilot key's file form is the hub's alone; a runner reads only
	// NODEVAS_COPILOT_API_KEY itself (SharedWithRunner).
	"NODEVAS_COPILOT_API_KEY_FILE",
	// A runner started by serve gets its token and Access settings from
	// its config file, never from these.
	"NODEVAS_RUNNER_TOKEN",
	"NODEVAS_ACCESS_CLIENT_SECRET",
	// libpq's and pgx's own password variables: pgx reads them for the
	// hub's database connection.
	"PGPASSWORD",
	"PGPASSFILE",
}

// SharedWithRunner are secrets the hub reads that a runner on the same
// machine reads too, by design, so the local runner keeps them:
//   - NODEVAS_COPILOT_API_KEY: the runner-side copilot uses the same key
//     (`nodevas runner copilot-key`); the runner withholds every NODEVAS_*
//     variable from agents itself.
//   - ANTHROPIC_API_KEY, OPENAI_API_KEY: the vendors' own variables, which
//     the agent CLIs authenticate with.
var SharedWithRunner = []string{"NODEVAS_COPILOT_API_KEY", "ANTHROPIC_API_KEY", "OPENAI_API_KEY"}

// IsHubSecret reports whether the variable name holds a hub secret or
// points at one. Names compare without regard to case, as Windows does.
func IsHubSecret(name string) bool {
	upper := strings.ToUpper(strings.TrimSpace(name))
	for _, secret := range hubSecrets {
		if upper == secret {
			return true
		}
	}
	return false
}

// Names lists the variables IsHubSecret names.
func Names() []string { return append([]string(nil), hubSecrets...) }

// Without is environ (KEY=value entries) without the hub's secrets.
func Without(environ []string) []string {
	return environ
}
