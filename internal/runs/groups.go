package runs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"nodevas/internal/identity"
	"nodevas/internal/runnerwire"
)

// CodeGroupNeedsBackground is StartError.Reason when a multi-repository group
// start asks for interactive mode (§10.2).
const CodeGroupNeedsBackground = "GROUP_NEEDS_BACKGROUND"

// maxGroupSiblingNamesBytes caps the comma-separated sibling repository list in
// a group member's prompt (§10.2).
const maxGroupSiblingNamesBytes = 1024

// GroupRecord is a stored multi-repo fan-out group (run_groups).
type GroupRecord struct {
	runnerwire.RunGroup
	OwnerID string
}

// GlobalGroupStartRequest is one multi-repo fan-out group to start.
type GlobalGroupStartRequest struct {
	Body        runnerwire.CreateGlobalGroupRequest
	ActorID     string
	ActorName   string
	SteppedUpAt string
}

// GlobalGroupResult is the created group and its member runs.
type GlobalGroupResult struct {
	Group GroupRecord
	Runs  []Record
}

type groupRepoPlan struct {
	repo    runnerwire.Repo
	repoKey string
	outcome BusyOutcome
}

// groupSiblingLine formats the sibling repository list for a group member's
// prompt, truncating the names to maxGroupSiblingNamesBytes with an ellipsis.
func groupSiblingLine(repos []runnerwire.Repo) string {
	names := make([]string, 0, len(repos))
	for _, r := range repos {
		name := strings.TrimSpace(r.Name)
		if name == "" {
			name = r.ID
		}
		names = append(names, name)
	}
	joined := strings.Join(names, ", ")
	if len(joined) > maxGroupSiblingNamesBytes {
		const ellipsis = "…"
		joined = Truncate(joined, maxGroupSiblingNamesBytes-len(ellipsis)) + ellipsis
	}
	return fmt.Sprintf("This is one of %d repositories changing together: %s", len(repos), joined)
}

