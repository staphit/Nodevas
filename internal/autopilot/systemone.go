package autopilot

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"nodevas/internal/locale"
	"nodevas/internal/runnerwire"
)

// The System One advisor (W7, AdvisorSystemOne): a System One model —
// TypeSafe's hosted Jev, or Laya run locally — answers "which of these agents
// and models should implement this task?" as a typed choice with calibrated
// probabilities, in a fraction of a second, and generates no text to parse.
// It runs on the owner's runner (`nodevas runner set advisorProvider
// systemone`), through the same relay as the LLM advisor, so the hub holds no
// key for it. Its answer can only ever select among the candidates it was
// given; anything else is an error and the orchestrator falls back to the
// rules advisor.

// DecideRelay reaches the System One advisor on the owner's runner.
// *runners.LLMRelay satisfies it.
type DecideRelay interface {
	Decide(ctx context.Context, accountID string, req runnerwire.DecisionRequest) (runnerwire.DecisionResponse, error)
}

// Bounds on a System One decision.
const (
	// DefaultSystemOneTimeout bounds one decision end to end; System One
	// models answer in 0.1-0.5s, a cold local Laya in a few seconds.
	DefaultSystemOneTimeout = 10 * time.Second
	// maxSystemOneCandidates bounds the options of the implementer question
	// (Laya takes 100, Jev 255; fewer keep the choice sharp).
	maxSystemOneCandidates = 32
	// maxSystemOneTaskBytes bounds the task text in the state.
	maxSystemOneTaskBytes = 8 << 10
	// minSystemOneFallback is the least probability a fallback needs.
	minSystemOneFallback = 0.05
	// minSystemOneConfidence is how sure the implementer choice must be to be
	// taken as it is. Below it the task's complexity picks a tier instead:
	// a local Laya (0.4) spreads its choice over every option (confidence
	// about 0.05) while its complexity score still tells tasks apart.
	minSystemOneConfidence = 0.35
)

// Model tiers, cheapest first.
const (
	tierUnknown = iota - 1
	tierFast
	tierBalanced
	tierStrong
)

var tierNames = map[int]string{tierFast: "fast", tierBalanced: "balanced", tierStrong: "strong"}

// complexityLevels is the complexity question's scale, mildest first.
var complexityLevels = []string{"trivial", "small", "medium", "large", "risky"}

const implementerInstructions = "Which coding agent and model should implement this task? Prefer fast, cheap models " +
	"for small mechanical edits and stronger models for complex, cross-cutting or risky changes."

// SystemOneAdvisor is an Advisor backed by the owner's System One endpoint.
type SystemOneAdvisor struct {
	relay DecideRelay
	// Timeout overrides DefaultSystemOneTimeout; tests shorten it.
	Timeout time.Duration
}

// NewSystemOneAdvisor builds a System One advisor over relay.
func NewSystemOneAdvisor(relay DecideRelay) *SystemOneAdvisor { return &SystemOneAdvisor{relay: relay} }

// systemOneQuestion is one question as the endpoint takes it.
type systemOneQuestion struct {
	Type         string `json:"type"`
	Instructions string `json:"instructions"`
	Criteria     any    `json:"criteria,omitempty"`
}

// systemOneDecision is the request for a task, and how its options map back.
type systemOneDecision struct {
	req     runnerwire.DecisionRequest
	options map[string]advisorCandidate // "o1" → candidate
}

// buildSystemOneDecision asks the implementer choice (and the task's
// complexity, for the reason) over the candidates.
func buildSystemOneDecision(in ChooseInput, candidates []advisorCandidate) (systemOneDecision, error) {
	if len(candidates) > maxSystemOneCandidates {
		candidates = candidates[:maxSystemOneCandidates]
	}
	options := make(map[string]advisorCandidate, len(candidates))
	criteria := make(map[string]string, len(candidates))
	for i, c := range candidates {
		key := "o" + strconv.Itoa(i+1)
		options[key] = c
		criteria[key] = candidateDescription(c)
	}
	var state strings.Builder
	state.WriteString("Task: ")
	state.WriteString(advisorOneLine(in.Title, 300))
	body := strings.TrimSpace(in.Body)
	if body == "" {
		body = "(no description)"
	}
	state.WriteString("\n\n")
	state.WriteString(advisorTruncate(body, maxSystemOneTaskBytes))
	if mode := in.Policy.Review.Mode; mode != "" && mode != runnerwire.ReviewNone {
		fmt.Fprintf(&state, "\n\nThe pull request is reviewed (%s) before it is merged.", advisorOneLine(mode, 40))
	}
	stateJSON, err := json.Marshal(state.String())
	if err != nil {
		return systemOneDecision{}, err
	}
	questions, err := json.Marshal(map[string]systemOneQuestion{
		"implementer": {Type: "choice", Instructions: implementerInstructions, Criteria: criteria},
		"complexity": {Type: "score", Instructions: "How complex and risky is this change to implement?",
			Criteria: complexityLevels},
	})
	if err != nil {
		return systemOneDecision{}, err
	}
	return systemOneDecision{
		req:     runnerwire.DecisionRequest{State: stateJSON, Questions: questions},
		options: options,
	}, nil
}

