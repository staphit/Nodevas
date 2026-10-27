package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"nodevas/internal/buildinfo"
	"nodevas/internal/runner"
	"nodevas/internal/runnerwire"
)

// hubHTTPClient asks the hub for its commit; tests swap it. Like
// linkHTTPClient it never follows a redirect: one is Cloudflare Access
// sending a browser to its sign-in page.
var hubHTTPClient = &http.Client{
	Timeout:       20 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// runnerHubVersion is `runner hub-version`: the commit the saved hub was
// built from, one line on stdout. scripts/install.sh runs it to pick which
// GitHub-built binary to fetch, so the runner token and the Access secret stay
// in this program and never pass through the shell. The answer only selects a
// binary; the installer verifies what it fetches against GitHub.
func runnerHubVersion(args []string, stdout, stderr io.Writer) error {
	fs, configFlag := newRunnerFlags("hub-version")
	if rest, err := parseInterspersed(fs, args); err != nil || len(rest) > 0 {
		return errUsage
	}
	_, cfg, err := loadRunnerConfig(*configFlag, stderr)
	if err != nil {
		return err
	}
	if cfg.Hub == "" || cfg.Token == "" {
		return errors.New("not logged in: run nodevas runner login first")
	}
	hub, err := runner.ValidateHub(cfg.Hub)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), hubHTTPClient.Timeout+5*time.Second)
	defer cancel()
	commit, err := fetchHubCommit(ctx, hub, cfg)
	if err != nil {
		return err
	}
	fmt.Fprintln(stdout, commit)
	return nil
}

// fetchHubCommit asks hub for its commit with the runner's token (and the
// Access service token, when there is one). Anything but a full commit is an
// error, so a caller can use the answer as a path component.
func fetchHubCommit(ctx context.Context, hub string, cfg *runner.Config) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, hub+runnerwire.VersionPath, nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("Authorization", "Bearer "+cfg.Token)
	request.Header.Set("Accept", "application/json")
	if cfg.AccessClientID != "" {
		request.Header.Set(runnerwire.AccessClientIDHeader, cfg.AccessClientID)
		request.Header.Set(runnerwire.AccessClientSecretHeader, cfg.AccessClientSecret)
	}
	response, err := hubHTTPClient.Do(request)
	if err != nil {
		return "", fmt.Errorf("reaching the hub: %w", err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 4<<10))
	if err != nil {
		return "", fmt.Errorf("reading the hub's answer: %w", err)
	}
	switch {
	case response.StatusCode == http.StatusOK:
	case response.StatusCode == http.StatusNotFound:
		return "", errors.New("this hub does not report its commit (it is older than the version endpoint)")
	case response.StatusCode == http.StatusUnauthorized:
		return "", errors.New("the hub refused the runner token (revoked?); log in again")
	case response.StatusCode >= 300 && response.StatusCode < 400,
		response.StatusCode == http.StatusForbidden && !isJSON(response):
		return "", errors.New("Cloudflare Access did not let this request through: check the runner's Access service token")
	default:
		return "", fmt.Errorf("the hub answered %d", response.StatusCode)
	}
	var out runnerwire.VersionResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("the hub's answer is not a version: %w", err)
	}
	commit := strings.TrimSpace(out.Commit)
	if commit == "" {
		return "", errors.New("the hub does not know which commit it was built from (a local build?)")
	}
	if !buildinfo.IsCommit(commit) {
		return "", errors.New("the hub's answer is not a full commit")
	}
	return commit, nil
}
