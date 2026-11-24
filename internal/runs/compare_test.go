package runs

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"nodevas/internal/runnerwire"
)

func TestComparePromptAnonymisesAndRotates(t *testing.T) {
	entries := []CompareEntry{
		{
			Label:   "A",
			Branch:  "agent/fix-login-aaaaaaaa",
			Summary: "Approach A fixed the button handler.",
			Changes: &runnerwire.RunChanges{
				Commit:    "1111111111111111111111111111111111111111",
				Base:      "main",
				Additions: 12,
				Deletions: 3,
				Files:     []runnerwire.FileChange{{Path: "web/login.ts", Status: runnerwire.FileModified, Additions: 12, Deletions: 3}},
				Patch:     "diff --git a/web/login.ts b/web/login.ts\n--- a/web/login.ts\n+++ b/web/login.ts\n@@ -1 +1 @@\n-old\n+newA\n",
			},
		},
		{
			Label:   "B",
			Branch:  "agent/fix-login-bbbbbbbb",
			Summary: "Approach B rewrote form submit.",
			Changes: &runnerwire.RunChanges{
				Commit:    "2222222222222222222222222222222222222222",
				Base:      "main",
				Additions: 8,
				Deletions: 2,
				Files:     []runnerwire.FileChange{{Path: "web/form.ts", Status: runnerwire.FileModified, Additions: 8, Deletions: 2}},
				Patch:     "diff --git a/web/form.ts b/web/form.ts\n--- a/web/form.ts\n+++ b/web/form.ts\n@@ -1 +1 @@\n-old\n+newB\n",
			},
		},
		{
			Label:   "C",
			Branch:  "agent/fix-login-cccccccc",
			Summary: "Approach C added a fallback click handler.",
			Changes: &runnerwire.RunChanges{
				Commit:    "3333333333333333333333333333333333333333",
				Base:      "main",
				Additions: 5,
				Deletions: 1,
				Files:     []runnerwire.FileChange{{Path: "web/click.ts", Status: runnerwire.FileModified, Additions: 5, Deletions: 1}},
				Patch:     "diff --git a/web/click.ts b/web/click.ts\n--- a/web/click.ts\n+++ b/web/click.ts\n@@ -1 +1 @@\n-old\n+newC\n",
			},
		},
	}

	prompt := BuildComparePrompt("contest-1", "# Fix the login bug\n\nButton broken on Safari.", entries)
	for _, want := range []string{
		"Fix the login bug",
		"## Entry A",
		"## Entry B",
		"## Entry C",
		"agent/fix-login-aaaaaaaa",
		"agent/fix-login-bbbbbbbb",
		"agent/fix-login-cccccccc",
		"web/login.ts",
		"web/form.ts",
		"web/click.ts",
		"Score each entry 0-10",
		`"scores"`,
		`"pick"`,
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
	for _, forbidden := range []string{"claude", "codex", "gemini", "opus", "sonnet", "gpt-5"} {
		if strings.Contains(strings.ToLower(prompt), forbidden) {
			t.Errorf("prompt leaked agent/model word %q", forbidden)
		}
	}

	// Deterministic for the same contest id.
	if again := BuildComparePrompt("contest-1", "# Fix the login bug\n\nButton broken on Safari.", entries); again != prompt {
		t.Fatalf("BuildComparePrompt is not deterministic for the same contest id")
	}

	// Across different contest ids, the entry ordering rotates (not always A, B, C).
	orders := map[string]bool{}
	for i := 0; i < 30; i++ {
		p := BuildComparePrompt(fmt.Sprintf("contest-%d", i), "Task", entries)
		posA := strings.Index(p, "## Entry A")
		posB := strings.Index(p, "## Entry B")
		posC := strings.Index(p, "## Entry C")
		if posA < 0 || posB < 0 || posC < 0 {
			t.Fatalf("missing entry headings in contest-%d", i)
		}
		var order string
		switch {
		case posA < posB && posB < posC:
			order = "ABC"
		case posB < posC && posC < posA:
			order = "BCA"
		case posC < posA && posA < posB:
			order = "CAB"
		default:
			t.Fatalf("unexpected non-rotational order posA=%d posB=%d posC=%d", posA, posB, posC)
		}
		orders[order] = true
	}
	if len(orders) < 2 {
		t.Fatalf("expected rotated entry orders across contest ids, got only %v", orders)
	}
}

func TestComparePromptFitsMaxPrompt(t *testing.T) {
	hugeTask := "# Huge task\n\n" + strings.Repeat("task line with details and requirements\n", 1500)
	hugeSummary := strings.Repeat("summary sentence explaining approach. ", 300)
	hugePatch := "diff --git a/big.go b/big.go\n--- a/big.go\n+++ b/big.go\n@@ -1 +1 @@\n" +
		strings.Repeat("+line of generated diff content here\n", 4000)

	var files []runnerwire.FileChange
	for i := 0; i < 150; i++ {
		files = append(files, runnerwire.FileChange{
			Path:      fmt.Sprintf("pkg/sub/module_%03d/file_%03d.go", i, i),
			Status:    runnerwire.FileModified,
			Additions: 10,
			Deletions: 2,
		})
	}

	for _, n := range []int{2, 3} {
		labels := []string{"A", "B", "C"}[:n]
		entries := make([]CompareEntry, n)
		for i, lbl := range labels {
			entries[i] = CompareEntry{
				Label:   lbl,
				Branch:  "agent/task-" + lbl,
				Summary: hugeSummary,
				Changes: &runnerwire.RunChanges{
					Commit:    strings.Repeat("a", 40),
					Additions: 1000,
					Deletions: 200,
					Files:     files,
					Patch:     hugePatch,
				},
			}
		}
		prompt := BuildComparePrompt("c-huge", hugeTask, entries)
		if len(prompt) > runnerwire.MaxPromptBytes {
			t.Fatalf("n=%d: prompt len = %d, exceeds MaxPromptBytes (%d)", n, len(prompt), runnerwire.MaxPromptBytes)
		}
		if !utf8.ValidString(prompt) {
			t.Fatalf("n=%d: prompt is not valid UTF-8", n)
		}
		if !strings.Contains(prompt, "(longer text was cut)") {
			t.Fatalf("n=%d: expected cut marker in truncated prompt", n)
		}
		if !strings.Contains(prompt, "Score each entry 0-10") {
			t.Fatalf("n=%d: scoring instruction was cut off", n)
		}
	}
}

func TestComparePromptRedactsPatch(t *testing.T) {
	secretToken := "ghp_1234567890abcdefghijklmnopqrstuvwxyz"
	secretAssign := "sk-super-secret-value-99999"
	patch := strings.Join([]string{
		"diff --git a/app/config.go b/app/config.go",
		"--- a/app/config.go",
		"+++ b/app/config.go",
		"@@ -1,3 +1,5 @@",
		"+const gh = \"" + secretToken + "\"",
		"+OPENAI_API_KEY=" + secretAssign,
		"+const safe = \"hello world\"",
	}, "\n")

	prompt := BuildComparePrompt("c-redact", "Fix config", []CompareEntry{
		{
			Label:   "A",
			Branch:  "agent/cfg-a",
			Summary: "Updated config with API_SECRET=dontleakme123",
			Changes: &runnerwire.RunChanges{
				Commit:    "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				Additions: 3,
				Files:     []runnerwire.FileChange{{Path: "app/config.go", Status: runnerwire.FileModified, Additions: 3}},
				Patch:     patch,
			},
		},
		{
			Label:   "B",
			Branch:  "agent/cfg-b",
			Summary: "Clean summary",
			Changes: &runnerwire.RunChanges{
				Commit:    "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
				Additions: 1,
				Files:     []runnerwire.FileChange{{Path: "app/other.go", Status: runnerwire.FileModified, Additions: 1}},
				Patch:     "diff --git a/app/other.go b/app/other.go\n+ok\n",
			},
		},
	})

	for _, leaked := range []string{secretToken, secretAssign, "dontleakme123"} {
		if strings.Contains(prompt, leaked) {
			t.Errorf("compare prompt leaked secret %q:\n%s", leaked, prompt)
		}
	}
	if !strings.Contains(prompt, "const safe = \"hello world\"") {
		t.Errorf("compare prompt dropped safe code line")
	}
}

func TestComparePromptDropsSecretFiles(t *testing.T) {
	patch := strings.Join([]string{
		"diff --git a/.env b/.env",
		"new file mode 100644",
		"--- /dev/null",
		"+++ b/.env",
		"@@ -0,0 +1,2 @@",
		"+DATABASE_URL=postgres://user:pass@host/db",
		"+TOP_SECRET_LINE=env_value_alpha",
		"diff --git a/src/main.go b/src/main.go",
		"--- a/src/main.go",
		"+++ b/src/main.go",
		"@@ -1 +1,2 @@",
		" package main",
		"+// normal code change",
		"diff --git a/certs/server.key b/certs/server.key",
		"--- a/certs/server.key",
		"+++ b/certs/server.key",
		"@@ -0,0 +1 @@",
		"+PRIVATE_KEY_BODY_BETA",
		"diff --git a/deploy/prod-kubeconfig.yaml b/deploy/prod-kubeconfig.yaml",
		"--- a/deploy/prod-kubeconfig.yaml",
		"+++ b/deploy/prod-kubeconfig.yaml",
		"@@ -0,0 +1 @@",
		"+KUBECONFIG_CLUSTER_GAMMA",
		"diff --git a/home/id_rsa b/home/id_rsa",
		"--- a/home/id_rsa",
		"+++ b/home/id_rsa",
		"@@ -0,0 +1 @@",
		"+SSH_KEY_DELTA",
	}, "\n")

	prompt := BuildComparePrompt("c-secrets", "Check files", []CompareEntry{
		{
			Label:   "A",
			Branch:  "agent/sec-a",
			Summary: "Done",
			Changes: &runnerwire.RunChanges{
				Commit:    "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				Additions: 6,
				Files: []runnerwire.FileChange{
					{Path: ".env", Status: runnerwire.FileAdded, Additions: 2},
					{Path: "src/main.go", Status: runnerwire.FileModified, Additions: 1},
					{Path: "certs/server.key", Status: runnerwire.FileAdded, Additions: 1},
					{Path: "deploy/prod-kubeconfig.yaml", Status: runnerwire.FileAdded, Additions: 1},
					{Path: "home/id_rsa", Status: runnerwire.FileAdded, Additions: 1},
				},
				Patch: patch,
			},
		},
		{
			Label:   "B",
			Branch:  "agent/sec-b",
			Summary: "Done",
			Changes: &runnerwire.RunChanges{
				Commit:    "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
				Additions: 1,
				Files:     []runnerwire.FileChange{{Path: "src/ok.go", Status: runnerwire.FileModified, Additions: 1}},
				Patch:     "diff --git a/src/ok.go b/src/ok.go\n+// ok\n",
			},
		},
	})

	for _, forbidden := range []string{
		"env_value_alpha",
		"PRIVATE_KEY_BODY_BETA",
		"KUBECONFIG_CLUSTER_GAMMA",
		"SSH_KEY_DELTA",
		"certs/server.key",
		"prod-kubeconfig.yaml",
		"home/id_rsa",
	} {
		if strings.Contains(prompt, forbidden) {
			t.Errorf("compare prompt leaked secret file or hunk %q:\n%s", forbidden, prompt)
		}
	}
	if !strings.Contains(prompt, "src/main.go") || !strings.Contains(prompt, "// normal code change") {
		t.Errorf("compare prompt dropped non-secret file hunk:\n%s", prompt)
	}
}

func TestParseScoresLastBlockOnly(t *testing.T) {
	// Two fenced json blocks: only the last one counts.
	twoBlocks := strings.Join([]string{
		"Initial thought:",
		"```json",
		`{"scores":[{"entry":"A","score":3,"why":"first draft"},{"entry":"B","score":9,"why":"first draft"}],"pick":"B"}`,
		"```",
		"After checking tests again:",
		"```json",
		`{"scores":[{"entry":"A","score":8,"why":"passes edge cases"},{"entry":"B","score":6,"why":"misses nil check"}],"pick":"A"}`,
		"```",
	}, "\n")

	got := ParseScores(twoBlocks, []string{"A", "B"})
	if got == nil {
		t.Fatalf("ParseScores = nil, want parsed last block")
	}
	if got.Pick != "A" || len(got.Scores) != 2 || got.Scores[0].Score != 8 || got.Scores[1].Score != 6 {
		t.Fatalf("ParseScores = %+v, want A=8 B=6 pick=A", got)
	}

	// Valid first block followed by broken last json block -> nil (never falls back to first).
	brokenLast := strings.Join([]string{
		"```json",
		`{"scores":[{"entry":"A","score":8,"why":"ok"},{"entry":"B","score":7,"why":"ok"}],"pick":"A"}`,
		"```",
		"```json",
		`{"not": "scores"}`,
		"```",
	}, "\n")
	if got := ParseScores(brokenLast, []string{"A", "B"}); got != nil {
		t.Fatalf("broken last block = %+v, want nil", got)
	}

	// A non-json code block after the json block does not clobber the last json block.
	withOtherFence := strings.Join([]string{
		"```json",
		`{"scores":[{"entry":"A","score":9,"why":"great"},{"entry":"B","score":5,"why":"partial"}],"pick":"A"}`,
		"```",
		"```go",
		"func example() {}",
		"```",
	}, "\n")
	got = ParseScores(withOtherFence, []string{"A", "B"})
	if got == nil || got.Pick != "A" {
		t.Fatalf("withOtherFence = %+v, want pick A", got)
	}

	// No fenced json block -> nil.
	if got := ParseScores(`{"scores":[{"entry":"A","score":8,"why":"unwrapped"}],"pick":"A"}`, []string{"A", "B"}); got != nil {
		t.Fatalf("unfenced json = %+v, want nil", got)
	}
}

func TestParseScoresRejectsUnknownLabel(t *testing.T) {
	cases := []struct {
		name    string
		json    string
		allowed []string
	}{
		{
			name:    "unknown entry label C in 2-entry contest",
			json:    `{"scores":[{"entry":"A","score":8,"why":"ok"},{"entry":"C","score":7,"why":"nope"}],"pick":"A"}`,
			allowed: []string{"A", "B"},
		},
		{
			name:    "reviewer label J",
			json:    `{"scores":[{"entry":"A","score":8,"why":"ok"},{"entry":"J","score":7,"why":"nope"}],"pick":"A"}`,
			allowed: []string{"A", "B"},
		},
		{
			name:    "unknown pick label",
			json:    `{"scores":[{"entry":"A","score":8,"why":"ok"},{"entry":"B","score":7,"why":"ok"}],"pick":"C"}`,
			allowed: []string{"A", "B"},
		},
		{
			name:    "duplicate label",
			json:    `{"scores":[{"entry":"A","score":8,"why":"ok"},{"entry":"A","score":7,"why":"dup"}],"pick":"A"}`,
			allowed: []string{"A", "B"},
		},
		{
			name:    "score above 10",
			json:    `{"scores":[{"entry":"A","score":11,"why":"too high"},{"entry":"B","score":7,"why":"ok"}],"pick":"A"}`,
			allowed: []string{"A", "B"},
		},
		{
			name:    "negative score",
			json:    `{"scores":[{"entry":"A","score":-1,"why":"negative"},{"entry":"B","score":7,"why":"ok"}],"pick":"B"}`,
			allowed: []string{"A", "B"},
		},
		{
			name:    "float score",
			json:    `{"scores":[{"entry":"A","score":7.5,"why":"float"},{"entry":"B","score":7,"why":"ok"}],"pick":"A"}`,
			allowed: []string{"A", "B"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			summary := "```json\n" + tc.json + "\n```"
			if got := ParseScores(summary, tc.allowed); got != nil {
				t.Fatalf("ParseScores = %+v, want nil", got)
			}
		})
	}
}

func TestParseScoresRedactsWhy(t *testing.T) {
	longWhy := strings.Repeat("好", 350)
	summary := "```json\n" +
		`{"scores":[` +
		`{"entry":"A","score":9,"why":"Fixed auth; token ghp_1234567890abcdefghijklmnopqrstuvwxyz and API_KEY=mysecret999"},` +
		`{"entry":"B","score":6,"why":"` + longWhy + `"}` +
		`],"pick":"A"}` + "\n```"

	got := ParseScores(summary, []string{"A", "B"})
	if got == nil || len(got.Scores) != 2 {
		t.Fatalf("ParseScores = %+v", got)
	}
	if strings.Contains(got.Scores[0].Why, "ghp_1234567890") || strings.Contains(got.Scores[0].Why, "mysecret999") {
		t.Fatalf("why leaked secret: %q", got.Scores[0].Why)
	}
	if !strings.Contains(got.Scores[0].Why, "[redacted]") {
		t.Fatalf("why missing redaction placeholder: %q", got.Scores[0].Why)
	}
	if n := utf8.RuneCountInString(got.Scores[1].Why); n > 300 || !utf8.ValidString(got.Scores[1].Why) {
		t.Fatalf("why rune count = %d (valid=%v), want <= 300", n, utf8.ValidString(got.Scores[1].Why))
	}
}
