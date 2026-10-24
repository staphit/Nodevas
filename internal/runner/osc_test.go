package runner

import (
	"strings"
	"testing"
)

func feedAll(s *oscScanner, chunks ...string) []string {
	var out []string
	for _, c := range chunks {
		for _, p := range s.feed([]byte(c)) {
			out = append(out, string(p))
		}
	}
	return out
}

func TestOSCScanner(t *testing.T) {
	cases := []struct {
		name   string
		chunks []string
		want   []string
	}{
		{"bel", []string{"abc\x1b]7501;working\x07def"}, []string{"working"}},
		{"st", []string{"\x1b]7501;{\"state\":\"idle\"}\x1b\\"}, []string{`{"state":"idle"}`}},
		{"two in one chunk", []string{"\x1b]7501;a\x07x\x1b]7501;b\x1b\\"}, []string{"a", "b"}},
		{"split everywhere", strings.Split("x\x1b]7501;split-up\x1b\\y", ""), []string{"split-up"}},
		{"split esc and bracket", []string{"..\x1b", "]75", "01", ";wait", "ing\x07"}, []string{"waiting"}},
		{"other osc skipped", []string{"\x1b]0;7501;title\x07\x1b]7501;real\x07"}, []string{"real"}},
		{"other osc with st", []string{"\x1b]2;\x1b]7501;fake\x1b\\\x1b]7501;ok\x07"}, []string{"ok"}},
		{"longer number", []string{"\x1b]75011;no\x07"}, nil},
		{"shorter number", []string{"\x1b]750;no\x07"}, nil},
		{"csi is not osc", []string{"\x1b[31m7501;red\x07"}, nil},
		{"cut short by another escape", []string{"\x1b]7501;half\x1b[0m\x1b]7501;whole\x07"}, []string{"whole"}},
		{"empty payload", []string{"\x1b]7501;\x07"}, []string{""}},
		{"junk", []string{"\x00\xff\x1b\x1b]7501;ok\x07\x9d"}, []string{"ok"}},
		{"unterminated", []string{"\x1b]7501;never ends"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var s oscScanner
			got := feedAll(&s, tc.chunks...)
			if strings.Join(got, "|") != strings.Join(tc.want, "|") || len(got) != len(tc.want) {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestOSCScannerOverflow(t *testing.T) {
	var s oscScanner
	got := feedAll(&s, "\x1b]7501;", strings.Repeat("x", maxOSCPayload+10), "\x07", "\x1b]7501;after\x07")
	if len(got) != 1 || got[0] != "after" {
		t.Fatalf("got %q", got)
	}
}

func TestAgentState(t *testing.T) {
	for payload, want := range map[string]string{
		`{"state":"working"}`:             "working",
		`{"state":" waiting-for-input "}`: "waiting-for-input",
		`{"other":"x"}`:                   "",
		`{"state":`:                       "",
		"idle":                            "idle",
		" done ":                          "done",
		"needs input":                     "needs input",
		"\x1b[31m":                        "",
		strings.Repeat("a", 65):           "",
		`{"state":"<script>"}`:            "",
		`{"state":"tool:Bash","extra":1}`: "tool:Bash",
		"":                                "",
	} {
		if got := agentState([]byte(payload)); got != want {
			t.Errorf("agentState(%q) = %q, want %q", payload, got, want)
		}
	}
}

func TestPlainTextAndLastLines(t *testing.T) {
	raw := "\x1b[?1049h\x1b[2J\x1b[H\x1b]0;title\x07\x1b(B\x1b[1;32mDone\x1b[0m: all good\r\n\r\n" +
		"line\ttwo\x1b]7501;idle\x1b\\\r\n\x1b[38;2;1;2;3m> \x1b[0m\r\n"
	text := plainText([]byte(raw))
	if strings.Contains(text, "\x1b") || strings.Contains(text, "title") || strings.Contains(text, "idle") {
		t.Fatalf("escapes left in %q", text)
	}
	if got := lastLines(text, 20); got != "Done: all good\nline two\n>" {
		t.Fatalf("lastLines = %q", got)
	}
	if got := lastLines(plainText([]byte("Quick\x1b[1Csafety\x1b[3Ccheck\x1b[5;1HYes,\x1b[CI\x1b[")), 5); got != "Quick safety   check\nYes, I" {
		t.Fatalf("cursor moves: %q", got)
	}
	if got := lastLines("a\nb\nc\n\n", 2); got != "b\nc" {
		t.Fatalf("lastLines(2) = %q", got)
	}
}
