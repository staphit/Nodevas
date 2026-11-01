package redact

import (
	"strings"
	"testing"
)

func TestString(t *testing.T) {
	secrets := []string{
		"sk-ant-api03-" + strings.Repeat("A1b2", 10),
		"sk-proj-" + strings.Repeat("x9", 15),
		"sk-" + strings.Repeat("Q", 40),
		"ghp_" + strings.Repeat("a", 36),
		"gho_" + strings.Repeat("B", 36),
		"github_pat_11ABCDEFG0" + strings.Repeat("z", 30),
		"AKIAIOSFODNN7EXAMPLE",
		"xoxb-123456789012-abcdefghijkl",
		"eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U",
		"nvr_abcdefghijklmnopqrstuvwxyz012345",
		"-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNzaC1rZXktdjEAAAAA\n-----END OPENSSH PRIVATE KEY-----",
		"-----BEGIN RSA PRIVATE KEY-----\nMIIEpAIBAAKCAQEA cut off here",
	}
	for _, secret := range secrets {
		got := String("before " + secret + " after")
		if strings.Contains(got, secret) || !strings.Contains(got, Placeholder) {
			t.Errorf("not redacted: %q -> %q", secret, got)
		}
		if !strings.HasPrefix(got, "before ") {
			t.Errorf("%q: lost the text around it: %q", secret, got)
		}
	}
	for _, plain := range []string{
		"", "nothing to see", "risk-assessment of the sk- prefix", "ghost_protocol", "eyJ alone",
		"see https://github.com/o/r/pull/7", "AKIA short",
	} {
		if got := String(plain); got != plain {
			t.Errorf("changed %q -> %q", plain, got)
		}
	}
}

// More vendors' key shapes, a password in a URL and a bearer value: each one
// goes, and the words around it stay.
func TestStringCatchesKeysPasswordsAndBearers(t *testing.T) {
	for _, tc := range []struct{ in, secret string }{
		{"GEMINI_API_KEY=AIzaSyA1b2C3d4E5f6G7h8I9j0KlMnOpQrStUvW", "AIzaSyA1b2C3d4E5f6G7h8I9j0KlMnOpQrStUvW"},
		{"DATABASE_URL=postgres://nodevas:s3cr3t-pw@db:5432/x", "s3cr3t-pw"},
		{"git remote: https://oauth2:glpat-abcdefghij0123456789@gitlab.example/o/r.git", "glpat-abcdefghij0123456789"},
		{"curl -H 'Authorization: Bearer abcdefghijklmnop0123456789.xyz'", "abcdefghijklmnop0123456789.xyz"},
		{"stripe sk_live_" + strings.Repeat("a1", 12), "sk_live_" + strings.Repeat("a1", 12)},
		{"hf token hf_" + strings.Repeat("Ab", 17), "hf_" + strings.Repeat("Ab", 17)},
		{"npm npm_" + strings.Repeat("x", 36), "npm_" + strings.Repeat("x", 36)},
		{"link nvlink1.eyJodWIiOiJodHRwczovL2h1Yi5leGFtcGxlIiwiY29kZSI6ImFiYyJ9", "nvlink1.eyJodWIiOiJodHRwczovL2h1Yi5leGFtcGxlIiwiY29kZSI6ImFiYyJ9"},
	} {
		got := String(tc.in)
		if strings.Contains(got, tc.secret) || !strings.Contains(got, Placeholder) {
			t.Errorf("not redacted: %q -> %q", tc.in, got)
		}
	}
	for _, plain := range []string{
		"http://127.0.0.1:5666/api/graph", "ssh://git@github.com/o/r.git", "Bearer short", "user@example.com:22",
	} {
		if got := String(plain); got != plain {
			t.Errorf("changed %q -> %q", plain, got)
		}
	}
}

// Output is String plus environment-style assignments to secret-sounding
// names: an `env` dump or a printed .env file. Prose keeps its words.
func TestOutputRedactsSecretAssignments(t *testing.T) {
	dump := "GEMINI_API_KEY=whatever-it-is\nAWS_SECRET_ACCESS_KEY=wJalrXUtnFEMI/K7MDENG\nNODEVAS_SECRET_KEY: \"abc def\"\nPATH=/usr/bin"
	got := Output(dump)
	for _, secret := range []string{"whatever-it-is", "wJalrXUtnFEMI", "abc def"} {
		if strings.Contains(got, secret) {
			t.Errorf("not redacted %q: %q", secret, got)
		}
	}
	if !strings.Contains(got, "GEMINI_API_KEY=[redacted]") || !strings.Contains(got, "PATH=/usr/bin") {
		t.Errorf("Output(dump) = %q", got)
	}
	for _, prose := range []string{"Fixed the token: it now expires", "the password: field is not validated",
		"Author: Patrick", "max_tokens: 4096"} {
		if got := Output(prose); got != prose {
			t.Errorf("changed prose %q -> %q", prose, got)
		}
	}
}

// The shapes a URL password and an env dump also come in: an empty user (the
// usual Redis URL), '/' or '@' in the password, a JSON key, and PowerShell's
// `Get-ChildItem env:` table. A URL with a port and an '@' in its path is not
// a password.
func TestOutputRedactsOtherSecretShapes(t *testing.T) {
	for _, tc := range []struct{ in, secret string }{
		{"REDIS_URL=redis://:s3cr3tpw@cache:6379/0", "s3cr3tpw"},
		{"DATABASE_URL=postgres://u:ab/cd3f@db/x", "cd3f"},
		{"DATABASE_URL=postgres://u:p@ss@db/x", "ss@db"},
		{`{"DB_PASSWORD": "hunter2xyz"}`, "hunter2xyz"},
		{"Name                           Value\n----                           -----\n" +
			"AWS_SECRET_ACCESS_KEY          wJalrXUtnFEMI/K7MDENG\r\nPATH                           C:\bin", "wJalrXUtnFEMI"},
	} {
		got := Output(tc.in)
		if strings.Contains(got, tc.secret) || !strings.Contains(got, Placeholder) {
			t.Errorf("not redacted: %q -> %q", tc.in, got)
		}
	}
	for _, plain := range []string{
		"http://localhost:5173/@vite/client", "https://registry.npmjs.org:443/@types/node", "PATH                           C:\bin",
		"user@example.com:22", "the API_KEY  is read from the environment",
	} {
		if got := Output(plain); got != plain {
			t.Errorf("changed %q -> %q", plain, got)
		}
	}
}
