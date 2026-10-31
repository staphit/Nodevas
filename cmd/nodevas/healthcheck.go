package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"nodevas/internal/config"
)

// healthcheckTimeout is how long the probe waits for an answer; the image's
// HEALTHCHECK gives the whole command 5 s.
const healthcheckTimeout = 4 * time.Second

// healthcheckCmd asks the hub on this machine whether it is healthy, the way
// a container runtime wants it answered: exit 0 for a 2xx from /api/healthz,
// 1 for anything else. It runs as its own process, so it cannot see the
// flags serve was started with; it finds the address from the same places
// serve reads it (healthcheckTarget).
func healthcheckCmd(args []string, lookupEnv func(string) (string, bool), stderr io.Writer) int {
	target, err := healthcheckTarget(args, lookupEnv)
	if err != nil {
		fmt.Fprintln(stderr, "healthcheck:", err)
		return 1
	}
	client := &http.Client{
		Timeout: healthcheckTimeout,
		Transport: &http.Transport{
			Proxy: nil,
			// The probe asks this machine's own listener, by address rather
			// than by the name its certificate carries, and sends nothing;
			// whether the certificate verifies is not its question.
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // loopback self-probe
		},
	}
	resp, err := client.Get(target)
	if err != nil {
		fmt.Fprintln(stderr, "healthcheck:", err)
		return 1
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		fmt.Fprintf(stderr, "healthcheck: %s answered %s\n", target, resp.Status)
		return 1
	}
	return 0
}

// healthcheckTarget is the /api/healthz URL of the hub serve would start with
// the same configuration: --port, --listen and --tls-cert if given, else
// NODEVAS_SERVE_PORT, NODEVAS_SERVE_LISTEN and NODEVAS_SERVE_TLS_CERT, else
// the config file (--config, NODEVAS_CONFIG, or <project>/nodevas.yaml),
// else the defaults. A listener on every interface is asked on loopback.
func healthcheckTarget(args []string, lookupEnv func(string) (string, bool)) (string, error) {
	fs := flag.NewFlagSet("healthcheck", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	project := fs.String("project", ".", "workspace directory serve was started with")
	configPath := fs.String("config", "", "server YAML config file (default: <project>/nodevas.yaml)")
	port := fs.Int("port", 0, "listen port")
	listen := fs.String("listen", "", "listen address")
	certFile := fs.String("tls-cert", "", "TLS certificate file: the hub answers HTTPS")
	if err := fs.Parse(args); err != nil {
		return "", err
	}
	if fs.NArg() > 0 {
		return "", fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}

	path, explicit := strings.TrimSpace(*configPath), *configPath != ""
	if !explicit {
		if fromEnv, ok := lookupEnv("NODEVAS_CONFIG"); ok && strings.TrimSpace(fromEnv) != "" {
			path, explicit = strings.TrimSpace(fromEnv), true
		}
	}
	if !explicit {
		path = filepath.Join(*project, config.DefaultFileName)
	}
	cfg, err := config.LoadServeConfig(path, !explicit)
	if err != nil {
		return "", err
	}
	if err := config.ApplyEnvironment(&cfg, lookupEnv); err != nil {
		return "", err
	}
	if *port != 0 {
		cfg.Port = *port
	}
	if *listen != "" {
		cfg.Listen = *listen
	}
	if *certFile != "" {
		cfg.TLSCert = *certFile
	}
	if cfg.Port < 1 || cfg.Port > 65535 {
		return "", fmt.Errorf("port %d is not between 1 and 65535", cfg.Port)
	}

	host := strings.Trim(strings.TrimSpace(cfg.Listen), "[]")
	if ip := net.ParseIP(host); host == "" || (ip != nil && ip.IsUnspecified()) {
		host = "127.0.0.1"
	}
	scheme := "http"
	if strings.TrimSpace(cfg.TLSCert) != "" {
		scheme = "https"
	}
	return scheme + "://" + net.JoinHostPort(host, strconv.Itoa(cfg.Port)) + healthzPath, nil
}

// healthzPath is the hub's liveness endpoint (internal/server/health.go).
const healthzPath = "/api/healthz"

// healthcheck is the `nodevas healthcheck` entry point.
func healthcheck(args []string) {
	os.Exit(healthcheckCmd(args, os.LookupEnv, os.Stderr))
}
