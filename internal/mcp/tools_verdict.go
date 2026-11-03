package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// A reviewer's or QA's structured verdict (agent analytics B): the hub
// keeps one per run, replaced when the run calls again, and reads the run's
// role from its own record, never from this call.

// VerdictCheck is one acceptance item a QA verdict judged.
type VerdictCheck struct {
	ID   string `json:"id"`
	Pass bool   `json:"pass"`
	Note string `json:"note,omitempty"`
}

// VerdictRequest is POST /api/verdicts.
type VerdictRequest struct {
	Outcome  string         `json:"outcome"`
	Severity string         `json:"severity"`
	Reasons  []string       `json:"reasons"`
	Note     string         `json:"note,omitempty"`
	Checks   []VerdictCheck `json:"checks,omitempty"`
}

// VerdictResult is the hub's answer.
type VerdictResult struct {
	Round    int  `json:"round"`
	Replaced bool `json:"replaced"`
}

// SubmitVerdict records this run's structured verdict.
func (c *Client) SubmitVerdict(ctx context.Context, in VerdictRequest) (VerdictResult, error) {
	var out VerdictResult
	err := c.post(ctx, "/api/verdicts", nil, in, &out)
	return out, err
}

type submitVerdictInput struct {
	Outcome  string         `json:"outcome" jsonschema:"pass or fail"`
	Severity string         `json:"severity" jsonschema:"none|low|medium|high|critical (pass: none/low)"`
	Reasons  []string       `json:"reasons,omitempty" jsonschema:"up to 5 of tests_missing tests_weak criteria_unmet regression style security scope_creep flaky other"`
	Note     string         `json:"note,omitempty"`
	Checks   []VerdictCheck `json:"checks,omitempty"`
}

type submitVerdictOutput struct {
	Round    int    `json:"round"`
	Replaced bool   `json:"replaced"`
	Note     string `json:"note"`
}

func registerVerdictTools(server *mcp.Server, client *Client) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "submit_verdict",
		Description: "Record your structured verdict for this review or QA run. Call it before you finish; calling again replaces it.",
		Annotations: &mcp.ToolAnnotations{Title: "Submit a verdict"},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in submitVerdictInput) (*mcp.CallToolResult, submitVerdictOutput, error) {
		outcome := strings.ToLower(strings.TrimSpace(in.Outcome))
		if outcome != "pass" && outcome != "fail" {
			return nil, submitVerdictOutput{}, &APIError{Code: CodeInvalidArgument, Message: "outcome is pass or fail"}
		}
		reasons := in.Reasons
		if reasons == nil {
			reasons = []string{}
		}
		res, err := client.SubmitVerdict(ctx, VerdictRequest{Outcome: outcome, Severity: strings.ToLower(strings.TrimSpace(in.Severity)),
			Reasons: reasons, Note: in.Note, Checks: in.Checks})
		if err != nil {
			return nil, submitVerdictOutput{}, guide(err)
		}
		note := fmt.Sprintf("Verdict recorded for round %d.", res.Round)
		if res.Replaced {
			note = fmt.Sprintf("Verdict replaced for round %d.", res.Round)
		}
		return nil, submitVerdictOutput{Round: res.Round, Replaced: res.Replaced, Note: note}, nil
	})
}
