package adapter

import (
	"testing"

	"nodevas/internal/runnerwire"
)

// The recorded fixtures, through the real parser: what a run's failure text
// is, and what it is classified as.
func TestClassifyFixtures(t *testing.T) {
	cases := []struct {
		name    string
		adapter Adapter
		fixture string
		want    string
	}{
		// Real codex-cli 0.156.0 output: a model a ChatGPT login cannot use.
		{"codex model", Codex{}, "codex_model_error.jsonl", runnerwire.FailureModel},
		// claude_error.jsonl: the turn limit, which another model would hit too.
		{"claude max turns", Claude{}, "claude_error.jsonl", runnerwire.FailureTask},
		// gemini_error.jsonl is written from the CLI's docs, not recorded.
		{"gemini quota", Gemini{}, "gemini_error.jsonl", runnerwire.FailureQuota},
		// Real opencode 1.18.23 output: the model's provider is unreachable.
		{"opencode provider down", OpenCode{}, "opencode_error.jsonl", runnerwire.FailureModel},
		// Real agy 1.4.6 output: an executor error, nothing another model fixes.
		{"antigravity executor", Antigravity{}, "antigravity_error.jsonl", runnerwire.FailureTask},
	}
	for _, c := range cases {
		_, outcome := parseWith(t, c.adapter.NewParser(), c.fixture)
		if !outcome.IsError {
			t.Fatalf("%s: not an error: %+v", c.name, outcome)
		}
		if got := c.adapter.Classify(outcome.Error); got != c.want {
			t.Errorf("%s: %q classified %q, want %q", c.name, outcome.Error, got, c.want)
		}
	}
}

