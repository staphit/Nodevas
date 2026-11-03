package runs

import (
	"fmt"
	"regexp"
	"strings"

	"nodevas/internal/locale"
)

// A run's error is stored as its runner or the hub said it: in English, since
// a runner is its own process (often another machine, another version) with no
// reader to ask. RenderError gives a Chinese reader the runner's and the hub's
// own sentences in Chinese when the run is read; anything else — an agent's
// words, a reason this hub does not know — stays as written, never half
// translated.

// runErrorChinese is every fixed sentence a run's error can be.
var runErrorChinese = map[string]string{
	// The runner's terminal accepter (internal/runner, accept.go).
	"runner needs someone at its terminal to accept (or `nodevas runner autoaccept on`)": "runner 需要有人在它的終端機前接受（或執行 `nodevas runner autoaccept on`）",
	"nobody accepted at the runner":      "runner 那端沒有人接受",
	"declined at the runner":             "在 runner 那端被拒絕",
	"the runner's terminal input closed": "runner 的終端機輸入已關閉",
	"cancelled before anyone accepted":   "在有人接受前就取消了",
	// Auto-accept (internal/runner, autoaccept.go).
	"auto-accept is off on this runner":                                              "這個 runner 沒有開啟自動接受",
	"auto-accept needs a recent step-up on the hub":                                  "自動接受需要最近在 hub 上驗證過身分",
	"auto-accept leaves that to a person (nodevas runner autoaccept allow-shell on)": "自動接受把這件事留給人決定（nodevas runner autoaccept allow-shell on）",
	"verify jobs run this repository's test command on code an agent wrote":          "verify 工作會在 agent 寫的程式碼上執行這個 repository 的測試指令",
	"auto-accept them with: nodevas runner autoaccept verify on":                     "要自動接受，請執行：nodevas runner autoaccept verify on",
	// The runner's checks before it asks (internal/runner, jobs.go).
	"this runner already ran or refused this run": "這個 runner 已經執行過或拒絕過這次執行",
	"run id is not usable on this runner":         "這個 run id 在這個 runner 上不能用",
	"a verify job is headless":                    "verify 工作只能在背景執行",
	// The hub's own.
	"cancelled while the runner was offline": "runner 離線時已取消",
	ReasonLost:                               "runner 重新啟動或遺失了這次執行",
	"the pipeline failed":                    "管線失敗了",
	"cancelled by a person":                  "被人取消",
	"autopilot cancelled":                    "autopilot 已取消",
	"skipped in the autopilot":               "在 autopilot 中略過",
}

// runErrorPatterns are the sentences that name something: the captured
// names are kept as they are.
var runErrorPatterns = []struct {
	english *regexp.Regexp
	chinese string
}{
	// The hub's cancels, naming who.
	{regexp.MustCompile(`^cancelled by (.+)$`), "被 %s 取消"},
	{regexp.MustCompile(`^paused everything: cancelled by (.+)$`), "全部暫停：被 %s 取消"},
	// Auto-accept and the runner's checks.
	{regexp.MustCompile(`^repo (.+) is not on this runner's auto-accept list$`), "repo %s 不在這個 runner 的自動接受清單上"},
	{regexp.MustCompile(`^agent (\S+) is not on this runner's auto-accept list$`), "agent %s 不在這個 runner 的自動接受清單上"},
	{regexp.MustCompile(`^(\S+) runs are not on this runner's auto-accept list$`), "%s 執行不在這個 runner 的自動接受清單上"},
	{regexp.MustCompile(`^agent (\S+) may run any shell command here \((.+)\)$`), "agent %s 在這裡可以執行任何 shell 指令（%s）"},
	{regexp.MustCompile(`^repository (".*") is not on this runner's allowlist$`), "repository %s 不在這個 runner 的允許清單上"},
	{regexp.MustCompile(`^runner is busy: it runs at most (\d+) at once$`), "runner 忙碌中：它一次最多執行 %s 個"},
	{regexp.MustCompile(`^agent (".*") is not installed on this runner$`), "這個 runner 沒有安裝 agent %s"},
	{regexp.MustCompile(`^model (".*") is not one this runner offers for (\S+)$`), "這個 runner 的 %[2]s 沒有提供 model %[1]s"},
}

// nobodyAtTerminal is what the runner adds when auto-accept did not cover an
// offer and nobody was at its terminal to be asked instead.
const (
	nobodyAtTerminal        = " (and nobody is at the runner's terminal to accept)"
	nobodyAtTerminalChinese = "（而且 runner 的終端機前沒有人可以接受）"
)

// RenderError is a run's error for a reader of language.
func RenderError(text string, language locale.Language) string {
	if language != locale.Chinese || text == "" {
		return text
	}
	body, suffix := text, ""
	if trimmed, ok := strings.CutSuffix(text, nobodyAtTerminal); ok {
		body, suffix = trimmed, nobodyAtTerminalChinese
	}
	// Auto-accept lists every condition that failed, "; " between them.
	parts := strings.Split(body, "; ")
	for i, part := range parts {
		chinese, ok := runErrorSentence(part)
		if !ok {
			return text
		}
		parts[i] = chinese
	}
	return strings.Join(parts, "；") + suffix
}

func runErrorSentence(english string) (string, bool) {
	if chinese := runErrorChinese[english]; chinese != "" {
		return chinese, true
	}
	for _, pattern := range runErrorPatterns {
		match := pattern.english.FindStringSubmatch(english)
		if match == nil {
			continue
		}
		args := make([]any, len(match)-1)
		for i, name := range match[1:] {
			args[i] = name
		}
		return fmt.Sprintf(pattern.chinese, args...), true
	}
	return "", false
}
