// Package mcp exposes a running Nodevas server to an MCP client such as Claude
// Code, so an agent can read the ready queue, claim work and report progress on
// the same board a person is looking at.
//
// The process is a thin shell. It opens no database, no store and no workspace
// lock: everything goes over HTTP to `nodevas serve`. That is what lets an agent
// and a browser hold the same workspace at once — the workspace lock is
// exclusive, so a second process that opened the files directly would lock the
// person out — and it is what makes the agent's writes visible in the browser
// without a reload, because they travel the same handlers that broadcast to the
// realtime hub.
package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"nodevas/internal/auth"
)

// Error codes an agent can act on. A tool that fails hands back one of these
// alongside readable prose: the code is what a client can branch on, the prose
// is what the model reads.
const (
	CodeServerUnavailable = "SERVER_UNAVAILABLE"
	CodeNotFound          = "NOT_FOUND"
	CodeInvalidArgument   = "INVALID_ARGUMENT"
	CodeLocked            = "LOCKED"
	CodeAlreadyClaimed    = "ALREADY_CLAIMED"
	CodeLeaseExpired      = "LEASE_EXPIRED"
	CodeConflict          = "CONFLICT"
	CodeRateLimited       = "RATE_LIMITED"
	CodeAuthRequired      = "AUTH_REQUIRED"
	CodePermissionDenied  = "PERMISSION_DENIED"
	CodeServerError       = "SERVER_ERROR"
)

// APIError is a failed call to the Nodevas server, carrying whatever the agent
// needs to decide what to do next.
type APIError struct {
	Code    string
	Message string
	Status  int

	// CurrentRev and DiskContent are set on CodeConflict: the caller writes
	// against a rev that is no longer on disk, and these are what the disk says
	// now. Nothing here merges them — an agent overwriting a person's edit is
	// exactly the failure this reports instead of causing.
	CurrentRev  string
	DiskContent string

	// RetryAfter is what the server asked for on CodeRateLimited.
	RetryAfter time.Duration
}

func (e *APIError) Error() string {
	if e.Message == "" {
		return e.Code
	}
	return e.Message
}

// codeFor maps an HTTP status onto the code an agent branches on. The server
// answers with statuses; agents should not have to know which ones mean
// "retry", so the translation happens once, here.
func codeFor(status int) string {
	switch status {
	case http.StatusNotFound:
		return CodeNotFound
	case http.StatusBadRequest, http.StatusUnprocessableEntity,
		http.StatusRequestEntityTooLarge, http.StatusUnsupportedMediaType:
		return CodeInvalidArgument
	case http.StatusUnauthorized:
		return CodeAuthRequired
	case http.StatusForbidden:
		// Distinct from CodeAuthRequired on purpose: a 403 means the server
		// knows who is asking and said no — a node's write_access outranks this
		// agent's role. Presenting it as an authentication problem would send
		// the agent chasing credentials it already has.
		return CodePermissionDenied
	case http.StatusConflict:
		return CodeConflict
	case http.StatusTooManyRequests:
		return CodeRateLimited
	}
	if status >= 500 {
		return CodeServerError
	}
	return CodeServerError
}

// Client talks to one Nodevas server about one project.
type Client struct {
	base      *url.URL
	project   string
	actor     string
	agentRole string
	http      *http.Client
}

// ClientOptions configures a Client. Project is fixed for the process: tools
// other than list_projects never accept a project argument, so an agent cannot
// be talked into writing to a workspace the operator did not point it at.
type ClientOptions struct {
	Server  string
	Project string
	Actor   string
	// AgentRole is the write-permission class every request declares:
	// "worker" or "orchestrator". Empty sends no header, which the server
	// reads as a human session. Like Actor it is fixed for the process, so a
	// tool call cannot talk the agent into a stronger role mid-session.
	AgentRole string
	Timeout   time.Duration
}

// LoopbackOnlyError is returned when --server names something this process
// refuses to talk to.
type LoopbackOnlyError struct{ Host string }