// Synthesized lines: these are written to look like what each CLI says, not
// recorded from it. They pin the tables' order (a 429 about a usage limit is
// a quota) and what must not match (a 429 inside an ID).
func TestClassifyTables(t *testing.T) {
	type row struct {
		text, want string
	}
	tables := []struct {
		adapter Adapter
		rows    []row
	}{
		{Claude{}, []row{
			{`API Error: 429 {"type":"error","error":{"type":"rate_limit_error","message":"Number of request tokens has exceeded your per-minute rate limit"}}`, runnerwire.FailureRateLimit},
			{`API Error: 529 {"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`, runnerwire.FailureRateLimit},
			{"Claude AI usage limit reached|1761300000", runnerwire.FailureQuota},
			{"5-hour limit reached ∙ resets 3pm", runnerwire.FailureQuota},
			{"Credit balance is too low", runnerwire.FailureQuota},
			{"Invalid API key · Please run /login", runnerwire.FailureAgentAbsent},
			{"OAuth token has expired. Please obtain a new token or refresh your existing token.", runnerwire.FailureAgentAbsent},
			{"There's an issue with the selected model (claude-nope). It may not exist or you may not have access to it.", runnerwire.FailureModel},
			{`API Error: 404 {"type":"error","error":{"type":"not_found_error","message":"model: claude-nope"}}`, runnerwire.FailureModel},
			{"\n/bin/sh: 1: claude: command not found", runnerwire.FailureAgentAbsent},
			{"'node' is not recognized as an internal or external command,", runnerwire.FailureAgentAbsent},
			{"Reached maximum number of turns (50)", runnerwire.FailureTask},
			{"session 0b3f2a1c-0429-4000 ended; tests failed", runnerwire.FailureTask},
			{"", runnerwire.FailureTask},
		}},
		{Codex{}, []row{
			{"The 'gpt-6-astra' model is not supported when using Codex with a ChatGPT account. (HTTP 400)", runnerwire.FailureModel},
			{"model_not_found: The model `o9` does not exist", runnerwire.FailureModel},
			{"exceeded retry limit, last status: 429 Too Many Requests", runnerwire.FailureRateLimit},
			{"Rate limit reached for gpt-5.5 in organization org-x on tokens per min", runnerwire.FailureRateLimit},
			{"You've hit your usage limit. Upgrade to Pro or try again in 3 hours.", runnerwire.FailureQuota},
			{`{"type":"usage_limit_reached","status":429}`, runnerwire.FailureQuota},
			{"insufficient_quota (HTTP 429)", runnerwire.FailureQuota},
			{"\nError: Not logged in. Run `codex login` first.", runnerwire.FailureAgentAbsent},
			{"Your access token could not be refreshed because your refresh token has expired. Please log out and sign in again.", runnerwire.FailureAgentAbsent},
			{"codex reported that the turn failed", runnerwire.FailureTask},
		}},
		{Gemini{}, []row{
			{"[API Error: Quota exceeded for quota metric 'Gemini 2.5 Pro Requests']", runnerwire.FailureQuota},
			{`{"error":{"code":429,"message":"Resource has been exhausted (e.g. check quota).","status":"RESOURCE_EXHAUSTED"}}`, runnerwire.FailureQuota},
			{`{"error":{"code":429,"status":"RESOURCE_EXHAUSTED"}}`, runnerwire.FailureRateLimit},
			{"models/gemini-9-pro is not found for API version v1beta", runnerwire.FailureModel},
			{"Please set an Auth method in your settings.json or specify GEMINI_API_KEY", runnerwire.FailureAgentAbsent},
			{"Tool execution failed: npm test exited 1", runnerwire.FailureTask},
		}},
		{OpenCode{}, []row{
			{"ProviderModelNotFoundError: Model not found: opencode/nope", runnerwire.FailureModel},
			{"ProviderAuthError: Anthropic API key is missing", runnerwire.FailureAgentAbsent},
			{"APIError: 401 Unauthorized", runnerwire.FailureAgentAbsent},
			{"APIError: Rate limit exceeded, retry later (HTTP 429)", runnerwire.FailureRateLimit},
			{"APIError: insufficient_quota (HTTP 429)", runnerwire.FailureQuota},
			{"'node' is not recognized as an internal or external command,", runnerwire.FailureAgentAbsent},
			{"tests failed after the edit", runnerwire.FailureTask},
		}},
		{Antigravity{}, []row{
			{"Print mode: not authenticated, trying silent auth", runnerwire.FailureAgentAbsent},
			{`AGY_ERROR: {"short_error":"RESOURCE_EXHAUSTED: quota exceeded for this model","status":"RESOURCE_EXHAUSTED"}`, runnerwire.FailureQuota},
			{`AGY_ERROR: {"short_error":"too many requests","status":"RESOURCE_EXHAUSTED","error_code":8}`, runnerwire.FailureRateLimit},
			{"model gemini-9 not found", runnerwire.FailureModel},
			{"the design in the brief is incomplete", runnerwire.FailureTask},
		}},
	}
	for _, table := range tables {
		for _, r := range table.rows {
			if got := table.adapter.Classify(r.text); got != r.want {
				t.Errorf("%s: %q classified %q, want %q", table.adapter.Kind(), r.text, got, r.want)
			}
		}
	}
}

// Claude Code's rate_limit_event says when a refused window resets; an
// allowed one says nothing about a failure.
func TestClaudeRateLimitEventReset(t *testing.T) {
	p := Claude{}.NewParser()
	p.Parse([]byte(`{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","resetsAt":1792785600,"rateLimitType":"five_hour"}}`))
	if !p.Outcome().ResetAt.IsZero() {
		t.Fatalf("allowed event set a reset: %v", p.Outcome().ResetAt)
	}
	p.Parse([]byte(`{"type":"rate_limit_event","rate_limit_info":{"status":"rejected","resetsAt":1792785600,"rateLimitType":"five_hour"}}`))
	p.Parse([]byte(`{"type":"result","subtype":"success","is_error":true,"result":"Claude AI usage limit reached|1792785600"}`))
	if got := p.Outcome(); got.ResetAt.Unix() != 1792785600 || !got.IsError {
		t.Fatalf("outcome = %+v", got)
	}
}