// StartGlobalGroup validates a multi-repo fan-out group request against the
// caller's own runner, checks the daily run budget for all N members under the
// owner's budget lock in one transaction, inserts the group and N runs, writes
// each member's seq-0 user event, audits the group start without task text, and
// pumps the runner once (§10.2, §C.2.3).
func (d *Dispatcher) StartGlobalGroup(ctx context.Context, req GlobalGroupStartRequest) (GlobalGroupResult, error) {
	body := req.Body
	if !runnerwire.ValidGroupRepoIDs(body.RepoIDs) {
		return GlobalGroupResult{}, invalidField("repoIds",
			"repoIds must be %d to %d distinct repositories",
			"repoIds 須為 %d 到 %d 個不重複的儲存庫",
			runnerwire.MinGroupRepos, runnerwire.MaxGroupRepos)
	}
	v, err := d.validateGlobal(ctx, body.Task, body.Slug, body.Base, body.Mode, body.OnBusy, body.HolderIDs,
		body.RunnerID, req.ActorID, req.ActorName, true)
	if err != nil {
		return GlobalGroupResult{}, err
	}
	runner := v.runner
	model, problem := ResolveAgentModel(runner.Info, runnerwire.AgentModel{Agent: body.Agent, Model: body.Model})
	if problem != nil {
		field := "model"
		if runner.Info == nil || !slices.ContainsFunc(runner.Info.Agents, func(a runnerwire.Agent) bool {
			return a.Kind == body.Agent && a.Installed
		}) {
			field = "agent"
		}
		return GlobalGroupResult{}, &StartError{Code: StartInvalid, Field: field, Msg: problem.Error(), said: problem}
	}
	repos := make([]runnerwire.Repo, 0, len(body.RepoIDs))
	for _, repoID := range body.RepoIDs {
		_, repo, repoProblem := CheckAgainstHello(runner.Info, runnerwire.CreateRunRequest{
			Agent: body.Agent, Model: model, RepoID: repoID,
		})
		if repoProblem != nil {
			return GlobalGroupResult{}, &StartError{Code: StartInvalid, Field: "repoIds", Msg: repoProblem.Error(), said: repoProblem}
		}
		repos = append(repos, repo)
	}
	if err := checkGlobalRunnerCaps(runner.Info, runnerwire.ModeHeadless); err != nil {
		return GlobalGroupResult{}, err
	}

	lockKeys := make([]string, 0, len(repos)+1)
	lockKeys = append(lockKeys, "budget:"+runner.OwnerID)
	for _, repo := range repos {
		lockKeys = append(lockKeys, "repo:"+runnerwire.RepoKey(runner.ID, repo))
	}

	var group GroupRecord
	var created []Record
	var kept []groupRepoPlan
	err = d.store.db.Tx(ctx, func(tx *sql.Tx) error {
		if err := lockBusy(ctx, tx, lockKeys...); err != nil {
			return err
		}
		kept = make([]groupRepoPlan, 0, len(repos))
		var busyRepos []runnerwire.RepoBusy
		var firstBusyErr *StartError
		for _, repo := range repos {
			repoKey := runnerwire.RepoKey(runner.ID, repo)
			outcome, err := d.applyBusy(ctx, tx, BusyRequest{
				Query:      BusyQuery{ActorID: req.ActorID, RepoKey: repoKey, Global: true},
				OnBusy:     body.OnBusy,
				HolderIDs:  body.HolderIDs,
				RunnerCaps: runner.Info.Capabilities,
				Group:      true,
			})
			if err != nil {
				var se *StartError
				if errors.As(err, &se) && se.Code == StartBusy && se.Busy != nil {
					if firstBusyErr == nil {
						firstBusyErr = se
					}
					if len(se.Busy.Repos) > 0 {
						busyRepos = append(busyRepos, se.Busy.Repos...)
					} else {
						busyRepos = append(busyRepos, runnerwire.RepoBusy{
							RepoID:  repo.ID,
							Holders: se.Busy.Holders,
							Others:  se.Busy.Others,
							Choices: se.Busy.Choices,
						})
					}
					continue
				}
				return err
			}
			if !outcome.Proceed {
				continue
			}
			kept = append(kept, groupRepoPlan{repo: repo, repoKey: repoKey, outcome: outcome})
		}
		if firstBusyErr != nil {
			firstBusyErr.Busy = &runnerwire.BusyResponse{
				Repos:   busyRepos,
				Choices: []string{runnerwire.OnBusyBoth, runnerwire.OnBusySkip},
			}
			return firstBusyErr
		}
		if len(kept) == 0 {
			return invalidField("repoIds", "all selected repositories are busy", "所選的儲存庫皆在忙碌中")
		}

		budget, err := d.store.budgetStatusWith(ctx, tx, runner.OwnerID)
		if err != nil {
			return err
		}
		if ex := exceededFor(budget, len(kept)); ex != "" {
			budget.Exceeded = ex
			said := BudgetSayFor(budget, len(kept))
			return &StartError{Code: StartBudget, Msg: said.Error(), said: said, Budget: budget}
		}

		keptRepos := make([]runnerwire.Repo, len(kept))
		for i, plan := range kept {
			keptRepos[i] = plan.repo
		}
		jobPrompt := BuildPrompt(v.title, v.rest, "", withSimple(groupSiblingLine(keptRepos), body.Simple))

		group, err = d.store.createGroupWith(ctx, tx, GroupRecord{
			OwnerID: runner.OwnerID,
			RunGroup: runnerwire.RunGroup{
				RunnerID:      runner.ID,
				Title:         v.title,
				Agent:         body.Agent,
				Model:         model,
				NoPR:          !body.OpenPR,
				RepoCount:     len(kept),
				CreatedBy:     req.ActorID,
				CreatedByName: v.actorName,
			},
		})
		if err != nil {
			return err
		}

		created = make([]Record, 0, len(kept))
		for _, plan := range kept {
			r, err := d.store.createWith(ctx, tx, New{
				Global:        true,
				NodeTitle:     v.title,
				BranchSlug:    v.slug,
				NoPR:          !body.OpenPR,
				RunnerID:      runner.ID,
				RunnerName:    runner.Name,
				OwnerID:       runner.OwnerID,
				Agent:         body.Agent,
				Model:         model,
				RepoID:        plan.repo.ID,
				RepoName:      plan.repo.Name,
				Base:          body.Base,
				JobPrompt:     jobPrompt,
				CreatedBy:     req.ActorID,
				CreatedByName: v.actorName,
				SteppedUpAt:   req.SteppedUpAt,
				Mode:          runnerwire.ModeHeadless,
				Origin:        runnerwire.OriginPerson,
				GroupID:       group.ID,
				AfterRun:      plan.outcome.AfterRun,
				BusyChoice:    plan.outcome.Choice,
				RepoKey:       plan.repoKey,
			})
			if err != nil {
				return err
			}
			created = append(created, r)
		}
		return nil
	})
	if err != nil {
		return GlobalGroupResult{}, err
	}

	actor := identity.Actor{ID: req.ActorID, Name: v.actorName}
	for _, plan := range kept {
		d.afterBusy(ctx, actor, plan.outcome)
	}
	for _, r := range created {
		if err := d.store.UserFirst(ctx, r.ID, v.task); err != nil {
			return GlobalGroupResult{}, err
		}
	}
	d.record(AuditEntry{
		ActorID: req.ActorID, ActorName: v.actorName,
		Action: "run.global.group", Target: group.ID,
		Detail: map[string]any{
			"group": group.ID, "runner": runner.ID, "agent": group.Agent, "model": group.Model,
			"base": body.Base, "slug": v.slug, "noPR": group.NoPR, "count": len(created),
			"taskBytes": len(v.task), "simple": body.Simple,
		},
	})
	for _, r := range created {
		d.notifyChanged(r)
	}
	d.Pump(ctx, runner.ID)

	out := make([]Record, len(created))
	for i, r := range created {
		if latest, err := d.store.Get(ctx, r.ID); err == nil {
			out[i] = latest
		} else {
			out[i] = r
		}
	}
	return GlobalGroupResult{Group: group, Runs: out}, nil
}

