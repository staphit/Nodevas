package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"nodevas/internal/runner"
	"nodevas/internal/runnerwire"
)

// linkHTTPClient redeems device links; tests swap it.
var linkHTTPClient = &http.Client{
	Timeout: 30 * time.Second,
	// A redirect here is Cloudflare Access sending a browser to its sign-in
	// page: the link carried no service token, or a stale one. Following it
	// would only fetch that page.
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// errLinkArgument refuses a link string on the command line, where every
// process on this machine (and the shell history) can read it.
var errLinkArgument = errors.New("a device link is a credential and is never taken as an argument: " +
	"run `nodevas runner login --link` and paste it at the prompt, or pipe it on stdin")

// linkInArgs reports a device link string among the arguments.
func linkInArgs(args []string) bool {
	for _, arg := range args {
		if strings.Contains(arg, runnerwire.LinkPrefix) {
			return true
		}
	}
	return false
}

// runnerLoginLink is `runner login --link`: the link string on stdin's first
// line (a hidden prompt at a terminal), then one request to redeem it. Nothing
// is saved unless the hub hands back a runner token, so a mistyped or stale
// link leaves a working runner as it was.
func runnerLoginLink(in *bufio.Reader, stdout, stderr io.Writer, configFlag, hubFlag, nameFlag string) error {
	link, err := readHiddenLine(in, stderr, "device link (from the hub's Runners → Link a new device): ")
	if err != nil {
		return fmt.Errorf("reading the device link from stdin: %w", err)
	}
	payload, err := runnerwire.DecodeLink(link)
	if err != nil {
		return err
	}
	hub, err := runner.ValidateHub(payload.Hub)
	if err != nil {
		return fmt.Errorf("the device link names a hub this runner will not use: %w", err)
	}
	if strings.TrimSpace(hubFlag) != "" {
		want, err := runner.ValidateHub(hubFlag)
		if err != nil {
			return err
		}
		if !strings.EqualFold(want, hub) {
			return fmt.Errorf("the device link is for %s, not %s", hub, want)
		}
	}
	path, cfg, err := loadRunnerConfig(configFlag, stderr)
	if err != nil {
		return err
	}
	device := strings.TrimSpace(nameFlag)
	if device == "" {
		device, _ = os.Hostname()
	}

	ctx, cancel := context.WithTimeout(context.Background(), linkHTTPClient.Timeout+5*time.Second)
	defer cancel()
	redeemed, err := redeemLink(ctx, hub, payload, device)
	if err != nil {
		return err
	}
	if err := runner.ValidateToken(redeemed.Token); err != nil {
		return fmt.Errorf("the hub answered with something that is not a runner token: %w", err)
	}

	cfg.Hub = hub
	cfg.Token = redeemed.Token
	cfg.AccessClientID = payload.AccessClientID
	cfg.AccessClientSecret = payload.AccessClientSecret
	if strings.TrimSpace(nameFlag) != "" {
		cfg.Name = strings.TrimSpace(nameFlag)
	}
	if err := runner.SaveConfig(path, cfg); err != nil {
		return fmt.Errorf("the hub created runner %q but its token could not be saved (%w); revoke it on the Runners page and link again",
			redeemed.Name, err)
	}
	fmt.Fprintf(stdout, "linked as runner %q on %s\nsaved %s\nstart the runner with: nodevas runner\n", redeemed.Name, hub, path)
	return nil
}

// redeemLink trades the link's code for a runner token. The Access service
// token, when the link has one, goes in the headers the edge checks.
func redeemLink(ctx context.Context, hub string, payload runnerwire.LinkPayload, device string) (runnerwire.RedeemLinkResponse, error) {
	body, err := json.Marshal(runnerwire.RedeemLinkRequest{Code: payload.Code, Name: device, OS: runtime.GOOS})
	if err != nil {
		return runnerwire.RedeemLinkResponse{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, hub+runnerwire.LinkPath, bytes.NewReader(body))
	if err != nil {
		return runnerwire.RedeemLinkResponse{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	if payload.AccessClientID != "" {
		request.Header.Set(runnerwire.AccessClientIDHeader, payload.AccessClientID)
		request.Header.Set(runnerwire.AccessClientSecretHeader, payload.AccessClientSecret)
	}
	response, err := linkHTTPClient.Do(request)
	if err != nil {
		return runnerwire.RedeemLinkResponse{}, fmt.Errorf("reaching the hub: %w", err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	if err != nil {
		return runnerwire.RedeemLinkResponse{}, fmt.Errorf("reading the hub's answer: %w", err)
	}
	switch {
	case response.StatusCode == http.StatusCreated || response.StatusCode == http.StatusOK:
		var out runnerwire.RedeemLinkResponse
		if err := json.Unmarshal(raw, &out); err != nil {
			return out, fmt.Errorf("the hub's answer is not a runner token: %w", err)
		}
		return out, nil
	case response.StatusCode >= 300 && response.StatusCode < 400,
		response.StatusCode == http.StatusForbidden && !isJSON(response):
		return runnerwire.RedeemLinkResponse{}, errors.New("Cloudflare Access did not let this request through: " +
			"the link carries no runner service token, or the stored one is no longer valid. " +
			"Ask an administrator to set the runner access token on the hub, then make a new link")
	case response.StatusCode == http.StatusNotFound:
		return runnerwire.RedeemLinkResponse{}, errors.New("that device link is unknown, expired or already used; make a new one on the hub's Runners page")
	case response.StatusCode == http.StatusTooManyRequests:
		return runnerwire.RedeemLinkResponse{}, errors.New("the hub refused: too many attempts; wait ten minutes and try a new link")
	}
	var failure struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(raw, &failure)
	if failure.Error == "" {
		failure.Error = http.StatusText(response.StatusCode)
	}
	return runnerwire.RedeemLinkResponse{}, fmt.Errorf("the hub refused the link (%d): %s", response.StatusCode, failure.Error)
}

func isJSON(response *http.Response) bool {
	return strings.HasPrefix(response.Header.Get("Content-Type"), "application/json")
}

// readHiddenLine reads one line from in. When stdin is a terminal it prompts
// on stderr and turns echo off while the line is typed, so the credential
// never shows on screen or in a scrollback.
func readHiddenLine(in *bufio.Reader, stderr io.Writer, prompt string) (string, error) {
	if isTerminal(os.Stdin) {
		fmt.Fprint(stderr, prompt)
		if restore, err := echoOff(os.Stdin); err == nil {
			defer func() {
				restore()
				fmt.Fprintln(stderr)
			}()
		}
	}
	line, err := readSecretLine(in)
	if err != nil {
		return "", err
	}
	if line == "" {
		return "", errors.New("nothing was given")
	}
	return line, nil
}
