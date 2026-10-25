package runnerwire

import "encoding/json"

// System One decisions on the runner (W7). A System One model (TypeSafe's
// hosted Jev, or Laya run locally with `laya-serve`) answers typed questions
// about a piece of state — pick one of these options, score on this scale,
// yes or no — with calibrated probabilities in a fraction of a second, and
// generates no text. The autopilot's advisor asks one which agent and model
// should implement a task. As with the copilot's model calls (llm.go), the
// call runs on the owner's runner: the endpoint and any key are its settings
// (`nodevas runner set advisorProvider systemone`), never the hub's.
const (
	// Hub → runner: ask the runner's System One endpoint (Decide).
	TypeDecideRequest = "decide_request"
	// Runner → hub: its answer, or why there is none (DecideDone).
	TypeDecideDone = "decide_done"
)

// CapDecide: the runner answers System One decisions (its hello's Advisor
// names ProviderSystemOne).
const CapDecide = "decide"

// ProviderSystemOne is the advisor provider for a System One endpoint.
const ProviderSystemOne = "systemone"

// DefaultSystemOneBaseURL is where `laya-serve` listens by default.
const DefaultSystemOneBaseURL = "http://127.0.0.1:8000"

// Limits on decisions.
const (
	// MaxDecideStateBytes bounds the state (System One endpoints take about
	// 50,000 characters).
	MaxDecideStateBytes = 64 << 10
	// MaxDecideQuestionsBytes bounds the questions.
	MaxDecideQuestionsBytes = 256 << 10
	// MaxDecideQuestions is the most questions one decision asks.
	MaxDecideQuestions = 64
	// MaxDecideAnswerBytes bounds the answer the runner sends back.
	MaxDecideAnswerBytes = 1 << 20
)

// DecisionRequest is one System One call: the state and the questions, as
// the endpoint takes them ({"<id>": {"type": "choice", "instructions": …,
// "criteria": {…}}, …}). Model is the endpoint's model id ("jev-latest");
// "" leaves it to the endpoint.
type DecisionRequest struct {
	Model     string          `json:"model,omitempty"`
	State     json.RawMessage `json:"state"`
	Questions json.RawMessage `json:"questions"`
}

// DecideRequest asks a runner for a decision.
type DecideRequest struct {
	ID string `json:"id"`
	// AccountID is whose autopilot asked; a runner answers its owner only.
	AccountID string `json:"accountId"`
	// Purpose is LLMPurposeAdvisor.
	Purpose string `json:"purpose"`
	// Model and BaseURL are what the hub expects from the runner's hello;
	// the runner refuses a request that disagrees with its settings.
	Model   string          `json:"model"`
	BaseURL string          `json:"baseUrl"`
	Request DecisionRequest `json:"request"`
}

// DecisionAnswer is one question's answer. Choice questions set Choice,
// Confidence and Probabilities; score questions Score, Confidence, Legend and
// Probabilities; noul (yes/no) questions Noul, the probability of yes.
type DecisionAnswer struct {
	Type          string             `json:"type"`
	Choice        string             `json:"choice,omitempty"`
	Confidence    float64            `json:"confidence,omitempty"`
	Probabilities map[string]float64 `json:"probabilities,omitempty"`
	// Score is the expected level, as the endpoint gives it (a number, or a
	// level's name); Legend maps levels to their names.
	Score  json.RawMessage `json:"score,omitempty"`
	Legend json.RawMessage `json:"legend,omitempty"`
	Noul   *float64        `json:"noul,omitempty"`
}

// DecisionUsage is what a decision cost.
type DecisionUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// DecisionResponse is a System One endpoint's answer.
type DecisionResponse struct {
	Model   string                    `json:"model"`
	Answers map[string]DecisionAnswer `json:"answers"`
	Usage   DecisionUsage             `json:"usage"`
}

// DecideDone is a runner's answer to a DecideRequest: Response, or Error.
type DecideDone struct {
	ID       string            `json:"id"`
	Response *DecisionResponse `json:"response,omitempty"`
	Error    string            `json:"error,omitempty"`
}
