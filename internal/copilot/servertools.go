package copilot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"nodevas/internal/copilot/provider"
	"nodevas/internal/copilot/toolbridge"
	"nodevas/internal/runnerwire"
)

// Copilot-only tools (W7): turning what the person dictated into work on
// their own machine. start_breakdown files the requirement as a goal node and
// dispatches a plan run that breaks it into tasks; start_autopilot hands the
// goal's tasks to the orchestrator.
//
// They are not MCP tools (internal/mcp), so no agent reaches them, through the
// runner proxy or otherwise: agents cannot dispatch runs. Only a copilot turn
// offers them, and only on a server that hands the copilot its router. They
// call the API as the person, not as an in-process agent, because dispatching
// is something only a person may do; that is also why every call asks the
// person first, whatever the permission mode (plan mode refuses them), and
// why a runner still applies its own acceptance rules to what they dispatch.

// serverToolSpec is a copilot-only tool the server runs itself.
type serverToolSpec struct {
	title       string
	description string
	schema      string
	run         func(ctx context.Context, env *serverToolEnv, input json.RawMessage) toolbridge.Result
}

var serverTools = map[string]serverToolSpec{
	"start_breakdown": {
		title: "Break down a requirement",
		description: "File a requirement the person dictated as a goal node and start a plan run on their runner: " +
			"a coding agent reads the repository and breaks the goal into task nodes with dependencies, changing " +
			"no files. Use it only after the person confirmed the requirement. The plan run takes a few minutes; " +
			"its tasks appear under the goal on the board. Pass goalNodeId to break down an existing node instead.",
		schema: `{"type":"object","properties":{` +
			`"requirement":{"type":"string","description":"The requirement, in the person's own words"},` +
			`"title":{"type":"string","description":"Short title for the goal node; derived from the requirement when omitted"},` +
			`"goalNodeId":{"type":"string","description":"An existing node to break down instead of creating one"},` +
			`"runnerId":{"type":"string"},"agent":{"type":"string"},"model":{"type":"string"},` +
			`"repoId":{"type":"string","description":"Repository on the runner; the runner's first when omitted"}},` +
			`"additionalProperties":false}`,
		run: runStartBreakdown,
	},
	"start_autopilot": {
		title: "Start autopilot",
		description: "Start the autopilot on a goal node: it dispatches the goal's tasks in dependency order to the " +
			"person's runner, each through implement, review and merge, and records every task's stages. Use it " +
			"only after the person agreed to the task breakdown. Review defaults to a person approving each pull " +
			"request; reviewMode agents lets reviewer agents approve by quorum. Who implements: the implement " +
			"chain when given, else the advisor; without either, the person's runner's own advisor (System One, " +
			"then its fast model) when it has one, else the routing rules, which fall back to the first " +
			"installed agent's default model.",
		schema: `{"type":"object","properties":{` +
			`"goalNodeId":{"type":"string"},` +
			`"maxParallel":{"type":"integer","minimum":1,"maximum":8},` +
			`"reviewMode":{"type":"string","enum":["none","agents","human","agents+human"]},` +
			`"reviewers":{"type":"array","maxItems":5,"items":{"type":"object","properties":{` +
			`"agent":{"type":"string"},"model":{"type":"string"}},"required":["agent","model"],"additionalProperties":false}},` +
			`"quorum":{"type":"integer","minimum":0},` +
			`"anyEditor":{"type":"boolean","description":"Let anyone who may edit the project give the person's review, not only the person starting it, the runner's owner or an admin"},` +
			`"autoMerge":{"type":"boolean","description":"Merge approved pull requests once CI is green; needs reviewMode human or agents+human (a person approves each pull request)"},` +
			`"implement":{"type":"array","maxItems":3,"description":"Who implements every task: the first, then fallbacks","items":{"type":"object","properties":{` +
			`"agent":{"type":"string"},"model":{"type":"string"}},"required":["agent","model"],"additionalProperties":false}},` +
			`"advisor":{"type":"string","enum":["rules","llm","systemone"],"description":"Who picks the implementer per task when no implement chain is given: rules (routing rules), llm (the person's fast advisor model), systemone (a System One decision model such as Laya or Jev on the person's runner)"},` +
			`"runnerId":{"type":"string"},"repoId":{"type":"string"}},` +
			`"required":["goalNodeId"],"additionalProperties":false}`,
		run: runStartAutopilot,
	},
}

