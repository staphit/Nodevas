package runs

import (
	"net/http"
	"slices"
	"strings"

	"github.com/gin-gonic/gin"

	"nodevas/internal/auth"
	"nodevas/internal/engine"
	"nodevas/internal/httpapi/httpx"
	"nodevas/internal/locale"
	"nodevas/internal/runnerwire"
	"nodevas/internal/runs"
)

// runsBusy serves GET /api/runs/busy?runnerId=&repoId=[&nodeId=] (?project=).
// It reads on the reader pool and answers the preflight busy state for a node
// start (or repo check alone when nodeId is omitted, e.g. copilot start_breakdown).
func (a *API) runsBusy(c *gin.Context) {
	if !a.takesRuns(c) {
		return
	}
	_, projectName, ok := a.requestProject(c)
	if !ok {
		return
	}
	runnerID := strings.TrimSpace(c.Query("runnerId"))
	repoID := strings.TrimSpace(c.Query("repoId"))
	nodeID := strings.TrimSpace(c.Query("nodeId"))
	if runnerID == "" {
		httpx.Coded(c, http.StatusBadRequest, "INVALID_ARGUMENT",
			locale.Errorf("runnerId is required", "需要指定 runnerId"), map[string]any{"field": "runnerId"})
		return
	}
	if repoID == "" {
		httpx.Coded(c, http.StatusBadRequest, "INVALID_ARGUMENT",
			locale.Errorf("repoId is required", "需要指定 repoId"), map[string]any{"field": "repoId"})
		return
	}
	if nodeID != "" && !engine.ValidNodeID(nodeID) {
		httpx.Coded(c, http.StatusBadRequest, "INVALID_ARGUMENT",
			locale.Errorf("nodeId is not a valid node id", "nodeId 不是有效的節點代號"), map[string]any{"field": "nodeId"})
		return
	}

	actor := auth.ActorFrom(c.Request)
	ctx := c.Request.Context()
	runner, err := a.reg.Get(ctx, runnerID)
	if err != nil || runner.OwnerID != actor.ID {
		notFound(c, "runner")
		return
	}
	repo, found := findRunnerRepo(runner.Info, repoID)
	if !found {
		httpx.Coded(c, http.StatusBadRequest, "INVALID_ARGUMENT",
			locale.Errorf("repoId is not one of the runner's repositories", "repoId 不是這個 runner 的儲存庫"),
			map[string]any{"field": "repoId"})
		return
	}
	var caps []string
	if runner.Info != nil {
		caps = runner.Info.Capabilities
	}
	resp, err := a.d.PreflightBusy(ctx, runs.BusyQuery{
		ActorID:  actor.ID,
		IsAdmin:  actor.IsAdmin(),
		Project:  projectName,
		NodeID:   nodeID,
		RepoKey:  runnerwire.RepoKey(runner.ID, repo),
		RunnerID: runner.ID,
		RepoID:   repo.ID,
	}, caps, false)
	if err != nil {
		fail(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, resp)
}

// globalBusy serves GET /api/global-runs/busy?runnerId=&repoIds=a,b,c.
// Person-only and own-runner-only (admins included); answers per-repo busy
// status on the reader pool (§C.2.2, §C.6).
func (a *API) globalBusy(c *gin.Context) {
	if !a.takesRuns(c) {
		return
	}
	runnerID := strings.TrimSpace(c.Query("runnerId"))
	if runnerID == "" {
		httpx.Coded(c, http.StatusBadRequest, "INVALID_ARGUMENT",
			locale.Errorf("runnerId is required", "需要指定 runnerId"), map[string]any{"field": "runnerId"})
		return
	}
	rawRepos := strings.TrimSpace(c.Query("repoIds"))
	if rawRepos == "" {
		httpx.Coded(c, http.StatusBadRequest, "INVALID_ARGUMENT",
			locale.Errorf("repoIds is required", "需要指定 repoIds"), map[string]any{"field": "repoIds"})
		return
	}
	parts := strings.Split(rawRepos, ",")
	repoIDs := make([]string, 0, len(parts))
	seen := make(map[string]bool, len(parts))
	for _, p := range parts {
		id := strings.TrimSpace(p)
		if id == "" {
			httpx.Coded(c, http.StatusBadRequest, "INVALID_ARGUMENT",
				locale.Errorf("repoIds contains an empty entry", "repoIds 包含空白項目"), map[string]any{"field": "repoIds"})
			return
		}
		if !seen[id] {
			seen[id] = true
			repoIDs = append(repoIDs, id)
		}
	}
	if len(repoIDs) == 0 || len(repoIDs) > runnerwire.MaxGroupRepos {
		httpx.Coded(c, http.StatusBadRequest, "INVALID_ARGUMENT",
			locale.Errorf("repoIds must have 1 to %d repositories", "repoIds 須包含 1 到 %d 個儲存庫", runnerwire.MaxGroupRepos),
			map[string]any{"field": "repoIds"})
		return
	}

	actor := auth.ActorFrom(c.Request)
	ctx := c.Request.Context()
	// Own runner only, admins included (§C.2.2).
	runner, err := a.reg.Get(ctx, runnerID)
	if err != nil || runner.OwnerID != actor.ID {
		notFound(c, "runner")
		return
	}
	var caps []string
	if runner.Info != nil {
		caps = runner.Info.Capabilities
	}
	out := runnerwire.GlobalBusyResponse{Repos: make([]runnerwire.RepoBusy, 0, len(repoIDs))}
	for _, repoID := range repoIDs {
		repo, found := findRunnerRepo(runner.Info, repoID)
		if !found {
			httpx.Coded(c, http.StatusBadRequest, "INVALID_ARGUMENT",
				locale.Errorf("repoId is not one of the runner's repositories", "repoId 不是這個 runner 的儲存庫"),
				map[string]any{"field": "repoIds"})
			return
		}
		resp, err := a.d.PreflightBusy(ctx, runs.BusyQuery{
			ActorID:  actor.ID,
			RepoKey:  runnerwire.RepoKey(runner.ID, repo),
			Global:   true,
			RunnerID: runner.ID,
			RepoID:   repo.ID,
		}, caps, false)
		if err != nil {
			fail(c, err)
			return
		}
		out.Repos = append(out.Repos, runnerwire.RepoBusy{
			RepoID:   repo.ID,
			Holders:  resp.Holders,
			Others:   resp.Others,
			Choices:  resp.Choices,
			Continue: resp.Continue,
		})
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, out)
}

func findRunnerRepo(info *runnerwire.Info, repoID string) (runnerwire.Repo, bool) {
	if info == nil || repoID == "" {
		return runnerwire.Repo{}, false
	}
	idx := slices.IndexFunc(info.Repos, func(r runnerwire.Repo) bool { return r.ID == repoID })
	if idx < 0 {
		return runnerwire.Repo{}, false
	}
	return info.Repos[idx], true
}
