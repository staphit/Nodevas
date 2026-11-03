package main

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"nodevas/internal/runner"
)

// Every key `nodevas runner set` takes is named in its usage text, so a
// setting the owner may turn off (repoBrowse) is not one only the source
// mentions.
func TestRunnerUsageNamesEverySetting(t *testing.T) {
	var out bytes.Buffer
	runnerUsage(&out)
	for _, key := range runner.SettingKeys {
		if !regexp.MustCompile(`\b` + regexp.QuoteMeta(key) + `\b`).Match(out.Bytes()) {
			t.Errorf("runner usage does not name the setting %q", key)
		}
	}
}

// permissionRelay does not reach every run (round-1 security fixes): a run
// auto-accepted without allow-shell gets no relay, OpenCode 2 runs through
// `opencode run`, which cannot relay, and review, plan and judge runs keep
// their read-only limits whatever a person answers. The help and the
// status line say so, so the owner does not expect prompts that never come.
func TestRunnerTextSaysWhereThePermissionRelayStops(t *testing.T) {
	var usage bytes.Buffer
	runnerUsage(&usage)
	help := usage.String()
	start := strings.Index(help, "permissionRelay (")
	if start < 0 {
		t.Fatal("runner usage does not describe permissionRelay")
	}
	end := strings.Index(help[start:], "pull requests:")
	if end < 0 {
		t.Fatal("runner usage: permissionRelay is not followed by the pull request settings")
	}
	var status bytes.Buffer
	printRunSettings(&status, &runner.Config{PermissionRelay: true})
	line := ""
	for _, l := range strings.Split(status.String(), "\n") {
		if strings.Contains(l, "permissionRelay") {
			line = l
		}
	}
	for name, text := range map[string]string{"usage": strings.Join(strings.Fields(help[start:start+end]), " "), "status": line} {
		for _, want := range []string{"allow-shell", "OpenCode 2", "read-only"} {
			if !strings.Contains(text, want) {
				t.Errorf("%s does not mention %q: %s", name, want, text)
			}
		}
	}
}

// Runners are added, linked and revoked under Settings → Agents; there is
// no Runners page to send anyone to, from the CLI or from the hub's own
// messages and mail.
func TestRunnerTextNamesNoRunnersPage(t *testing.T) {
	for _, root := range []string{".", filepath.Join("..", "..", "internal")} {
		err := filepath.WalkDir(root, func(name string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				return nil
			}
			raw, err := os.ReadFile(name)
			if err != nil {
				return err
			}
			for i, line := range strings.Split(string(raw), "\n") {
				if strings.Contains(line, "Runners page") || strings.Contains(line, "Runners 頁面") {
					t.Errorf("%s:%d sends people to a Runners page: %s", name, i+1, strings.TrimSpace(line))
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
