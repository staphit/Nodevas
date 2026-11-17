//go:build copilot_eval

// Quality evals for the drafter, against a real local model. Not part of
// `go test ./...` or CI: the build tag keeps this file out, and nothing here
// calls a paid API unless a key is passed explicitly.
//
//	go test -tags copilot_eval ./internal/copilot -run TestDraftEval -model ollama:qwen3:8b -v
//	go test -tags copilot_eval ./internal/copilot -run TestDraftEval -model lmstudio:qwen3-8b -v
//	NODEVAS_EVAL_API_KEY=… go test -tags copilot_eval ./internal/copilot -run TestDraftEval -model openai:<model> -base https://…/v1 -v
//	go test -tags copilot_eval ./internal/copilot -run TestDraftEval -model opencode:opencode/deepseek-v4.1-flash-free -v
//
// opencode:<provider/model> drives the opencode CLI (`opencode run`), which
// reaches OpenCode's free models that are not served to other clients; it
// runs in an empty folder and approves no tool, so it can only answer.
//
// Ollama's default context is small for a board listing; start it with
// OLLAMA_CONTEXT_LENGTH=16384. The cases are internal/copilot/evals/cases.json:
// a fixture board, a description and the properties a good draft has. Each
// case scores the share of its properties met; the run reports the mean per
// language and overall, so a change to the prompt or the model can be
// compared before and after. A low score does not fail the test.
//
// Baseline (2026-11-17, cases.json as of the P7 commit, the drafter prompt
// before the final review fixes): opencode:opencode/deepseek-v4.1-flash-free
// scored 99% overall (zh-TW 97%, en 100%) over 30 cases, every case valid on
// the first call with no repair, 4-14 s per draft. The first run scored 98%;
// zh-long-spec's keywords were then widened (租屋, 租約) because the model had
// drafted those steps correctly in other words. Known misses:
// zh-after-selection drops the third of three steps (上線); and while both
// review-gate cases passed, in the browser smoke test a refine asking for a
// manager's review (發布前加一個主管審查) added it as kind task, not gate.
// Ollama and LM Studio were never run, nor whether they honour
// response_format (the provider retries without it when refused, and the
// drafter validates the answer either way). Re-run and replace
// this paragraph when the prompt, the schema or the cases change.
package copilot

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"nodevas/internal/copilot/provider"
)

var (
	evalModel = flag.String("model", "", "model to evaluate: ollama:<model>, lmstudio:<model>, opencode:<provider/model>, openai:<model> (needs -base) or anthropic:<model>")
	evalBase  = flag.String("base", "", "base URL for openai:<model>")
	evalCase  = flag.String("case", "", "run only cases whose name contains this")
)

type evalNode struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Kind   string `json:"kind"`
	Status string `json:"status"`
}

type evalBoard struct {
	Layout string     `json:"layout"`
	Nodes  []evalNode `json:"nodes"`
	// Edges are [waiter, prerequisite].
	Edges [][2]string `json:"edges"`
}

type evalExpect struct {
	MinNodes      int           `json:"minNodes"`
	MaxNodes      int           `json:"maxNodes"`
	Keywords      [][]string    `json:"keywords"`
	Kinds         []string      `json:"kinds"`
	Order         [][2][]string `json:"order"`
	NoDuplicateOf []string      `json:"noDuplicateOf"`
	AttachesTo    []string      `json:"attachesTo"`
	QuestionsOK   bool          `json:"questionsOk"`
	NeedsCode     bool          `json:"needsCode"`
}

type evalCaseSpec struct {
	Name     string     `json:"name"`
	Language string     `json:"language"`
	Board    string     `json:"board"`
	Selected []string   `json:"selected"`
	Text     string     `json:"text"`
	Expect   evalExpect `json:"expect"`
}

type evalFile struct {
	Boards map[string]evalBoard `json:"boards"`
	Cases  []evalCaseSpec       `json:"cases"`
}