// serverToolNames lists serverTools in a stable order.
func serverToolNames() []string {
	names := make([]string, 0, len(serverTools))
	for name := range serverTools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// serverToolEnv is how a turn's server tools reach the API: in-process, as
// the person whose turn it is, against the conversation's project.
type serverToolEnv struct {
	handler    http.Handler
	host       string
	cookie     string
	remoteAddr string
	project    string
	accountID  string
	// followup, when set, records a plan run start_breakdown started, so the
	// copilot follows up in the conversation when it ends (followup.go).
	followup func(goalNodeID, runID string)
}

// maxServerToolResponse bounds a response a server tool reads.
const maxServerToolResponse = 4 << 20

// do serves one API request through the router and decodes a 2xx JSON
// answer into out. A non-2xx answer is an error carrying the API's message.
func (e *serverToolEnv) do(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader = http.NoBody
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	target := path
	if e.project != "" {
		sep := "?"
		if strings.Contains(path, "?") {
			sep = "&"
		}
		target += sep + "project=" + url.QueryEscape(e.project)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return err
	}
	req.RequestURI = req.URL.RequestURI()
	req.Host = e.host
	if req.Host == "" {
		req.Host = "localhost"
	}
	req.RemoteAddr = e.remoteAddr
	if req.RemoteAddr == "" {
		req.RemoteAddr = "127.0.0.1:0"
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if e.cookie != "" {
		req.Header.Set("Cookie", e.cookie)
	}
	recorder := httptest.NewRecorder()
	e.handler.ServeHTTP(recorder, req)
	data := recorder.Body.Bytes()
	if len(data) > maxServerToolResponse {
		return errors.New("the server's answer is too large")
	}
	if recorder.Code < 200 || recorder.Code > 299 {
		var problem struct {
			Error   string `json:"error"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal(data, &problem)
		text := strings.TrimSpace(problem.Error)
		if text == "" {
			text = strings.TrimSpace(problem.Message)
		}
		if text == "" {
			text = http.StatusText(recorder.Code)
		}
		return fmt.Errorf("%s %s: %s (HTTP %d)", method, path, truncateUTF8(text, 500), recorder.Code)
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("%s %s: unreadable answer: %v", method, path, err)
		}
	}
	return nil
}

func toolError(format string, args ...any) toolbridge.Result {
	return toolbridge.Result{Output: fmt.Sprintf(format, args...), IsError: true}
}

func toolOK(v any) toolbridge.Result {
	raw, err := json.Marshal(v)
	if err != nil {
		return toolError("could not encode the result: %v", err)
	}
	return toolbridge.Result{Output: string(raw)}
}

// Bounds on what start_breakdown files.
const (
	maxRequirementBytes = 16 << 10
	maxGoalTitleRunes   = 80
)

// agentPreference is the order agents are picked in when the person named
// none, as the dispatch dialog lists them.
var agentPreference = []string{"claude", "codex", "opencode", "gemini", "antigravity"}

// dispatchTarget is where a run goes.
type dispatchTarget struct {
	runner runnerwire.Runner
	agent  string
	model  string
	repo   runnerwire.Repo
}

// pickTarget chooses the person's online runner, agent, model and repo,
// honouring whatever the call named.
func pickTarget(ctx context.Context, env *serverToolEnv, runnerID, agent, model, repoID string) (dispatchTarget, error) {
	var list runnerwire.ListResponse
	if err := env.do(ctx, http.MethodGet, "/api/runners", nil, &list); err != nil {
		return dispatchTarget{}, err
	}
	var candidates []runnerwire.Runner
	for _, runner := range list.Runners {
		if !runner.Online || runner.Info == nil || !slices.Contains(runner.Info.Capabilities, runnerwire.CapHeadless) {
			continue
		}
		if env.accountID != "" && runner.OwnerID != env.accountID {
			continue
		}
		if runnerID != "" && runner.ID != runnerID {
			continue
		}
		candidates = append(candidates, runner)
	}
	if len(candidates) == 0 {
		if runnerID != "" {
			return dispatchTarget{}, fmt.Errorf("runner %q is not one of your online runners", runnerID)
		}
		return dispatchTarget{}, errors.New("none of your runners is online: start `nodevas runner` on your PC")
	}
	for _, runner := range candidates {
		target := dispatchTarget{runner: runner}
		if !pickRepo(&target, repoID) || !pickAgent(&target, agent, model) {
			continue
		}
		return target, nil
	}
	switch {
	case repoID != "":
		return dispatchTarget{}, fmt.Errorf("no online runner of yours has repository %q", repoID)
	case agent != "":
		return dispatchTarget{}, fmt.Errorf("no online runner of yours has %s installed with model %q", agent, model)
	}
	return dispatchTarget{}, errors.New("your online runners have no repository or no installed agent")
}

func pickRepo(target *dispatchTarget, repoID string) bool {
	for _, repo := range target.runner.Info.Repos {
		if repoID == "" || repo.ID == repoID || strings.EqualFold(repo.Name, repoID) {
			target.repo = repo
			return true
		}
	}
	return false
}

func pickAgent(target *dispatchTarget, agent, model string) bool {
	installed := map[string]runnerwire.Agent{}
	for _, a := range target.runner.Info.Agents {
		if a.Installed {
			installed[strings.ToLower(a.Kind)] = a
		}
	}
	order := agentPreference
	if agent != "" {
		order = []string{strings.ToLower(agent)}
	}
	for _, kind := range order {
		a, ok := installed[kind]
		if !ok {
			continue
		}
		chosen := ""
		for _, m := range a.Models {
			switch {
			case model != "" && m.ID == model:
				chosen = m.ID
			case model == "" && m.Default && chosen == "":
				chosen = m.ID
			}
		}
		if chosen == "" && model == "" && len(a.Models) > 0 {
			chosen = a.Models[0].ID
		}
		if chosen == "" {
			continue
		}
		target.agent, target.model = a.Kind, chosen
		return true
	}
	return false
}

func goalTitle(title, requirement string) string {
	title = strings.Join(strings.Fields(title), " ")
	if title == "" {
		first, _, _ := strings.Cut(strings.TrimSpace(requirement), "\n")
		title = strings.Join(strings.Fields(first), " ")
	}
	if utf8.RuneCountInString(title) > maxGoalTitleRunes {
		title = strings.TrimSpace(string([]rune(title)[:maxGoalTitleRunes])) + "…"
	}
	return title
}

func runStartBreakdown(ctx context.Context, env *serverToolEnv, input json.RawMessage) toolbridge.Result {
	var in struct {
		Requirement string `json:"requirement"`
		Title       string `json:"title"`
		GoalNodeID  string `json:"goalNodeId"`
		RunnerID    string `json:"runnerId"`
		Agent       string `json:"agent"`
		Model       string `json:"model"`
		RepoID      string `json:"repoId"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return toolError("unreadable arguments: %v", err)
	}
	in.Requirement = strings.TrimSpace(in.Requirement)
	in.GoalNodeID = strings.TrimSpace(in.GoalNodeID)
	if in.GoalNodeID == "" && in.Requirement == "" {
		return toolError("give the requirement (or the goalNodeId of an existing node)")
	}
	if len(in.Requirement) > maxRequirementBytes || !utf8.ValidString(in.Requirement) {
		return toolError("the requirement is too long (at most %d bytes)", maxRequirementBytes)
	}
	target, err := pickTarget(ctx, env, strings.TrimSpace(in.RunnerID), strings.TrimSpace(in.Agent),
		strings.TrimSpace(in.Model), strings.TrimSpace(in.RepoID))
	if err != nil {
		return toolError("Not started: %v", err)
	}

	goal, title, created := in.GoalNodeID, "", false
	if goal == "" {
		title = goalTitle(in.Title, in.Requirement)
		var node struct {
			ID string `json:"id"`
		}
		err := env.do(ctx, http.MethodPost, "/api/nodes", map[string]any{
			"title": title, "kind": "task", "tags": []string{"goal"}, "body": in.Requirement,
		}, &node)
		if err != nil {
			return toolError("Could not create the goal node: %v", err)
		}
		if node.ID == "" {
			return toolError("Could not create the goal node: the server returned no id")
		}
		goal, created = node.ID, true
	}

	var run runnerwire.Run
	err = env.do(ctx, http.MethodPost, "/api/runs", map[string]any{
		"nodeId": goal, "runnerId": target.runner.ID, "agent": target.agent, "model": target.model,
		"repoId": target.repo.ID, "kind": runnerwire.JobPlan,
	}, &run)
	if err != nil {
		text := "Could not start the plan run: %v"
		if created {
			text = "Created the goal node " + goal + " but could not start the plan run: %v"
		}
		return toolError(text, err)
	}
	note := "The plan run reads the repository and adds task nodes under the goal; it may take a few minutes. " +
		"Its tasks need the person's review before the autopilot starts."
	if env.followup != nil && run.ID != "" {
		env.followup(goal, run.ID)
		note += " When it ends, this conversation gets a follow-up by itself: its tasks and the question whether " +
			"to start the autopilot. Tell the person you will come back to them then; they need not ask."
	}
	result := map[string]any{
		"goalNodeId": goal, "goalCreated": created, "runId": run.ID, "runStatus": run.Status,
		"runner": target.runner.Name, "agent": target.agent, "model": target.model, "repo": target.repo.Name,
		"note": note,
	}
	if title != "" {
		result["goalTitle"] = title
	}
	if repos := target.runner.Info.Repos; strings.TrimSpace(in.RepoID) == "" && len(repos) > 1 {
		names := make([]string, 0, len(repos))
		for _, repo := range repos {
			names = append(names, repo.Name)
		}
		result["otherRepos"] = names
	}
	return toolOK(result)
}

// Defaults for a policy the person did not spell out: two tasks at a time,
// two review rounds, a person approving every pull request, no auto-merge.
const (
	defaultAutopilotParallel = 2
	defaultAutopilotRounds   = 2
)

func runStartAutopilot(ctx context.Context, env *serverToolEnv, input json.RawMessage) toolbridge.Result {
	var in struct {
		GoalNodeID  string                  `json:"goalNodeId"`
		MaxParallel int                     `json:"maxParallel"`
		ReviewMode  string                  `json:"reviewMode"`
		Reviewers   []runnerwire.AgentModel `json:"reviewers"`
		Quorum      int                     `json:"quorum"`
		AnyEditor   bool                    `json:"anyEditor"`
		AutoMerge   bool                    `json:"autoMerge"`
		Implement   []runnerwire.AgentModel `json:"implement"`
		Advisor     string                  `json:"advisor"`
		RunnerID    string                  `json:"runnerId"`
		RepoID      string                  `json:"repoId"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return toolError("unreadable arguments: %v", err)
	}
	goal := strings.TrimSpace(in.GoalNodeID)
	if goal == "" {
		return toolError("give the goalNodeId")
	}
	policy := runnerwire.AutopilotPolicy{
		MaxParallel: in.MaxParallel, MaxRounds: defaultAutopilotRounds, AutoMerge: in.AutoMerge,
		RunnerID: strings.TrimSpace(in.RunnerID), RepoID: strings.TrimSpace(in.RepoID),
		Review: runnerwire.ReviewPolicy{Mode: in.ReviewMode, Reviewers: in.Reviewers, Quorum: in.Quorum,
			AnyEditor: in.AnyEditor},
	}
	if policy.MaxParallel <= 0 {
		policy.MaxParallel = defaultAutopilotParallel
	}
	policy.MaxParallel = min(policy.MaxParallel, runnerwire.MaxAutopilotParallel)
	switch policy.Review.Mode {
	case "":
		policy.Review.Mode = runnerwire.ReviewHuman
	case runnerwire.ReviewNone, runnerwire.ReviewHuman, runnerwire.ReviewAgents, runnerwire.ReviewAgentsHuman:
	default:
		return toolError("reviewMode must be none, agents, human or agents+human")
	}
	if (policy.Review.Mode == runnerwire.ReviewAgents || policy.Review.Mode == runnerwire.ReviewAgentsHuman) &&
		len(policy.Review.Reviewers) == 0 {
		return toolError("reviewMode %s needs reviewers (agent and model each); ask the person which", policy.Review.Mode)
	}
	// Merging what no person approved is the person's own choice, made in
	// the Autopilot panel; the copilot cannot opt in for them.
	if policy.AutoMerge && policy.Review.Mode != runnerwire.ReviewHuman && policy.Review.Mode != runnerwire.ReviewAgentsHuman {
		return toolError("autoMerge needs reviewMode human or agents+human: a person approves each pull request before it merges")
	}
	if len(policy.Review.Reviewers) > runnerwire.MaxReviewers {
		return toolError("at most %d reviewers", runnerwire.MaxReviewers)
	}
	if len(in.Implement) > 3 {
		return toolError("at most 3 implementers (the first, then fallbacks)")
	}
	policy.Implement = in.Implement
	picker := ""
	switch in.Advisor {
	case "":
		if len(policy.Implement) == 0 {
			// Nothing said: the advisor the person set up on their runner,
			// rather than the routing rules' last resort (the first installed
			// agent's default model, often the most expensive one).
			policy.Advisor, picker = defaultAdvisor(ctx, env, policy.RunnerID)
		}
	case "rules":
		policy.Advisor = runnerwire.AdvisorRules
	case "llm":
		policy.Advisor = runnerwire.AdvisorLLM
	case "systemone":
		policy.Advisor = runnerwire.AdvisorSystemOne
	default:
		return toolError("advisor must be rules, llm or systemone")
	}
	switch {
	case len(policy.Implement) > 0:
		picker = "the implement chain: " + chainText(policy.Implement)
	case picker != "":
	case policy.Advisor == runnerwire.AdvisorSystemOne:
		picker = "the System One advisor on the runner, the routing rules if it cannot decide"
	case policy.Advisor == runnerwire.AdvisorLLM:
		picker = "the fast advisor model on the runner, the routing rules if it cannot decide"
	default:
		picker = "the routing rules, else the first installed agent's default model"
	}

	var answer struct {
		ID     string                   `json:"id"`
		Status string                   `json:"status"`
		Run    *runnerwire.AutopilotRun `json:"run"`
	}
	err := env.do(ctx, http.MethodPost, "/api/autopilot", runnerwire.CreateAutopilotRequest{
		GoalNodeID: goal, Policy: policy,
	}, &answer)
	if err != nil {
		return toolError("Could not start the autopilot: %v", err)
	}
	id, status := answer.ID, answer.Status
	if answer.Run != nil {
		id, status = answer.Run.ID, answer.Run.Status
	}
	return toolOK(map[string]any{
		"autopilotId": id, "status": status, "goalNodeId": goal,
		"review": policy.Review.Mode, "maxParallel": policy.MaxParallel, "autoMerge": policy.AutoMerge,
		"advisor": policy.Advisor, "implementedBy": picker,
		"note": "Tasks are dispatched in dependency order; their stages show on the goal's Autopilot panel. " +
			"Tell the person who implements (implementedBy).",
	})
}

// defaultAdvisor is the advisor a person's runner offers, best first: a
// System One decision model, then a fast advisor model, else the routing
// rules. picker describes the choice for the tool's result.
func defaultAdvisor(ctx context.Context, env *serverToolEnv, runnerID string) (advisor, picker string) {
	var list runnerwire.ListResponse
	if err := env.do(ctx, http.MethodGet, "/api/runners", nil, &list); err != nil {
		return runnerwire.AdvisorRules, ""
	}
	decide, llm := false, false
	for _, runner := range list.Runners {
		if !runner.Online || runner.Info == nil || (env.accountID != "" && runner.OwnerID != env.accountID) ||
			(runnerID != "" && runner.ID != runnerID) {
			continue
		}
		decide = decide || slices.Contains(runner.Info.Capabilities, runnerwire.CapDecide)
		llm = llm || slices.Contains(runner.Info.Capabilities, runnerwire.CapLLMAdvisor)
	}
	switch {
	case decide:
		return runnerwire.AdvisorSystemOne, "the System One advisor on the runner (chosen because none was named), " +
			"the routing rules if it cannot decide"
	case llm:
		return runnerwire.AdvisorLLM, "the fast advisor model on the runner (chosen because none was named), " +
			"the routing rules if it cannot decide"
	}
	return runnerwire.AdvisorRules, ""
}

// chainText is an implement chain as "agent/model, agent/model".
func chainText(chain []runnerwire.AgentModel) string {
	parts := make([]string, 0, len(chain))
	for _, am := range chain {
		parts = append(parts, am.Agent+"/"+am.Model)
	}
	return strings.Join(parts, ", ")
}

// serverToolDefs are the provider tools for the server tools, in order.
func serverToolDefs() []provider.Tool {
	names := serverToolNames()
	tools := make([]provider.Tool, 0, len(names))
	for _, name := range names {
		spec := serverTools[name]
		tools = append(tools, provider.Tool{Name: name, Description: spec.description, Schema: json.RawMessage(spec.schema)})
	}
	return tools
}
