package main

import (
	"flag"
	"io"
	"testing"

	"nodevas/internal/realtime"
)

func parseWSLimits(t *testing.T, env map[string]string, args ...string) realtime.ConnectionLimits {
	t.Helper()
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	limits := wsLimitFlags(fs, func(name string) (string, bool) {
		value, ok := env[name]
		return value, ok
	})
	if err := fs.Parse(args); err != nil {
		t.Fatal(err)
	}
	return limits()
}

func TestWSLimitFlagsDefault(t *testing.T) {
	if got, want := parseWSLimits(t, nil), realtime.DefaultConnectionLimits(); got != want {
		t.Fatalf("limits = %+v, want defaults %+v", got, want)
	}
}

func TestWSLimitFlagsEnvironmentAndCLIOverride(t *testing.T) {
	env := map[string]string{
		"NODEVAS_SERVE_WS_MAX_CONNS":             "1000",
		"NODEVAS_SERVE_WS_MAX_CONNS_PER_ACCOUNT": "24",
		"NODEVAS_SERVE_WS_MAX_CONNS_PER_IP":      "48",
	}
	want := realtime.ConnectionLimits{Total: 1000, PerAccount: 24, PerIP: 48}
	if got := parseWSLimits(t, env); got != want {
		t.Fatalf("env limits = %+v, want %+v", got, want)
	}
	// The command line wins over the environment.
	got := parseWSLimits(t, env, "--ws-max-conns-per-ip", "200")
	want.PerIP = 200
	if got != want {
		t.Fatalf("CLI limits = %+v, want %+v", got, want)
	}
}

func TestWSLimitFlagsIgnoreInvalidEnvironment(t *testing.T) {
	env := map[string]string{"NODEVAS_SERVE_WS_MAX_CONNS": "lots", "NODEVAS_SERVE_WS_MAX_CONNS_PER_IP": "-3"}
	if got, want := parseWSLimits(t, env), realtime.DefaultConnectionLimits(); got != want {
		t.Fatalf("limits = %+v, want defaults %+v for unusable values", got, want)
	}
}
