package mcp

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"nodevas/internal/engine"
	"nodevas/internal/store"
)

// The tools that change the board.
//
// The shape they enforce is claim, work, report. An agent cannot report on a
// node it has not claimed, which is not bureaucracy: two agents reading the
// same queue would otherwise both start the same task, and the only evidence
// would be two sets of changes to one file.

type claimTaskInput struct {
	ID           string `json:"id" jsonschema:"the node id, from get_ready_tasks"`
	LeaseMinutes int    `json:"leaseMinutes,omitempty" jsonschema:"how long to hold it before it returns to the queue (default 30, max 480)"`
	RequestID    string `json:"requestId,omitempty" jsonschema:"an id of your choosing; retrying with the same one will not claim twice"`
}

type claimTaskOutput struct {
	ID      string            `json:"id"`
	Owner   string            `json:"owner"`
	Expires string            `json:"expires"`
	Task    *engine.ReadyNode `json:"task,omitempty"`
	Note    string            `json:"note"`
}

type setStatusInput struct {
	ID        string `json:"id" jsonschema:"the node id you claimed"`
	Status    string `json:"status" jsonschema:"done, failed, or skipped"`
	Note      string `json:"note,omitempty" jsonschema:"what happened, for the timeline a person will read"`
	RequestID string `json:"requestId,omitempty" jsonschema:"an id of your choosing; retrying with the same one will not record the result twice"`
}

type setStatusOutput struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Note   string `json:"note"`
}

type releaseTaskInput struct {
	ID string `json:"id" jsonschema:"the node id to give back"`
}

type releaseTaskOutput struct {
	ID   string `json:"id"`
	Note string `json:"note"`
}

// reportableStatuses are the outcomes an agent may record.
//
// Not every valid status: `ready` is what releasing a task means and has its
// own tool, and the intermediate states describe a person's working rhythm
// rather than an agent's. Narrowing the vocabulary here keeps a model from
// inventing a lifecycle nobody asked for.
var reportableStatuses = map[string]bool{
	string(engine.StatusDone):    true,
	string(engine.StatusFailed):  true,
	string(engine.StatusSkipped): true,
}

func registerWriteTools(server *mcp.Server, client *Client) {
	mcp.AddTool(server, &mcp.Tool{
		Name: "claim_task",
		Description: "Take a task before working on it. This is what stops two agents doing the same one: " +
			"the check and the claim happen together on the server, so exactly one caller wins. " +
			"The task moves to in_progress and anyone looking at the board sees it change. " +
			"Claiming again extends your hold rather than failing, so a long task is safe. " +
			"Report the outcome with set_node_status when you finish, or give it back with release_task.",
		Annotations: &mcp.ToolAnnotations{
			Title:          "Claim a task",
			IdempotentHint: true,
		},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in claimTaskInput) (*mcp.CallToolResult, claimTaskOutput, error) {
		id := strings.TrimSpace(in.ID)
		if id == "" {
			return nil, claimTaskOutput{}, &APIError{Code: CodeInvalidArgument, Message: "id is required"}
		}
		result, err := client.Claim(ctx, id,
			time.Duration(in.LeaseMinutes)*time.Minute, in.RequestID)
		if err != nil {
			return nil, claimTaskOutput{}, guide(err)
		}
		return nil, claimTaskOutput{
			ID:      id,
			Owner:   ownerOf(result.Claim),
			Expires: expiryOf(result.Claim),
			Task:    result.Task,
			Note: "You hold this task until the time above. Read it with get_node, " +
				"then report the outcome with set_node_status.",
		}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "set_node_status",
		Description: "Record what happened to a task you claimed: done, failed, or skipped. " +
			"The note is written into the project's timeline, which people read to reconstruct what happened — " +
			"say what you actually did, and on failure say what stopped you. " +
			"Reporting releases the task and may unblock the ones waiting on it.",
		Annotations: &mcp.ToolAnnotations{Title: "Report an outcome", IdempotentHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in setStatusInput) (*mcp.CallToolResult, setStatusOutput, error) {
		id := strings.TrimSpace(in.ID)
		status := strings.ToLower(strings.TrimSpace(in.Status))
		if id == "" {
			return nil, setStatusOutput{}, &APIError{Code: CodeInvalidArgument, Message: "id is required"}
		}
		if !reportableStatuses[status] {
			return nil, setStatusOutput{}, &APIError{
				Code:    CodeInvalidArgument,
				Message: "status must be done, failed or skipped; to give a task back unfinished use release_task",
			}
		}
		if err := client.SetStatus(ctx, id, status, in.Note, in.RequestID); err != nil {
			return nil, setStatusOutput{}, guide(err)
		}
		return nil, setStatusOutput{
			ID:     id,
			Status: status,
			Note:   "Recorded. Call get_ready_tasks for what is actionable now.",
		}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "release_task",
		Description: "Give a claimed task back unfinished, so it returns to the ready queue for someone else. " +
			"Use this when you cannot make progress — it is better than holding a task until the lease runs out, " +
			"and better than reporting a result you did not achieve.",
		Annotations: &mcp.ToolAnnotations{Title: "Give a task back", IdempotentHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in releaseTaskInput) (*mcp.CallToolResult, releaseTaskOutput, error) {
		id := strings.TrimSpace(in.ID)
		if id == "" {
			return nil, releaseTaskOutput{}, &APIError{Code: CodeInvalidArgument, Message: "id is required"}
		}
		if err := client.Release(ctx, id); err != nil {
			return nil, releaseTaskOutput{}, guide(err)
		}
		return nil, releaseTaskOutput{
			ID:   id,
			Note: "Given back. It is in the ready queue again.",
		}, nil
	})
}

func ownerOf(claim *store.Claim) string {
	if claim == nil {
		return ""
	}
	return claim.Owner
}

func expiryOf(claim *store.Claim) string {
	if claim == nil {
		return ""
	}
	return claim.Expires.Format(time.RFC3339)
}

// guide adds the next move to a refusal.
//
// The server says why it said no; a model also needs to know what to do about
// it, and "already claimed" read on its own invites a retry loop against a node
// somebody else is holding.
func guide(err error) error {
	apiErr, ok := err.(*APIError)
	if !ok {
		return err
	}
	var advice string
	switch apiErr.Code {
	case "ALREADY_CLAIMED":
		advice = "Another agent is working on it — pick a different task."
	case "NOT_CLAIMABLE":
		advice = "It is not actionable; get_ready_tasks lists what is."
	case "NOT_OWNER":
		advice = "Claim it with claim_task before reporting on it."
	default:
		return err
	}
	guided := *apiErr
	guided.Message = fmt.Sprintf("%s. %s", strings.TrimRight(apiErr.Message, "."), advice)
	return &guided
}