// Choose asks the owner's System One endpoint who should implement the task.
func (a *SystemOneAdvisor) Choose(ctx context.Context, in ChooseInput) (Choice, error) {
	if a == nil || a.relay == nil {
		return Choice{}, locale.Errorf("no System One advisor is reachable from this server", "這個伺服器連不到任何 System One 顧問")
	}
	candidates := advisorCandidates(in.Agents)
	if len(candidates) == 0 {
		return Choice{}, errNoAgent
	}
	d, err := buildSystemOneDecision(in, candidates)
	if err != nil {
		return Choice{}, err
	}
	timeout := a.Timeout
	if timeout <= 0 {
		timeout = DefaultSystemOneTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	resp, err := a.relay.Decide(ctx, in.OwnerID, d.req)
	if err != nil {
		return Choice{}, err
	}
	return d.choice(resp)
}

// choice turns the endpoint's answer into an implement chain: the options by
// probability, at most MaxAdvisorChain, a fallback only when it has at least
// minSystemOneFallback. An answer that names no option given is unusable.
func (d systemOneDecision) choice(resp runnerwire.DecisionResponse) (Choice, error) {
	answer, ok := resp.Answers["implementer"]
	if !ok {
		return Choice{}, locale.Errorf("%w: no implementer answer", "%w：沒有實作者的回答", ErrAdvisorAnswer)
	}
	var order []rankedOption
	for key, p := range answer.Probabilities {
		if _, known := d.options[key]; known && !math.IsNaN(p) {
			order = append(order, rankedOption{key, p})
		}
	}
	if len(order) == 0 {
		if _, known := d.options[answer.Choice]; !known {
			return Choice{}, locale.Errorf("%w: it chose %q, not one of the options", "%w：它選了「%s」，不是選項之一", ErrAdvisorAnswer, answer.Choice)
		}
		order = append(order, rankedOption{answer.Choice, 1})
	}
	sort.SliceStable(order, func(i, j int) bool {
		if order[i].p != order[j].p {
			return order[i].p > order[j].p
		}
		// The endpoint's own choice wins a tie, then the option order.
		if (order[i].key == answer.Choice) != (order[j].key == answer.Choice) {
			return order[i].key == answer.Choice
		}
		return optionIndex(order[i].key) < optionIndex(order[j].key)
	})
	var chain []runnerwire.AgentModel
	for i, r := range order {
		if len(chain) == MaxAdvisorChain || (i > 0 && r.p < minSystemOneFallback) {
			break
		}
		c := d.options[r.key]
		chain = append(chain, runnerwire.AgentModel{Agent: c.agent, Model: c.model})
	}
	model := resp.Model
	if model == "" {
		model = "System One"
	}
	level, conf, hasLevel := complexityOf(resp.Answers["complexity"])
	if answer.Confidence < minSystemOneConfidence && hasLevel {
		if tiered, reason, ok := d.byComplexity(order, resp.Answers["complexity"], level); ok {
			return Choice{Implement: tiered, Reason: fmt.Sprintf("System One (%s): %s; its own pick was unsure (conf %.2f)",
				model, reason, answer.Confidence)}, nil
		}
	}
	top := d.options[order[0].key]
	reason := fmt.Sprintf("System One (%s): %s/%s p=%.2f", model, top.agent, top.model, order[0].p)
	if hasLevel {
		reason += fmt.Sprintf(", complexity %s (conf %.2f)", level, conf)
	}
	return Choice{Implement: chain, Reason: reason}, nil
}

// byComplexity picks the implementer from the task's complexity: trivial and
// small tasks to the fast tier, medium to the balanced one, large and risky
// to the strong one; within a tier in the order the choice ranked them, then
// one fallback from the next tier up (down from the strongest). Candidates
// whose tier the model's name does not tell are left out. ok is false when
// no candidate has a known tier.
func (d systemOneDecision) byComplexity(order []rankedOption, answer runnerwire.DecisionAnswer, level string) ([]runnerwire.AgentModel, string, bool) {
	score := complexityScore(answer, level)
	want := tierStrong
	switch {
	case score < 1.75:
		want = tierFast
	case score < 2.4:
		want = tierBalanced
	}
	byTier := map[int][]advisorCandidate{}
	for _, r := range order {
		c := d.options[r.key]
		if t := modelTier(c.model); t != tierUnknown {
			byTier[t] = append(byTier[t], c)
		}
	}
	// The wanted tier, then the nearest others: up first, as a stronger model
	// can do a smaller task but not the other way round.
	var tiers []int
	for _, t := range []int{want, want + 1, want - 1, want + 2, want - 2} {
		if t >= tierFast && t <= tierStrong {
			tiers = append(tiers, t)
		}
	}
	var chain []runnerwire.AgentModel
	got := tierUnknown
	for _, t := range tiers {
		list := byTier[t]
		if len(list) == 0 {
			continue
		}
		if got == tierUnknown {
			got = t
			chain = append(chain, runnerwire.AgentModel{Agent: list[0].agent, Model: list[0].model})
			continue
		}
		chain = append(chain, runnerwire.AgentModel{Agent: list[0].agent, Model: list[0].model})
		break
	}
	if got == tierUnknown {
		return nil, "", false
	}
	return chain, fmt.Sprintf("complexity %s (score %.2f) → %s tier: %s/%s",
		level, score, tierNames[got], chain[0].Agent, chain[0].Model), true
}

// complexityScore is the complexity as a number on complexityLevels' scale.
func complexityScore(a runnerwire.DecisionAnswer, level string) float64 {
	var n float64
	if json.Unmarshal(a.Score, &n) == nil && !math.IsNaN(n) {
		return n
	}
	for i, l := range complexityLevels {
		if strings.EqualFold(l, level) {
			return float64(i)
		}
	}
	return 2
}

// modelTier is a model's tier when its name tells, else tierUnknown.
func modelTier(model string) int {
	m := strings.ToLower(model)
	switch {
	case m == "" || m == "default":
		return tierUnknown
	case strings.Contains(m, "haiku"), strings.HasSuffix(m, "-free"), strings.Contains(m, "flash"),
		strings.Contains(m, "mini"), strings.Contains(m, "lite"), strings.HasSuffix(m, "-low"):
		return tierFast
	case strings.Contains(m, "sonnet"), strings.HasSuffix(m, "-medium"):
		return tierBalanced
	case strings.Contains(m, "opus"), strings.Contains(m, "fable"), strings.Contains(m, "mythos"),
		strings.Contains(m, "argon"), strings.Contains(m, "-pro"), strings.HasSuffix(m, "-high"):
		return tierStrong
	}
	return tierUnknown
}

// rankedOption is an option and its probability.
type rankedOption struct {
	key string
	p   float64
}

// optionIndex is an option key's position ("o3" → 3).
func optionIndex(key string) int {
	n, err := strconv.Atoi(strings.TrimPrefix(key, "o"))
	if err != nil {
		return math.MaxInt
	}
	return n
}

// complexityOf reads the complexity score as a level's name. The score is a
// level's name, or a number: an index into the scale, read through the
// legend when there is one. Without a score, the most probable level.
func complexityOf(a runnerwire.DecisionAnswer) (string, float64, bool) {
	if a.Type == "" && len(a.Score) == 0 && len(a.Probabilities) == 0 {
		return "", 0, false
	}
	var name string
	if json.Unmarshal(a.Score, &name) == nil && name != "" {
		return name, a.Confidence, true
	}
	var n float64
	if json.Unmarshal(a.Score, &n) == nil && !math.IsNaN(n) {
		i := int(math.Round(n))
		var legend map[string]string
		if json.Unmarshal(a.Legend, &legend) == nil {
			if label, ok := legend[strconv.Itoa(i)]; ok && label != "" {
				return label, a.Confidence, true
			}
		}
		var list []string
		if json.Unmarshal(a.Legend, &list) == nil && i >= 0 && i < len(list) {
			return list[i], a.Confidence, true
		}
		if i >= 0 && i < len(complexityLevels) {
			return complexityLevels[i], a.Confidence, true
		}
	}
	best, bestP := "", -1.0
	for level, p := range a.Probabilities {
		if p > bestP || (p == bestP && level < best) {
			best, bestP = level, p
		}
	}
	if best == "" {
		return "", 0, false
	}
	return best, a.Confidence, true
}

// candidateDescription describes an agent and model for the implementer
// question: the agent's product name and the model, plus what is commonly
// known of the model's cost and strength. Unknown models are just named.
func candidateDescription(c advisorCandidate) string {
	agent := map[string]string{
		"claude": "Claude Code", "codex": "Codex", "gemini": "Gemini CLI",
		"opencode": "OpenCode", "antigravity": "Antigravity CLI",
	}[strings.ToLower(c.agent)]
	if agent == "" {
		agent = c.agent
	}
	model := c.model
	if c.label != "" && c.label != c.model {
		model = c.label + " (" + c.model + ")"
	}
	text := agent + " with model " + model
	if hint := modelHint(c.agent, c.model); hint != "" {
		text += " — " + hint
	}
	return text
}

// modelHint is a short, honest note on a model's place in its family, when
// its name says.
func modelHint(agent, model string) string {
	m := strings.ToLower(model)
	switch {
	case m == "default":
		return "the agent's own default model"
	case strings.Contains(m, "fable"), strings.Contains(m, "mythos"):
		return "most capable, slowest and most expensive"
	case strings.Contains(m, "opus"):
		return "strong for complex work, slower and more expensive"
	case strings.Contains(m, "sonnet"):
		return "balanced speed, cost and capability"
	case strings.Contains(m, "haiku"):
		return "fast and cheap, for small well-defined edits"
	case strings.HasSuffix(m, "-free") || strings.Contains(m, "flash") || strings.Contains(m, "mini") ||
		strings.Contains(m, "lite") || strings.HasSuffix(m, "-low"):
		return "fast and cheap, for small well-defined edits"
	case strings.Contains(m, "argon"), strings.Contains(m, "-pro"), strings.HasSuffix(m, "-high"):
		return "strong for complex work, slower"
	}
	return ""
}
