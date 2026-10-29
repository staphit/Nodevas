package runs

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"nodevas/internal/auth"
	"nodevas/internal/httpapi/httpx"
	"nodevas/internal/runnerwire"
	"nodevas/internal/runs"
)

// CodeBudgetExceeded is the error code of a run refused by its owner's daily
// budget (HTTP 429). The body's "exceeded" names the limit.
const CodeBudgetExceeded = "RESOURCE_EXHAUSTED"

const (
	maxMergeBody  = 4 << 10
	maxBudgetBody = 4 << 10
	// maxHeadSHA fits a SHA-256 object name; SHA-1 is 40.
	maxHeadSHA = 64
)

func validHeadSHA(sha string) bool {
	if sha == "" || len(sha) > maxHeadSHA {
		return false
	}
	for _, r := range sha {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F') {
			return false
		}
	}
	return true
}

func displayName(id, name string) string {
	if name == "" {
		return id
	}
	return name
}

// merge approves merging the run's pull request at the head the approver
// saw. The hub only records the approval; the run's runner merges, with its
// owner's own gh login, when it gets it. 202: the merge has not happened yet.
func (a *API) merge(c *gin.Context) {
	actor := auth.ActorFrom(c.Request)
	// A merge lands code. An agent approving its own (or another agent's)
	// work is exactly the loop the approval exists to break.
	if actor.IsAgent() {
		httpx.Coded(c, http.StatusForbidden, "PERMISSION_DENIED", errors.New("agents cannot approve merges"), nil)
		return
	}
	r, ok := a.visible(c)
	if !ok {
		return
	}
	var body runnerwire.MergeRequestBody
	if !httpx.DecodeJSONLimit(c, &body, maxMergeBody) {
		return
	}
	body.HeadSHA = strings.TrimSpace(body.HeadSHA)
	if !validHeadSHA(body.HeadSHA) {
		invalid(c, "headSha must be the PR head commit you are approving")
		return
	}
	updated, err := a.d.Merge(c.Request.Context(), r.ID,
		runs.Approver{ID: actor.ID, Name: displayName(actor.ID, actor.Name)}, body.HeadSHA)
	if err != nil {
		fail(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusAccepted, forReader(c, updated.Run))
}

// budget returns the caller's budget and what runs on their runners used
// today (UTC).
func (a *API) budget(c *gin.Context) {
	actor := auth.ActorFrom(c.Request)
	status, err := a.d.Store().BudgetStatus(c.Request.Context(), actor.ID)
	if err != nil {
		fail(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, status)
}

// setBudget replaces the caller's budget. Zero means no limit.
func (a *API) setBudget(c *gin.Context) {
	actor := auth.ActorFrom(c.Request)
	// A budget is a brake on agents; an agent must not be able to release it.
	if actor.IsAgent() {
		httpx.Coded(c, http.StatusForbidden, "PERMISSION_DENIED", errors.New("agents cannot change budgets"), nil)
		return
	}
	var body runnerwire.Budget
	if !httpx.DecodeJSONLimit(c, &body, maxBudgetBody) {
		return
	}
	if err := a.d.Store().SetBudget(c.Request.Context(), actor.ID, body); err != nil {
		fail(c, err)
		return
	}
	a.budget(c)
}
