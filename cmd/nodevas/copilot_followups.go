package main

import (
	"context"

	"nodevas/internal/autopilot"
	"nodevas/internal/copilot"
	"nodevas/internal/project"
	"nodevas/internal/runnerwire"
	"nodevas/internal/runs"
	"nodevas/internal/server"
)

// copilotFollowups gives the copilot what it needs to follow up on the plan
// runs it starts (internal/copilot/followup.go): the run store, the goal's
// tasks from the board, and a push to the person's own browsers. A server
// without runs has none.
func copilotFollowups(srv *server.Server, pm *project.ProjectManager) func(*copilot.Options) {
	return func(opts *copilot.Options) {
		dispatcher := srv.Runs()
		if dispatcher == nil || pm == nil {
			return
		}
		board := autopilot.ProjectBoard{PM: pm}
		opts.LookupRun = func(ctx context.Context, id string) (copilot.RunInfo, error) {
			r, err := dispatcher.Store().Get(ctx, id)
			if err != nil {
				return copilot.RunInfo{}, err
			}
			return copilotRunInfo(r), nil
		}
		opts.GoalTasks = func(_ context.Context, name, goalID string) (copilot.Goal, error) {
			g, _, err := board.Graph(name)
			if err != nil {
				return copilot.Goal{}, err
			}
			var goal copilot.Goal
			if node := g.NodeByID(goalID); node != nil {
				goal.Title = node.Title
			}
			for _, m := range autopilot.GoalMembers(g, goalID) {
				goal.Tasks = append(goal.Tasks, copilot.GoalTask{ID: m.ID, Title: m.Title, After: m.After})
			}
			return goal, nil
		}
		opts.NotifyFollowup = func(accountID, conversationID string) {
			srv.NotifyAccount(accountID, copilot.FollowupEvent, conversationID)
		}
	}
}

// observeCopilotFollowups tells the copilot about every run change, and
// delivers what came due while the hub was down.
func observeCopilotFollowups(srv *server.Server, service *copilot.Service) {
	dispatcher := srv.Runs()
	if dispatcher == nil {
		return
	}
	dispatcher.Observe(func(u runs.Update) {
		if u.Pipeline == nil {
			service.RunChanged(copilotRunInfo(u.Run))
		}
	})
	service.StartFollowups(context.Background())
}

func copilotRunInfo(r runs.Record) copilot.RunInfo {
	return copilot.RunInfo{
		ID: r.ID, Project: r.Project, NodeID: r.NodeID, Kind: r.Kind, Status: r.Status, Error: r.Error,
		CreatedBy: r.CreatedBy, RetryOf: r.RetryOf,
		MayFallBack: r.Status == runnerwire.RunFailed && runnerwire.FallbackWorthy(r.FailureKind) && len(r.Fallbacks) > 0,
	}
}
