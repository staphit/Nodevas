// Package redact replaces secret-looking strings with "[redacted]" in text
// that leaves the machine: a run's events, summary and errors on their way to
// the hub, and an external session's diff snapshot. An agent that read a key
// (from a .env file, `env`, a config) must not be able to post it through its
// run's output.
//
// It is pattern based: it catches the token shapes vendors use (prefixes,
// fixed lengths, PEM armour), not every secret. Text without a match is
// returned as it is.
package redact

import (
	"regexp"
	"strings"
)

// Placeholder is what a secret is replaced with.
const Placeholder = "[redacted]"

var patterns = []*regexp.Regexp{
	// PEM private keys, armour and all; an unterminated one to the end.
	regexp.MustCompile(`-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----(?s:.*?)(-----END [A-Z0-9 ]*PRIVATE KEY-----|\z)`),
	// Anthropic (sk-ant-…) and OpenAI-style (sk-…, sk-proj-…) API keys.
	regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{20,}`),
	// GitHub tokens: classic (ghp_, gho_, ghu_, ghs_, ghr_) and fine-grained.
	regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{20,}`),
	regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{20,}`),
	// AWS access key IDs.
	regexp.MustCompile(`\b(AKIA|ASIA)[0-9A-Z]{16}\b`),
	// Slack tokens.
	regexp.MustCompile(`\bxox[abposr]-[A-Za-z0-9-]{10,}`),
	// JSON Web Tokens.
	regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`),
	// Nodevas runner tokens (runnerwire.TokenPrefix).
	regexp.MustCompile(`\bnvr_[A-Za-z0-9_-]{16,}`),
}

// quick are substrings one of which every pattern's match contains: text
// without any of them is not scanned.
var quick = []string{"PRIVATE KEY", "sk-", "gh", "github_pat_", "AKIA", "ASIA", "xox", "eyJ", "nvr_"}

// String is s with every secret-looking string replaced by Placeholder.
func String(s string) string {
	if s == "" || !mayContain(s) {
		return s
	}
	for _, re := range patterns {
		s = re.ReplaceAllString(s, Placeholder)
	}
	return s
}

func mayContain(s string) bool {
	for _, q := range quick {
		if strings.Contains(s, q) {
			return true
		}
	}
	return false
}
