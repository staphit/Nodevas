package explorer

import (
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"nodevas/internal/httpapi/httpx"
	"nodevas/internal/runners"
	"nodevas/internal/runnerwire"
)

// RepoItem is one registered repo of one of the viewer's runners.
type RepoItem struct {
	RunnerID   string `json:"runnerId"`
	RunnerName string `json:"runnerName"`
	Online     bool   `json:"online"`
	RepoID     string `json:"repoId"`
	Name       string `json:"name"`
	Browsable  bool   `json:"browsable"`
	// Reason says why it is not browsable: offline or browse_off.
	Reason string `json:"reason,omitempty"`
}

// repos lists the repos on the viewer's own runners (an administrator's
// too: another person's disk is theirs).
func (a *API) repos(c *gin.Context) {
	actor, ok := person(c)
	if !ok {
		return
	}
	out := []RepoItem{}
	if a.o.Runners != nil {
		list, err := a.o.Runners.List(c.Request.Context(), actor)
		if err != nil {
			httpx.Err(c, http.StatusInternalServerError, err)
			return
		}
		for _, r := range list {
			if r.OwnerID != actor.ID || r.Info == nil {
				continue
			}
			online := a.o.Runners.Online(r.ID)
			canBrowse := slices.Contains(r.Info.Capabilities, runnerwire.CapRepoBrowse)
			for _, repo := range r.Info.Repos {
				item := RepoItem{RunnerID: r.ID, RunnerName: r.Name, Online: online, RepoID: repo.ID, Name: repo.Name,
					Browsable: online && canBrowse}
				switch {
				case !online:
					item.Reason = "offline"
				case !canBrowse:
					item.Reason = "browse_off"
				}
				out = append(out, item)
			}
		}
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{"repos": out})
}

// browse relays one read-only request to the viewer's runner and answers
// with what it returned, or the refusal's status.
func (a *API) browse(c *gin.Context, req runnerwire.RepoRequest, answer func(runnerwire.RepoDone) any) {
	actor, ok := person(c)
	if !ok {
		return
	}
	if _, _, ok := a.project(c); !ok {
		return
	}
	if a.o.Repo == nil {
		httpx.Coded(c, http.StatusNotFound, "NOT_FOUND", errors.New("runner or repo not found"), nil)
		return
	}
	req.RepoID = c.Param("repo")
	if len(req.Path) > runnerwire.MaxRepoPathBytes {
		httpx.Coded(c, http.StatusBadRequest, "BAD_PATH", errors.New("the path is too long"), nil)
		return
	}
	done, err := a.o.Repo.Browse(c.Request.Context(), actor.ID, c.Param("runner"), req)
	switch {
	case errors.Is(err, runners.ErrRepoNotFound):
		httpx.Coded(c, http.StatusNotFound, "NOT_FOUND", errors.New("runner or repo not found"), nil)
		return
	case errors.Is(err, runners.ErrRunnerOffline), errors.Is(err, runners.ErrRepoRunnerGone):
		httpx.Coded(c, http.StatusServiceUnavailable, "RUNNER_OFFLINE", err, nil)
		return
	case errors.Is(err, runners.ErrRepoBrowseOff):
		httpx.Coded(c, http.StatusPreconditionFailed, "REPO_BROWSE_OFF", err, nil)
		return
	case errors.Is(err, runners.ErrRepoTimeout):
		httpx.Coded(c, http.StatusGatewayTimeout, "TIMEOUT", err, nil)
		return
	case err != nil:
		httpx.Err(c, http.StatusInternalServerError, err)
		return
	}
	if done.Code != "" {
		status, code := http.StatusBadGateway, "GIT_ERROR"
		switch done.Code {
		case runnerwire.RepoErrNotFound, runnerwire.RepoErrRefused:
			status, code = http.StatusNotFound, "NOT_FOUND"
		case runnerwire.RepoErrBadPath:
			status, code = http.StatusBadRequest, "BAD_PATH"
		case runnerwire.RepoErrHidden:
			status, code = http.StatusForbidden, "HIDDEN_FILE"
		case runnerwire.RepoErrOff:
			status, code = http.StatusPreconditionFailed, "REPO_BROWSE_OFF"
		case runnerwire.RepoErrBusy:
			status, code = http.StatusTooManyRequests, "BUSY"
		}
		msg := strings.TrimSpace(done.Error)
		if msg == "" {
			msg = done.Code
		}
		httpx.Coded(c, status, code, errors.New(msg), nil)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, answer(done))
}

func (a *API) repoTree(c *gin.Context) {
	a.browse(c, runnerwire.RepoRequest{Op: runnerwire.RepoOpList, Path: c.Query("path")}, func(d runnerwire.RepoDone) any {
		if d.Listing == nil {
			return runnerwire.RepoListing{Entries: []runnerwire.RepoEntry{}}
		}
		return d.Listing
	})
}

func (a *API) repoFile(c *gin.Context) {
	a.browse(c, runnerwire.RepoRequest{Op: runnerwire.RepoOpRead, Path: c.Query("path")}, func(d runnerwire.RepoDone) any {
		if d.File == nil {
			return runnerwire.RepoFile{}
		}
		return d.File
	})
}

func (a *API) repoStatus(c *gin.Context) {
	a.browse(c, runnerwire.RepoRequest{Op: runnerwire.RepoOpStatus}, func(d runnerwire.RepoDone) any {
		if d.Status == nil {
			return runnerwire.RepoGitStatus{Entries: []runnerwire.RepoStatusEntry{}}
		}
		return d.Status
	})
}

func (a *API) repoLog(c *gin.Context) {
	if _, ok := person(c); !ok {
		return
	}
	limit := runnerwire.DefaultRepoLogEntries
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > runnerwire.MaxRepoLogEntries {
			httpx.Coded(c, http.StatusBadRequest, "INVALID_ARGUMENT", errors.New("limit is between 1 and 100"), nil)
			return
		}
		limit = n
	}
	a.browse(c, runnerwire.RepoRequest{Op: runnerwire.RepoOpLog, Limit: limit}, func(d runnerwire.RepoDone) any {
		log := d.Log
		if log == nil {
			log = []runnerwire.RepoCommit{}
		}
		return gin.H{"commits": log}
	})
}

func (a *API) repoDiff(c *gin.Context) {
	if _, ok := person(c); !ok {
		return
	}
	ref := strings.TrimSpace(c.Query("ref"))
	if !runnerwire.ValidRepoRef(ref) {
		httpx.Coded(c, http.StatusBadRequest, "BAD_PATH", errors.New("ref is empty (the worktree) or a full commit id"), nil)
		return
	}
	a.browse(c, runnerwire.RepoRequest{Op: runnerwire.RepoOpDiff, Ref: ref}, func(d runnerwire.RepoDone) any {
		if d.Diff == nil {
			return runnerwire.RepoDiff{Ref: ref}
		}
		return d.Diff
	})
}