// ResumeGroup clears a group's usage-limit hold and pumps its runner so queued
// members are offered (§10.2).
func (d *Dispatcher) ResumeGroup(ctx context.Context, groupID string) (GroupRecord, error) {
	g, err := d.store.GetGroup(ctx, groupID)
	if err != nil {
		return GroupRecord{}, err
	}
	if err := d.store.SetGroupHold(ctx, groupID, ""); err != nil {
		return GroupRecord{}, err
	}
	g.HeldUntil = ""
	d.Pump(ctx, g.RunnerID)
	return g, nil
}

// CancelGroup cancels every unfinished member run of groupID and returns the
// runs it cancelled (§10.2).
func (d *Dispatcher) CancelGroup(ctx context.Context, groupID, by string) ([]Record, error) {
	if _, err := d.store.GetGroup(ctx, groupID); err != nil {
		return nil, err
	}
	members, err := d.store.GroupRuns(ctx, groupID)
	if err != nil {
		return nil, err
	}
	out := make([]Record, 0, len(members))
	for _, r := range members {
		if runnerwire.RunFinal(r.Status) {
			continue
		}
		next, err := d.Cancel(ctx, r.ID, "cancelled by "+by)
		if errors.Is(err, ErrTransition) {
			continue
		}
		if err != nil {
			return out, err
		}
		out = append(out, next)
	}
	return out, nil
}

// releaseGroupHolds clears expired group usage-limit holds and pumps their
// runners on the RunRetries tick (§10.2).
func (d *Dispatcher) releaseGroupHolds(ctx context.Context) {
	if d.store == nil {
		return
	}
	runnerIDs, err := d.store.releaseExpiredGroupHolds(ctx, d.clockNow())
	if err != nil {
		slog.Warn("runs: release group holds", slog.Any("error", err))
		return
	}
	for _, runnerID := range runnerIDs {
		d.Pump(ctx, runnerID)
	}
}

