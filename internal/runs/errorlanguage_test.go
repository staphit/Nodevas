package runs

import (
	"testing"

	"nodevas/internal/locale"
	"nodevas/internal/runner"
	"nodevas/internal/runnerwire"
)

// Every reason auto-accept can give for not covering an offer reaches a
// Chinese reader whole in Chinese: a runner's wording that drifts from the
// hub's table shows up here, not as an English line in a Chinese page.
func TestAutoAcceptRefusalsRenderInChinese(t *testing.T) {
	offer := func(kind, mode, shell string) runner.Offer {
		return runner.Offer{
			Job:   runnerwire.Job{Kind: kind, Agent: "codex", RepoID: "repo-1", Mode: mode},
			Repo:  runnerwire.Repo{ID: "repo-1", Name: "visual-editor"},
			Shell: shell,
		}
	}
	strict := runnerwire.AutoAcceptPolicy{Enabled: true, RequireStepUp: true}
	cases := []struct {
		name   string
		policy runnerwire.AutoAcceptPolicy
		offer  runner.Offer
		want   string
	}{
		{"off", runnerwire.AutoAcceptPolicy{}, offer("", runnerwire.ModeHeadless, ""),
			"這個 runner 沒有開啟自動接受"},
		{"everything missing", strict, offer("", runnerwire.ModeInteractive, "danger-full-access"),
			"自動接受需要最近在 hub 上驗證過身分；repo visual-editor 不在這個 runner 的自動接受清單上；" +
				"agent codex 不在這個 runner 的自動接受清單上；interactive 執行不在這個 runner 的自動接受清單上；" +
				"agent codex 在這裡可以執行任何 shell 指令（danger-full-access）；" +
				"自動接受把這件事留給人決定（nodevas runner autoaccept allow-shell on）"},
		{"verify", strict, offer(runnerwire.JobVerify, runnerwire.ModeHeadless, ""),
			"verify 工作會在 agent 寫的程式碼上執行這個 repository 的測試指令；" +
				"要自動接受，請執行：nodevas runner autoaccept verify on；" +
				"自動接受需要最近在 hub 上驗證過身分；repo visual-editor 不在這個 runner 的自動接受清單上"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ok, reason := runner.AutoAcceptDecision(tc.policy, tc.offer)
			if ok {
				t.Fatalf("auto-accepted: %s", reason)
			}
			if got := RenderError(reason, locale.Chinese); got != tc.want {
				t.Errorf("Chinese = %q\nfor       %q\nwant      %q", got, reason, tc.want)
			}
			if got := RenderError(reason, locale.English); got != reason {
				t.Errorf("English = %q, want it as written %q", got, reason)
			}
		})
	}
}

// Anything the hub does not know, or knows only part of, stays as written.
func TestUnknownRunErrorStaysAsWritten(t *testing.T) {
	for _, text := range []string{
		"",
		"exit status 1",
		"declined at the runner; something else",
		"cancelled by",
		"something else (and nobody is at the runner's terminal to accept)",
	} {
		if got := RenderError(text, locale.Chinese); got != text {
			t.Errorf("RenderError(%q) = %q, want it as written", text, got)
		}
	}
}
