package document

import "testing"

// The exported HTML file is standalone and carries no CSP, so SafeURL is the
// only thing standing between a document URL and script execution in whatever
// browser opens the export. These cases pin both halves of that contract: the
// allowlist lets real document links through, and no amount of control-char
// smuggling rebuilds a scripting scheme.
func TestSafeURL(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		// Plain scheme blocking.
		{"javascript", "javascript:alert(1)", ""},
		{"javascript mixed case", "JaVaScRiPt:alert(1)", ""},
		{"javascript leading space", "   javascript:alert(1)", ""},
		{"vbscript", "vbscript:msgbox(1)", ""},

		// Control characters an HTML parser strips from inside the attribute
		// value before resolving the URL. Each of these re-forms as
		// `javascript:` in a browser, so each must be rejected here.
		{"interior tab", "java\tscript:alert(1)", ""},
		{"interior newline", "java\nscript:alert(1)", ""},
		{"interior carriage return", "java\rscript:alert(1)", ""},
		{"interior nul", "java\x00script:alert(1)", ""},
		{"leading control", "\x01javascript:alert(1)", ""},
		{"scattered controls", "\tj\na\rv\x00a\x0bscript:alert(1)", ""},
		{"control before colon", "javascript\t:alert(1)", ""},
		{"delete char", "java\x7fscript:alert(1)", ""},

		// Allowed schemes.
		{"http", "http://example.com/a?b=1#c", "http://example.com/a?b=1#c"},
		{"https", "https://example.com/", "https://example.com/"},
		{"https uppercase scheme", "HTTPS://example.com/", "HTTPS://example.com/"},
		{"mailto", "mailto:someone@example.com", "mailto:someone@example.com"},

		// data: only for raster images.
		{"data png", "data:image/png;base64,iVBORw0KGgo=", "data:image/png;base64,iVBORw0KGgo="},
		{"data jpeg", "data:image/jpeg;base64,/9j/4AAQ", "data:image/jpeg;base64,/9j/4AAQ"},
		{"data gif", "data:image/gif;base64,R0lGOD", "data:image/gif;base64,R0lGOD"},
		{"data webp", "data:image/webp;base64,UklGRg", "data:image/webp;base64,UklGRg"},
		{"data html", "data:text/html,<script>alert(1)</script>", ""},
		{"data svg scripts", "data:image/svg+xml,<svg onload=alert(1)>", ""},
		{"data image no payload", "data:image/png", ""},
		{"data smuggled tab", "data:text/ht\tml,<script>alert(1)</script>", ""},

		// Relative references: how exported attachments are addressed.
		{"relative file", "attachments/diagram.png", "attachments/diagram.png"},
		{"relative with space", "attachments/my diagram.png", "attachments/my diagram.png"},
		{"absolute path", "/files/a.png", "/files/a.png"},
		{"dot relative", "./x/y.md", "./x/y.md"},
		{"parent relative", "../sibling/y.md", "../sibling/y.md"},
		{"anchor", "#section-2", "#section-2"},
		{"query", "?q=1", "?q=1"},
		{"colon after slash", "./a:b/c.png", "./a:b/c.png"},
		{"colon after anchor", "#a:b", "#a:b"},

		// Unknown or malformed schemes are not relative references.
		{"unknown scheme", "foo:bar", ""},
		{"file scheme", "file:///etc/passwd", ""},
		{"numeric scheme", "1foo:bar", ""},
		{"protocol relative stays", "//example.com/a.png", "//example.com/a.png"},

		// Degenerate input.
		{"empty", "", ""},
		{"only whitespace", "  \t\n ", ""},
		{"only controls", "\x00\x00", ""},
		{"bare colon", ":nope", ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := SafeURL(test.raw); got != test.want {
				t.Fatalf("SafeURL(%q) = %q, want %q", test.raw, got, test.want)
			}
		})
	}
}

// A surviving URL must be the sanitized string, not the raw one: if the
// renderer wrote back the original, the browser would resolve a different
// string than the one that passed the check.
func TestSafeURLReturnsSanitizedForm(t *testing.T) {
	got := SafeURL("  https://exa\tmple.com/a\x00b  ")
	if want := "https://example.com/ab"; got != want {
		t.Fatalf("SafeURL = %q, want %q", got, want)
	}
}