const groupColumns = `id, owner_id, runner_id, created_by, created_by_name, title, agent, model, no_pr, repo_count, held_until, created_at`

func scanGroup(row interface{ Scan(...any) error }) (GroupRecord, error) {
	var g GroupRecord
	var repoCount int64
	if err := row.Scan(
		&g.ID, &g.OwnerID, &g.RunnerID, &g.CreatedBy, &g.CreatedByName,
		&g.Title, &g.Agent, &g.Model, &g.NoPR, &repoCount, &g.HeldUntil, &g.CreatedAt,
	); err != nil {
		return GroupRecord{}, err
	}
	g.RepoCount = int(repoCount)
	return g, nil
}

func (s *Store) createGroupWith(ctx context.Context, q querier, in GroupRecord) (GroupRecord, error) {
	id, err := newID()
	if err != nil {
		return GroupRecord{}, err
	}
	now := s.stamp()
	_, err = q.ExecContext(ctx, `
INSERT INTO run_groups (id, owner_id, runner_id, created_by, created_by_name, title, agent, model, no_pr, repo_count, held_until, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, in.OwnerID, in.RunnerID, in.CreatedBy, in.CreatedByName,
		in.Title, in.Agent, in.Model, in.NoPR, int64(in.RepoCount), in.HeldUntil, now)
	if err != nil {
		return GroupRecord{}, err
	}
	in.ID = id
	in.CreatedAt = now
	return in, nil
}

// GetGroup returns one multi-repo run group by ID.
func (s *Store) GetGroup(ctx context.Context, id string) (GroupRecord, error) {
	if id == "" {
		return GroupRecord{}, ErrNotFound
	}
	g, err := scanGroup(s.db.QueryRowContext(ctx, `SELECT `+groupColumns+` FROM run_groups WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return GroupRecord{}, ErrNotFound
	}
	return g, err
}

// GroupRuns returns the member runs of groupID in creation order (oldest first).
func (s *Store) GroupRuns(ctx context.Context, groupID string) ([]Record, error) {
	if groupID == "" {
		return []Record{}, nil
	}
	out, err := s.query(ctx, `SELECT `+runColumns+` FROM runs WHERE is_global AND group_id = ? ORDER BY created_at ASC, id ASC`, groupID)
	if err != nil {
		return nil, err
	}
	refs := make([]*Record, len(out))
	for i := range out {
		refs[i] = &out[i]
	}
	if err := s.attachMerges(ctx, refs); err != nil {
		return nil, err
	}
	if err := s.attachWorkspaceActions(ctx, refs); err != nil {
		return nil, err
	}
	return out, nil
}

// releaseExpiredGroupHolds clears held_until on every group whose hold has
// expired at now and returns the distinct runner IDs to pump.
func (s *Store) releaseExpiredGroupHolds(ctx context.Context, now time.Time) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, runner_id, held_until FROM run_groups WHERE held_until <> ''`)
	if err != nil {
		return nil, err
	}
	type heldGroup struct {
		id, runnerID, heldUntil string
	}
	var expired []heldGroup
	nowStamp := formatStamp(now)
	for rows.Next() {
		var h heldGroup
		if err := rows.Scan(&h.id, &h.runnerID, &h.heldUntil); err != nil {
			rows.Close()
			return nil, err
		}
		stillHeld := false
		if at, err := time.Parse(time.RFC3339Nano, h.heldUntil); err == nil {
			stillHeld = at.After(now)
		} else {
			stillHeld = h.heldUntil > nowStamp
		}
		if !stillHeld {
			expired = append(expired, h)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(expired) == 0 {
		return nil, nil
	}
	var runners []string
	for _, h := range expired {
		res, err := s.db.ExecContext(ctx, `UPDATE run_groups SET held_until = '' WHERE id = ? AND held_until = ?`, h.id, h.heldUntil)
		if err != nil {
			return runners, err
		}
		if n, err := res.RowsAffected(); err == nil && n > 0 && !slices.Contains(runners, h.runnerID) {
			runners = append(runners, h.runnerID)
		}
	}
	return runners, nil
}
