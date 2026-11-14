package runnerwire

// Offer asks: why a runner has not started an offered run yet, and where
// the answer is given.
//
//	hub    → runner  offer        {job}
//	runner → hub     offer_ask    {offerAsk}     the runner is asking; Where says where
//	hub    → runner  offer_answer {offerAnswer}  built-in runner only (CapAppAccept)
//	runner → hub     accept | decline {code}
//
// Every runner that sends offer_ask advertises CapOfferAsk. Only the built-in
// runner of a single-user hub (Welcome.BuiltIn) asks in the app and takes
// offer_answer; it advertises CapAppAccept. A classic runner asks on its own
// PC (Where AskOnRunner) and drops any offer_answer, so a hub can never
// approve a run on someone else's machine. The runner resends pending asks
// after a reconnect; the hub keeps them in memory only.
const (
	// Runner → hub: why the runner is asking, and where. Envelope.OfferAsk.
	TypeOfferAsk = "offer_ask"
	// Hub → runner: the owner's answer from the app. Envelope.OfferAnswer.
	// Built-in runner only.
	TypeOfferAnswer = "offer_answer"
	// CapOfferAsk: the runner sends offer_ask (every new runner).
	CapOfferAsk = "offer-ask"
	// CapAppAccept: the runner takes offer_answer (built-in runner only).
	CapAppAccept = "app-accept"

	// OfferAsk.Where values.
	AskInApp    = "app"    // answer in the app
	AskOnRunner = "runner" // on the runner's PC (tray or terminal)

	// AskReason.Code values.
	AskShellAgent  = "shell_agent"   // Agent, Setting
	AskEveryRun    = "ask_every_run" // the owner asked to confirm every run
	AskNotOnList   = "not_on_list"   // team: Missing = repo|agent|mode
	AskNeedsStepUp = "needs_step_up" // team
	AskVerifyTests = "verify_tests"  // team

	// AskReason.Missing values (AskNotOnList).
	AskMissingRepo  = "repo"
	AskMissingAgent = "agent"
	AskMissingMode  = "mode"

	// AppAskTimeoutSeconds is how long the built-in runner waits for an
	// answer in the app. A classic runner keeps its terminal timeout.
	AppAskTimeoutSeconds = 30 * 60

	// Limits the hub enforces on an offer_ask.
	MaxAskReasons    = 8
	MaxAskFieldBytes = 256
)

// AskReason is one reason the runner asks. The web turns Code (with Agent,
// Setting and Missing) into words; the runner never sends display text.
type AskReason struct {
	Code  string `json:"code"`
	Agent string `json:"agent,omitempty"`
	// Setting is the agent's permission setting that made it ask, e.g.
	// "codexSandbox=danger-full-access".
	Setting string `json:"setting,omitempty"`
	// Missing is what is not on the auto-accept list (AskNotOnList).
	Missing string `json:"missing,omitempty"`
}

// OfferAsk is Envelope.OfferAsk.
type OfferAsk struct {
	RunID   string      `json:"runId"`
	Where   string      `json:"where"`
	Reasons []AskReason `json:"reasons"`
	// ExpiresAt is when the runner gives up waiting, RFC3339.
	ExpiresAt string `json:"expiresAt"`
	// Machine is the runner's hostname, for "waiting on <PC>".
	Machine string `json:"machine,omitempty"`
}

// OfferAnswer is Envelope.OfferAnswer. AccountID is who answered; the
// runner takes it from its owner only.
type OfferAnswer struct {
	RunID     string `json:"runId"`
	AccountID string `json:"accountId"`
	Start     bool   `json:"start"`
	// AlwaysAllowShell: don't ask again for this agent's automatic runs
	// (sets shellWithoutAsking for the run's agent, saved on the runner).
	AlwaysAllowShell bool `json:"alwaysAllowShell,omitempty"`
}

// RunOfferBody is the body of POST /api/runs/:id/offer.
type RunOfferBody struct {
	Start            bool `json:"start"`
	AlwaysAllowShell bool `json:"alwaysAllowShell,omitempty"`
}