func (e *LoopbackOnlyError) Error() string {
	return fmt.Sprintf(
		"refusing to connect to %q: this build only talks to a server on this machine. "+
			"A remote Nodevas needs an authenticated transport, and there is no way to "+
			"supply credentials over stdio that is not worse than not supporting it",
		e.Host)
}

// NewClient validates the target and builds the HTTP client.
func NewClient(opts ClientOptions) (*Client, error) {
	raw := strings.TrimSpace(opts.Server)
	if raw == "" {
		raw = DefaultServer
	}
	base, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("--server %q: %w", raw, err)
	}
	if base.Scheme != "http" && base.Scheme != "https" {
		return nil, fmt.Errorf("--server %q: expected an http:// or https:// URL", raw)
	}
	if !isLoopbackURL(base) {
		return nil, &LoopbackOnlyError{Host: base.Host}
	}
	base.Path = strings.TrimSuffix(base.Path, "/")
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &Client{
		base:      base,
		project:   strings.TrimSpace(opts.Project),
		actor:     strings.TrimSpace(opts.Actor),
		agentRole: strings.TrimSpace(opts.AgentRole),
		http:      &http.Client{Timeout: timeout},
	}, nil
}

// Project is the workspace this client is pinned to; empty means the server's
// active project.
func (c *Client) Project() string { return c.project }

// Actor is the name written into the `by` field of everything this client
// changes. It is attribution, never authentication.
func (c *Client) Actor() string { return c.actor }

// AgentRole is the write-permission class this client declares on every
// request; empty means it presents as a human session.
func (c *Client) AgentRole() string { return c.agentRole }

// BaseURL is the server this client talks to, for diagnostics.
func (c *Client) BaseURL() string { return c.base.String() }

// isLoopbackURL reports whether a URL names this machine.
//
// A hostname that is not an IP literal is refused even if it might resolve to
// 127.0.0.1: resolution can change under us, and "it pointed at localhost when
// I checked" is not a property worth basing a trust boundary on.
func isLoopbackURL(u *url.URL) bool {
	host := u.Hostname()
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Probe checks that a server is actually answering before the MCP session
// starts. Without it the first tool call fails with a transport error the agent
// has no idea what to do with; with it, startup says what to run.
func (c *Client) Probe(ctx context.Context) error {
	var status struct {
		Mode string `json:"mode"`
	}
	if err := c.get(ctx, "/api/auth/status", nil, &status); err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.Code == CodeServerUnavailable {
			return fmt.Errorf(
				"no Nodevas server is answering at %s. Start one with "+
					"`nodevas serve --project <workspace>`, or pass --server if it "+
					"listens elsewhere", c.base.String())
		}
		return err
	}
	return nil
}

// get issues a read.
func (c *Client) get(ctx context.Context, path string, query url.Values, out any) error {
	return c.do(ctx, http.MethodGet, path, query, nil, out)
}

// post issues a write.
func (c *Client) post(ctx context.Context, path string, query url.Values, body, out any) error {
	return c.do(ctx, http.MethodPost, path, query, body, out)
}

// put issues a replacing write.
func (c *Client) put(ctx context.Context, path string, query url.Values, body, out any) error {
	return c.do(ctx, http.MethodPut, path, query, body, out)
}

// maxRateLimitRetries bounds the wait-and-try-again loop. The server's write
// budget is deliberately small, and an agent hammering it is the case the
// budget exists for: backing off is correct, but backing off forever would turn
// a busy server into a hung tool call.
const maxRateLimitRetries = 3

