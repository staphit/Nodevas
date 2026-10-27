package autopilot

import (
	"context"
	"errors"
	"io/fs"

	"nodevas/internal/engine"
	"nodevas/internal/identity"
	"nodevas/internal/project"
	"nodevas/internal/runners"
	"nodevas/internal/runnerwire"
	"nodevas/internal/runs"
)

// DispatcherRuns is Runs over the real dispatcher.
type DispatcherRuns struct{ D *runs.Dispatcher }

func (r DispatcherRuns) Start(ctx context.Context, req runs.StartRequest) (runs.Record, error) {
	return r.D.Start(ctx, req)
}

func (r DispatcherRuns) Get(ctx context.Context, id string) (runs.Record, error) {
	return r.D.Store().Get(ctx, id)
}

func (r DispatcherRuns) NodeRuns(ctx context.Context, project, nodeID string) ([]runs.Record, error) {
	return r.D.Store().List(ctx, runs.Filter{Project: project, NodeID: nodeID, Limit: 200})
}

func (r DispatcherRuns) Pipeline(ctx context.Context, id string) (runs.PipelineRecord, error) {
	return r.D.Store().Pipeline(ctx, id)
}

func (r DispatcherRuns) PipelineRuns(ctx context.Context, id string) ([]runs.Record, error) {
	return r.D.Store().PipelineRuns(ctx, id)
}

func (r DispatcherRuns) Merge(ctx context.Context, runID string, by runs.Approver, headSHA string) (runs.Record, error) {
	return r.D.Merge(ctx, runID, by, headSHA)
}

func (r DispatcherRuns) Cancel(ctx context.Context, runID, reason string) (runs.Record, error) {
	return r.D.Cancel(ctx, runID, reason)
}

func (r DispatcherRuns) HumanReview(ctx context.Context, id string, who runs.Reviewer, verdict, comment string) (runs.PipelineRecord, error) {
	return r.D.HumanReview(ctx, id, who, verdict, comment)
}

func (r DispatcherRuns) CancelPipeline(ctx context.Context, id, reason string) error {
	return r.D.CancelPipeline(ctx, id, reason)
}

func (r DispatcherRuns) Routing(ctx context.Context, ownerID string) ([]runnerwire.RouteRule, error) {
	return r.D.Store().Routing(ctx, ownerID)
}

func (r DispatcherRuns) Busy(ctx context.Context, runnerID string) (int, error) {
	return r.D.Store().Busy(ctx, runnerID)
}

func (r DispatcherRuns) Changes(ctx context.Context, runID string) (*runnerwire.RunChanges, error) {
	return r.D.Store().Changes(ctx, runID)
}

// RegistryRunners is Runners over the runner registry.
type RegistryRunners struct{ R *runners.Registry }

// Owned lists the owner's live runners with whether each is connected now.
func (r RegistryRunners) Owned(ctx context.Context, ownerID string) ([]runnerwire.Runner, error) {
	all, err := r.R.List(ctx, identity.Actor{ID: ownerID})
	if err != nil {
		return nil, err
	}
	var out []runnerwire.Runner
	for _, runner := range all {
		if runner.OwnerID != ownerID {
			continue
		}
		runner.Online = r.R.Online(runner.ID)
		out = append(out, runner)
	}
	return out, nil
}

// ProjectBoard is Board over the workspace's projects.
type ProjectBoard struct{ PM *project.ProjectManager }

func (b ProjectBoard) Graph(name string) (*engine.Graph, *engine.RunState, error) {
	st, err := b.PM.StoreFor(name)
	if err != nil {
		return nil, nil, err
	}
	g, _, err := st.LoadGraph()
	if err != nil {
		return nil, nil, err
	}
	rs, err := st.LoadState()
	if err != nil {
		return nil, nil, err
	}
	return g, rs, nil
}

func (b ProjectBoard) Content(name, nodeID string) (string, error) {
	st, err := b.PM.StoreFor(name)
	if err != nil {
		return "", err
	}
	raw, _, err := st.LoadNodeContent(nodeID)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	file, err := engine.ParseNodeFile([]byte(raw))
	if err != nil {
		return raw, nil
	}
	return file.Body, nil
}

func (b ProjectBoard) References(name, nodeID string) ([]runs.Reference, bool, error) {
	st, err := b.PM.StoreFor(name)
	if err != nil {
		return nil, false, err
	}
	g, _, err := st.LoadGraph()
	if err != nil {
		return nil, false, err
	}
	summaries, truncated, err := st.ReferenceSummaries(g, nodeID)
	if err != nil {
		return nil, false, err
	}
	var refs []runs.Reference
	for _, summary := range summaries {
		refs = append(refs, runs.Reference{
			ID: summary.ID, Title: summary.Title, TypeLabel: summary.TypeLabel, Summary: summary.Summary,
		})
	}
	return refs, truncated, nil
}

func (b ProjectBoard) SetStatus(name, nodeID string, status engine.Status, by, note string) error {
	st, err := b.PM.StoreFor(name)
	if err != nil {
		return err
	}
	_, err = st.SetStatus(nodeID, status, by, note)
	return err
}

// CreatedByRun is the run whose agent created a node, or "".
func (b ProjectBoard) CreatedByRun(name, nodeID string) string {
	st, err := b.PM.StoreFor(name)
	if err != nil {
		return ""
	}
	return st.CreatedByRun(nodeID)
}

// LockAcceptance locks a node's acceptance criteria as the hub, when an
// Executor starts on it.
func (b ProjectBoard) LockAcceptance(name, nodeID string) error {
	st, err := b.PM.StoreFor(name)
	if err != nil {
		return err
	}
	return st.LockAcceptance(nodeID)
}
