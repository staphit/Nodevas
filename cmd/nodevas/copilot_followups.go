package main

import (
	"context"
	"errors"
	"io/fs"

	"nodevas/internal/autopilot"
	"nodevas/internal/copilot"
	"nodevas/internal/engine"
	"nodevas/internal/identity"
	"nodevas/internal/project"
	"nodevas/internal/runnerwire"
	"nodevas/internal/runs"
	"nodevas/internal/server"
)

// copilotFollowups gives the copilot what it needs to follow up on the plan
// runs it starts (internal/copilot/followup.go): the run store, the goal's
// tasks from the board, and a push to the person's own browsers; and, for a
// revision's replan (internal/copilot/revise.go), the two board writes that
// apply it. A server without runs has none.
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
		opts.GoalTasks = func(ctx context.Context, name, goalID string) (copilot.Goal, error) {
			g, rs, err := board.Graph(name)
			if err != nil {
				return copilot.Goal{}, err
			}
			// Tasks with a run going (not a plan run): a revision never
			// changes them.
			going, err := dispatcher.Store().List(ctx, runs.Filter{Project: name, Active: true})
			if err != nil {
				return copilot.Goal{}, err
			}
			active := map[string]bool{}
			for _, r := range going {
				if r.Kind != runnerwire.JobPlan {
					active[r.NodeID] = true
				}
			}
			statuses := engine.ComputeStatuses(g, rs)
			var goal copilot.Goal
			if node := g.NodeByID(goalID); node != nil {
				goal.Title = node.Title
			}
			for _, m := range autopilot.GoalMembers(g, goalID) {
				goal.Tasks = append(goal.Tasks, copilot.GoalTask{ID: m.ID, Title: m.Title, After: m.After,
					Status: string(statuses[m.ID]), Active: active[m.ID]})
			}
			return goal, nil
		}
		opts.NotifyFollowup = func(accountID, conversationID string) {
			srv.NotifyAccount(accountID, copilot.FollowupEvent, conversationID)
		}
		opts.SkipTask = func(_ context.Context, name, nodeID, by, note string) error {
			if err := board.SetStatus(name, nodeID, engine.StatusSkipped, by, note); err != nil {
				return err
			}
			srv.NotifyProject(name, "state-changed", "")
			return nil
		}
		opts.NoteTask = func(_ context.Context, name, nodeID, accountID, note string) error {
			st, err := pm.StoreFor(name)
			if err != nil {
				return err
			}
			content, rev, err := st.LoadNodeContent(nodeID)
			if err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
			updated, changed := copilot.AppendRevision(content, copilot.RevisionStamp(), note)
			if !changed {
				return nil
			}
			if _, _, err := st.SaveNodeContent(identity.Actor{ID: accountID, Name: "copilot revision"}, nodeID, updated, rev); err != nil {
				return err
			}
			srv.NotifyProject(name, "node-changed", nodeID)
			return nil
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
		CreatedBy: r.CreatedBy, RetryOf: r.RetryOf, Summary: r.Summary,
		MayFallBack: r.Status == runnerwire.RunFailed && runnerwire.FallbackWorthy(r.FailureKind) && len(r.Fallbacks) > 0,
	}
}
