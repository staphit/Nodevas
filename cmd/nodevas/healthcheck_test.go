package main

import (
	"bytes"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func envFrom(values map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		value, ok := values[name]
		return value, ok
	}
}

// The probe has to ask the port the hub listens on, and it finds it the way
// serve does: --port, then NODEVAS_SERVE_PORT, then the config file, then
// the default.
func TestHealthcheckTargetFollowsTheServeConfiguration(t *testing.T) {
	project := t.TempDir()
	withConfig := t.TempDir()
	if err := os.WriteFile(filepath.Join(withConfig, "nodevas.yaml"), []byte("port: 9100\nlisten: 0.0.0.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	explicit := filepath.Join(t.TempDir(), "hub.yaml")
	if err := os.WriteFile(explicit, []byte("port: 9200\nlisten: 192.0.2.7\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		args []string
		env  map[string]string
		want string
	}{
		{"default", []string{"--project", project}, nil, "http://127.0.0.1:5666/api/healthz"},
		{"environment", []string{"--project", project}, map[string]string{"NODEVAS_SERVE_PORT": "8123"}, "http://127.0.0.1:8123/api/healthz"},
		{"project config", []string{"--project", withConfig}, nil, "http://127.0.0.1:9100/api/healthz"},
		{"environment over config", []string{"--project", withConfig}, map[string]string{"NODEVAS_SERVE_PORT": "8124"}, "http://127.0.0.1:8124/api/healthz"},
		{"NODEVAS_CONFIG", []string{"--project", project}, map[string]string{"NODEVAS_CONFIG": explicit}, "http://192.0.2.7:9200/api/healthz"},
		{"flag over everything", []string{"--project", withConfig, "--port", "7001"}, map[string]string{"NODEVAS_SERVE_PORT": "8124"}, "http://127.0.0.1:7001/api/healthz"},
		{"every interface", []string{"--project", project}, map[string]string{"NODEVAS_SERVE_LISTEN": "::"}, "http://127.0.0.1:5666/api/healthz"},
		{"ipv6 loopback", []string{"--project", project, "--listen", "::1"}, nil, "http://[::1]:5666/api/healthz"},
		{"tls", []string{"--project", project}, map[string]string{"NODEVAS_SERVE_TLS_CERT": "/run/cert.pem"}, "https://127.0.0.1:5666/api/healthz"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := healthcheckTarget(tc.args, envFrom(tc.env))
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("target = %q, want %q", got, tc.want)
			}
		})
	}
	if _, err := healthcheckTarget([]string{"--project", project}, envFrom(map[string]string{"NODEVAS_SERVE_PORT": "nope"})); err == nil {
		t.Fatal("an unparsable NODEVAS_SERVE_PORT was accepted")
	}
}

func TestHealthcheckExitsByWhatTheHubAnswers(t *testing.T) {
	status := http.StatusOK
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/healthz" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(status)
	}))
	defer hub.Close()
	_, port, err := net.SplitHostPort(strings.TrimPrefix(hub.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	args := []string{"--project", t.TempDir(), "--port", port}
	var stderr bytes.Buffer
	if code := healthcheckCmd(args, envFrom(nil), &stderr); code != 0 {
		t.Fatalf("healthy hub: exit %d (%s)", code, stderr.String())
	}
	status = http.StatusServiceUnavailable
	stderr.Reset()
	if code := healthcheckCmd(args, envFrom(nil), &stderr); code != 1 {
		t.Fatalf("hub answering 503: exit %d, want 1", code)
	}
	hub.Close()
	if code := healthcheckCmd(args, envFrom(nil), &stderr); code != 1 {
		t.Fatalf("hub not listening: exit %d, want 1", code)
	}
}

// The image's probe is the subcommand, not a URL with the default port
// written into it: a hub moved to another port must not be reported dead.
func TestDockerHealthcheckUsesTheConfiguredPort(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	var probe []string
	inHealthcheck := false
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "HEALTHCHECK") {
			inHealthcheck = true
		}
		if inHealthcheck {
			probe = append(probe, line)
			if !strings.HasSuffix(strings.TrimSpace(line), "\\") {
				break
			}
		}
	}
	healthcheck := strings.Join(probe, "\n")
	if healthcheck == "" {
		t.Fatal("Dockerfile has no HEALTHCHECK")
	}
	if strings.Contains(healthcheck, "5666") || !strings.Contains(healthcheck, "healthcheck") {
		t.Fatalf("HEALTHCHECK should run `nodevas healthcheck`, which reads the configured port:\n%s", healthcheck)
	}
}
