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
	// Nodevas runner tokens (runnerwire.TokenPrefix) and device links
	// (runnerwire.LinkPrefix).
	regexp.MustCompile(`\bnvr_[A-Za-z0-9_-]{16,}`),
	regexp.MustCompile(`\bnvlink1\.[A-Za-z0-9_-]{16,}=*`),
	// Google API keys (Gemini among them).
	regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}`),
	// Stripe live secret and restricted keys.
	regexp.MustCompile(`\b[rs]k_live_[0-9A-Za-z]{16,}`),
	// GitLab personal access tokens, Hugging Face and npm tokens.
	regexp.MustCompile(`\bglpat-[0-9A-Za-z_-]{20,}`),
	regexp.MustCompile(`\bhf_[A-Za-z0-9]{30,}`),
	regexp.MustCompile(`\bnpm_[A-Za-z0-9]{36}\b`),
}

// kept are secrets whose surroundings stay: an HTTP bearer value.
var kept = []struct {
	re   *regexp.Regexp
	with string
}{
	{regexp.MustCompile(`(?i)\b(Bearer\s+)[A-Za-z0-9._~+/=-]{16,}`), "${1}" + Placeholder},
}

// urlPassword is the password of a URL, scheme://user:password@host, the
// user possibly empty (redis://:password@host): up to the last '@' before
// whitespace, as a password may hold '/' and '@' unencoded.
var urlPassword = regexp.MustCompile(`(?i)\b([a-z][a-z0-9+.-]*://[^/\s:@]*:)(\S*)@`)

// urlPort is what urlPassword takes from scheme://host:port/path@…: a port
// and a path, not a password.
var urlPort = regexp.MustCompile(`^[0-9]{1,5}/`)

// redactURLPasswords keeps scheme://user: and @host around a URL's password.
func redactURLPasswords(s string) string {
	return urlPassword.ReplaceAllStringFunc(s, func(m string) string {
		sub := urlPassword.FindStringSubmatch(m)
		if urlPort.MatchString(sub[2]) {
			return m
		}
		return sub[1] + Placeholder + "@"
	})
}

// quick are substrings one of which every pattern's match contains: text
// without any of them is not scanned.
var quick = []string{"PRIVATE KEY", "sk-", "gh", "github_pat_", "AKIA", "ASIA", "xox", "eyJ", "nvr_",
	"nvlink1.", "AIza", "k_live_", "glpat-", "hf_", "npm_", "://", "earer", "EARER"}

// String is s with every secret-looking string replaced by Placeholder.
func String(s string) string {
	if s == "" || !mayContain(s) {
		return s
	}
	for _, re := range patterns {
		s = re.ReplaceAllString(s, Placeholder)
	}
	for _, k := range kept {
		s = k.re.ReplaceAllString(s, k.with)
	}
	if strings.Contains(s, "://") {
		s = redactURLPasswords(s)
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

// secretName is a secret-sounding name in capitals, as an environment
// variable has it, so prose ("the token: it expires") keeps its words.
const secretName = `[A-Z_][A-Z0-9_]*(?:KEY|TOKEN|SECRET|PASSWORD|PASSWD|CREDENTIALS?|AUTH)[A-Z0-9_]*`

// secretAssignment is an assignment to a secret name, as an `env` dump, a
// printed .env file or a JSON object ("NAME": "value") has it.
var secretAssignment = regexp.MustCompile(`\b(` + secretName + `)("?\s*[=:]\s*)("[^"\n]*"|'[^'\n]*'|\S+)`)

// secretRow is a secret name and its value in PowerShell's `Get-ChildItem
// env:` table: the name at the start of a line, two or more spaces, the
// value to the end of the line.
var secretRow = regexp.MustCompile(`(?m)^([ \t]*` + secretName + `[ \t]{2,})\S[^\r\n]*`)

// Output is String for a run's output on its way off the machine (events,
// summary, errors, PR bodies, review comments), plus the values of secret
// assignments. A diff keeps String alone: an assignment there is code.
func Output(s string) string {
	s = String(s)
	if strings.ContainsAny(s, "=:") {
		s = secretAssignment.ReplaceAllString(s, "${1}${2}"+Placeholder)
	}
	if strings.Contains(s, "  ") || strings.Contains(s, "\t") {
		s = secretRow.ReplaceAllString(s, "${1}"+Placeholder)
	}
	return s
}