func evalProvider(t *testing.T) provider.Provider {
	t.Helper()
	kind, model, ok := strings.Cut(*evalModel, ":")
	if !ok || model == "" {
		t.Skip("no -model given; see the file comment")
	}
	config := provider.Config{Model: model}
	switch kind {
	case "opencode":
		if _, err := exec.LookPath("opencode"); err != nil {
			t.Skip("opencode is not installed")
		}
		return &opencodeProvider{model: model, dir: t.TempDir()}
	case "ollama":
		config.Kind, config.BaseURL = "openai", "http://127.0.0.1:11434/v1"
	case "lmstudio":
		config.Kind, config.BaseURL = "openai", "http://127.0.0.1:1234/v1"
	case "openai", "anthropic":
		// Only with a key the person passed for this run.
		config.Kind, config.BaseURL, config.APIKey = kind, *evalBase, os.Getenv("NODEVAS_EVAL_API_KEY")
		if config.APIKey == "" {
			t.Skip("openai:/anthropic: need NODEVAS_EVAL_API_KEY; evals never use a configured key")
		}
	default:
		t.Fatalf("unknown model kind %q", kind)
	}
	p, err := provider.New(config)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDraftEval(t *testing.T) {
	model := evalProvider(t)
	raw, err := os.ReadFile("evals/cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var file evalFile
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	scores := map[string][]float64{}
	var lines []string
	for _, spec := range file.Cases {
		if *evalCase != "" && !strings.Contains(spec.Name, *evalCase) {
			continue
		}
		c, board := evalContext(file.Boards[spec.Board], spec)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		started := time.Now()
		outcome, err := draftWith(ctx, model, c, board, func(string) {})
		cancel()
		elapsed := time.Since(started).Round(100 * time.Millisecond)
		var score float64
		var failed []string
		if err != nil {
			failed = []string{"error: " + err.Error()}
		} else {
			score, failed = scoreDraft(outcome, spec.Expect, file.Boards[spec.Board])
		}
		scores[spec.Language] = append(scores[spec.Language], score)
		scores["all"] = append(scores["all"], score)
		line := fmt.Sprintf("%-22s %4.0f%%  %2d nodes  %d call(s)  %6s", spec.Name, score*100,
			len(outcome.draft.Nodes), outcome.calls, elapsed)
		if len(failed) > 0 {
			line += "  missed: " + strings.Join(failed, "; ")
		}
		lines = append(lines, line)
		t.Log(line)
		if testing.Verbose() && err == nil {
			for _, node := range outcome.draft.Nodes {
				t.Logf("    %s [%s] %s after %v", node.Key, node.Kind, node.Title, node.After)
			}
		}
	}
	summary := fmt.Sprintf("model %s: overall %.0f%% (zh-TW %.0f%%, en %.0f%%) over %d cases",
		*evalModel, mean(scores["all"])*100, mean(scores["zh-TW"])*100, mean(scores["en"])*100, len(scores["all"]))
	t.Log(summary)
	fmt.Println(strings.Join(append(lines, summary), "\n"))
}

func evalContext(b evalBoard, spec evalCaseSpec) (draftContext, draftBoard) {
	board := draftBoard{known: map[string]string{}, kinds: builtinKinds}
	c := draftContext{Text: spec.Text, Language: spec.Language, Layout: b.Layout, Kinds: builtinKinds, Total: len(b.Nodes)}
	after := map[string][]string{}
	for _, edge := range b.Edges {
		after[edge[0]] = append(after[edge[0]], edge[1])
		c.Edges = append(c.Edges, edge)
	}
	for _, node := range b.Nodes {
		board.known[node.ID] = node.Title
		shown := boardNode{ID: node.ID, Title: node.Title, Kind: node.Kind, Status: node.Status}
		if slices.Contains(spec.Selected, node.ID) {
			shown.After = after[node.ID]
			c.Selected = append(c.Selected, shown)
		} else {
			c.Listed = append(c.Listed, shown)
		}
	}
	return c, board
}

// scoreDraft is the share of the expected properties the draft has, and the
// names of the ones it missed.
func scoreDraft(outcome draftOutcome, expect evalExpect, b evalBoard) (float64, []string) {
	draft := outcome.draft
	var checks, passed int
	var missed []string
	check := func(name string, ok bool) {
		checks++
		if ok {
			passed++
		} else {
			missed = append(missed, name)
		}
	}
	if expect.QuestionsOK && len(draft.Questions) > 0 && len(draft.Nodes) == 0 {
		return 1, nil
	}
	check("valid", !hasErrors(outcome.issues))
	n := len(draft.Nodes)
	if expect.MinNodes > 0 {
		check(fmt.Sprintf("≥%d nodes", expect.MinNodes), n >= expect.MinNodes)
	}
	if expect.MaxNodes > 0 {
		check(fmt.Sprintf("≤%d nodes", expect.MaxNodes), n <= expect.MaxNodes)
	}
	has := func(title string, words []string) bool {
		title = strings.ToLower(title)
		return slices.ContainsFunc(words, func(word string) bool { return strings.Contains(title, strings.ToLower(word)) })
	}
	for _, words := range expect.Keywords {
		check("keyword "+strings.Join(words, "/"), slices.ContainsFunc(draft.Nodes, func(node DraftNode) bool { return has(node.Title, words) }))
	}
	for _, kind := range expect.Kinds {
		check("kind "+kind, slices.ContainsFunc(draft.Nodes, func(node DraftNode) bool { return node.Kind == kind }))
	}
	before := evalPrecedes(draft)
	for _, pair := range expect.Order {
		ok := false
		for _, first := range draft.Nodes {
			for _, then := range draft.Nodes {
				if has(first.Title, pair[0]) && has(then.Title, pair[1]) && before(first.Key, then.Key) {
					ok = true
				}
			}
		}
		check("order "+strings.Join(pair[0], "/")+"→"+strings.Join(pair[1], "/"), ok)
	}
	titles := map[string]string{}
	for _, node := range b.Nodes {
		titles[node.ID] = normalizeTitle(node.Title)
	}
	for _, id := range expect.NoDuplicateOf {
		check("no duplicate of "+id, !slices.ContainsFunc(draft.Nodes, func(node DraftNode) bool {
			return normalizeTitle(node.Title) == titles[id]
		}))
	}
	for _, id := range expect.AttachesTo {
		check("after "+id, slices.ContainsFunc(draft.Nodes, func(node DraftNode) bool {
			return slices.Contains(node.After, existingPrefix+id)
		}))
	}
	if expect.NeedsCode {
		check("needsCodeKnowledge", draft.NeedsCodeKnowledge)
	}
	if checks == 0 {
		return 1, nil
	}
	return float64(passed) / float64(checks), missed
}

// evalPrecedes reports whether a must finish (directly or not) before b.
func evalPrecedes(draft Draft) func(a, b string) bool {
	after := map[string][]string{}
	for _, node := range draft.Nodes {
		after[node.Key] = node.After
	}
	return func(a, b string) bool {
		seen := map[string]bool{}
		queue := slices.Clone(after[b])
		for len(queue) > 0 {
			key := queue[0]
			queue = queue[1:]
			if key == a {
				return true
			}
			if !seen[key] {
				seen[key] = true
				queue = append(queue, after[key]...)
			}
		}
		return false
	}
}

func mean(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	var sum float64
	for _, value := range values {
		sum += value
	}
	return sum / float64(len(values))
}

// The scorer itself, without a model: a draft that meets a case scores 1,
// one that misses scores less and names what it missed.
func TestDraftEvalScoring(t *testing.T) {
	expect := evalExpect{MinNodes: 3, MaxNodes: 6, Keywords: [][]string{{"文件"}, {"測試"}, {"發布", "上線"}},
		Order: [][2][]string{{{"測試"}, {"發布", "上線"}}}, AttachesTo: []string{"n-ui"}, NoDuplicateOf: []string{"n-ui"}}
	b := evalBoard{Nodes: []evalNode{{ID: "n-ui", Title: "登入畫面"}}}
	good := draftOutcome{draft: Draft{Nodes: []DraftNode{
		{Key: "n1", Title: "寫文件", Kind: "task", After: []string{"existing:n-ui"}},
		{Key: "n2", Title: "整合測試", Kind: "task", After: []string{"n1"}},
		{Key: "n3", Title: "發布", Kind: "task", After: []string{"n2"}},
	}}}
	if score, missed := scoreDraft(good, expect, b); score != 1 {
		t.Fatalf("good draft scored %v, missed %v", score, missed)
	}
	bad := draftOutcome{draft: Draft{Nodes: []DraftNode{
		{Key: "n1", Title: "發布", Kind: "task"},
		{Key: "n2", Title: "測試", Kind: "task", After: []string{"n1"}},
		{Key: "n3", Title: "登入 畫面", Kind: "task"},
	}}, issues: []DraftIssue{{Severity: IssueError, Code: IssueCycle}}}
	score, missed := scoreDraft(bad, expect, b)
	for _, want := range []string{"valid", "keyword 文件", "order 測試→發布/上線", "after n-ui", "no duplicate of n-ui"} {
		if !slices.Contains(missed, want) {
			t.Errorf("missed %v lacks %q", missed, want)
		}
	}
	if score >= 0.5 {
		t.Errorf("bad draft scored %v", score)
	}
	if score, _ := scoreDraft(draftOutcome{draft: Draft{Questions: []string{"?"}}}, evalExpect{QuestionsOK: true}, b); score != 1 {
		t.Errorf("questions-ok case scored %v", score)
	}
}

// opencodeProvider answers through `opencode run`: the system prompt and the
// conversation go in as one message, the model's text comes back from the
// JSON event stream. No structured output; the drafter validates anyway.
type opencodeProvider struct {
	model string
	dir   string
}

func (p *opencodeProvider) Name() string  { return "opencode" }
func (p *opencodeProvider) Model() string { return p.model }

func (p *opencodeProvider) Stream(ctx context.Context, req provider.Request, _ func(provider.Delta)) (provider.Response, error) {
	var prompt strings.Builder
	prompt.WriteString("Follow these instructions exactly. Do not use any tools; answer directly.\n\n")
	prompt.WriteString(req.System)
	for _, message := range req.Messages {
		for _, block := range message.Blocks {
			if block.Type != provider.BlockText {
				continue
			}
			if message.Role == provider.RoleAssistant {
				prompt.WriteString("\n\nYour previous answer:\n")
			} else {
				prompt.WriteString("\n\n")
			}
			prompt.WriteString(block.Text)
		}
	}
	prompt.WriteString("\n\nAnswer with the JSON object only.")
	// A provider error such as "Free usage limit reached" is only logged, and
	// opencode keeps retrying, which looks like a stalled model. Errors are
	// logged to stderr and watched, so such a call fails at once and says why.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(ctx, "opencode", "run", "--pure", "--print-logs", "--log-level", "ERROR",
		"--format", "json", "-m", p.model, prompt.String())
	cmd.Dir = p.dir
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return provider.Response{}, err
	}
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Start(); err != nil {
		return provider.Response{}, fmt.Errorf("opencode run: %w", err)
	}
	var fatal string
	logs := bufio.NewScanner(stderr)
	logs.Buffer(make([]byte, 64<<10), 1<<20)
	for logs.Scan() {
		line := logs.Text()
		if i := strings.Index(line, "Free usage limit reached"); i >= 0 && fatal == "" {
			fatal = "OpenCode's free usage limit is reached (all free models share it); wait for it to reset or use another model"
			cancel()
		}
	}
	err = cmd.Wait()
	if fatal != "" {
		return provider.Response{}, errors.New(fatal)
	}
	if err != nil {
		return provider.Response{}, fmt.Errorf("opencode run: %v", err)
	}
	out := stdout.Bytes()
	var text strings.Builder
	var response provider.Response
	scanner := bufio.NewScanner(bytes.NewReader(out))
	scanner.Buffer(make([]byte, 1<<20), 16<<20)
	for scanner.Scan() {
		var event struct {
			Type  string `json:"type"`
			Error *struct {
				Data struct {
					Message string `json:"message"`
				} `json:"data"`
			} `json:"error"`
			Part struct {
				Text   string `json:"text"`
				Tokens struct {
					Input  int `json:"input"`
					Output int `json:"output"`
				} `json:"tokens"`
			} `json:"part"`
		}
		if json.Unmarshal(scanner.Bytes(), &event) != nil {
			continue
		}
		switch event.Type {
		case "text":
			text.WriteString(event.Part.Text)
		case "step_finish":
			response.InputTokens += event.Part.Tokens.Input
			response.OutputTokens += event.Part.Tokens.Output
		case "error":
			if event.Error != nil {
				return provider.Response{}, errors.New("opencode: " + event.Error.Data.Message)
			}
		}
	}
	response.Message = textMessage(strings.TrimSpace(text.String()))
	response.Stop = provider.StopEndTurn
	return response, nil
}