func (c *Client) do(ctx context.Context, method, path string, query url.Values, body, out any) error {
	for attempt := 0; ; attempt++ {
		err := c.attempt(ctx, method, path, query, body, out)
		var apiErr *APIError
		if !errors.As(err, &apiErr) || apiErr.Code != CodeRateLimited || attempt >= maxRateLimitRetries {
			return err
		}
		// Retry-After is whole seconds; the server sends 1.
		wait := time.Second
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
}

func (c *Client) attempt(ctx context.Context, method, path string, query url.Values, body, out any) error {
	target := *c.base
	target.Path = c.base.Path + path
	if c.project != "" && needsProject(path) {
		if query == nil {
			query = url.Values{}
		}
		if query.Get("project") == "" {
			query.Set("project", c.project)
		}
	}
	if len(query) > 0 {
		target.RawQuery = query.Encode()
	}

	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return &APIError{Code: CodeInvalidArgument, Message: err.Error()}
		}
		payload = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, target.String(), payload)
	if err != nil {
		return &APIError{Code: CodeInvalidArgument, Message: err.Error()}
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	// Deliberately no Origin and no Sec-Fetch-Site. The server's browser
	// protections key on those headers being present and wrong; a client that
	// is not a browser passes by not claiming to be one.
	request.Header.Set("Accept", "application/json")
	// Every request declares the role, reads included: which class of agent is
	// asking is part of who is asking, and a node's write_access is enforced
	// against it server-side.
	if c.agentRole != "" {
		request.Header.Set(auth.AgentRoleHeaderName, c.agentRole)
	}

	response, err := c.http.Do(request)
	if err != nil {
		return &APIError{
			Code:    CodeServerUnavailable,
			Message: fmt.Sprintf("cannot reach the Nodevas server at %s: %v", c.base.String(), err),
		}
	}
	defer response.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes))
	if err != nil {
		return &APIError{Code: CodeServerUnavailable, Message: err.Error()}
	}
	if response.StatusCode >= 400 {
		failure := decodeError(response.StatusCode, raw)
		failure.RetryAfter = retryAfter(response.Header)
		return failure
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return &APIError{
			Code:    CodeServerError,
			Message: fmt.Sprintf("the server's answer to %s %s was not the JSON expected: %v", method, path, err),
		}
	}
	return nil
}

// maxResponseBytes stops a huge project from being read entirely into this
// process before anything gets a chance to trim it.
const maxResponseBytes = 64 << 20

// decodeError turns a non-2xx response into an APIError.
func decodeError(status int, raw []byte) *APIError {
	failure := &APIError{Code: codeFor(status), Status: status}
	var payload struct {
		Error       string `json:"error"`
		DiskRev     string `json:"diskRev"`
		DiskContent string `json:"diskContent"`
		Code        string `json:"code"`
	}
	if err := json.Unmarshal(raw, &payload); err == nil {
		failure.Message = payload.Error
		failure.CurrentRev = payload.DiskRev
		failure.DiskContent = payload.DiskContent
		// The server may name a more specific reason than the status can carry
		// — ALREADY_CLAIMED and LOCKED are both 409, and an agent's next move
		// differs between them.
		if payload.Code != "" {
			failure.Code = payload.Code
		}
	}
	if failure.Message == "" {
		failure.Message = strings.TrimSpace(string(raw))
	}
	if failure.Message == "" {
		failure.Message = http.StatusText(status)
	}
	switch failure.Code {
	case CodeConflict:
		failure.Message = failure.Message +
			". Someone changed this at the same time; re-read it and apply your change to the current version"
	case CodeRateLimited:
		failure.Message = failure.Message + ". Slow down and try again"
	}
	return failure
}

// needsProject reports whether a path is scoped to one project. The workspace
// endpoints are not, and sending a project to them is at best ignored.
func needsProject(path string) bool {
	switch {
	case path == "/api/projects",
		path == "/api/auth/status",
		strings.HasPrefix(path, "/api/workspaces"):
		return false
	}
	return true
}

// DefaultServer is where `nodevas serve` listens unless told otherwise.
const DefaultServer = "http://127.0.0.1:5666"

// retryAfter reads the header the abuse guard sets, for logging.
func retryAfter(h http.Header) time.Duration {
	seconds, err := strconv.Atoi(strings.TrimSpace(h.Get("Retry-After")))
	if err != nil || seconds <= 0 {
		return time.Second
	}
	return time.Duration(seconds) * time.Second
}
