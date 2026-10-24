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
