package runs

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"nodevas/internal/analytics"
	"nodevas/internal/auth"
	"nodevas/internal/engine"
	"nodevas/internal/httpapi/httpx"
	"nodevas/internal/runnerwire"
	"nodevas/internal/runs"
	"nodevas/internal/store"
)

// maxVerdictBody fits a verdict with its note and a QA run's checks.
const maxVerdictBody = 64 << 10

// verdictBody is POST /api/verdicts. Checks are a QA run's own record of
// what it judged; the hub keeps the verdict, not the checks.
type verdictBody struct {
	runs.VerdictInput
	Checks []runnerwire.CheckResult `json:"checks,omitempty"`
}

// submitVerdict records a reviewer's or QA run's structured verdict. The
// role is the run record's, never the request's: only a run reaching the
// hub through its runner's proxy, as a reviewer or QA, may post one.
func (a *API) submitVerdict(c *gin.Context) {
	actor := auth.ActorFrom(c.Request)
	if actor.RunID == "" {
		httpx.Coded(c, http.StatusForbidden, "VERDICT_NOT_A_RUN",
			errors.New("only a review or QA run submits a verdict"), nil)
		return
	}
	ctx := c.Request.Context()
	rec, err := a.d.Store().Get(ctx, actor.RunID)
	if err != nil {
		fail(c, err)
		return
	}
	role := a.verdictRole(rec)
	if role == "" {
		httpx.Coded(c, http.StatusForbidden, store.CodeRoleDenied, runs.ErrVerdictRole, map[string]any{"role": rec.Role})
		return
	}
	if rec.Status != runnerwire.RunRunning {
		httpx.Coded(c, http.StatusConflict, "RUN_NOT_RUNNING", errors.New("this run is not running"), nil)
		return
	}
	var body verdictBody
	if !httpx.DecodeJSONLimit(c, &body, maxVerdictBody) {
		return
	}
	v, replaced, err := a.d.Store().SubmitVerdictAs(ctx, rec, role, body.VerdictInput)
	switch {
	case errors.Is(err, runs.ErrVerdictRole):
		httpx.Coded(c, http.StatusForbidden, store.CodeRoleDenied, err, nil)
		return
	case err != nil:
		fail(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{"round": v.Round, "replaced": replaced})
}

// verdictRole is the verdict role of rec's recorded role: its slot (a
// custom role's base) when that is reviewer or QA, a legacy run's by its
// kind, else "".
func (a *API) verdictRole(rec runs.Record) string {
	if rec.Role == "" || !strings.HasPrefix(rec.Role, engine.CustomRolePrefix) {
		return runs.VerdictRole(rec)
	}
	if a.pm == nil {
		return ""
	}
	st, err := a.pm.StoreFor(rec.Project)
	if err != nil {
		return ""
	}
	g, _, err := st.LoadGraph()
	if err != nil {
		return ""
	}
	role, found := g.RoleOf(rec.Role)
	if !found {
		return ""
	}
	switch slot := role.Slot(); slot {
	case engine.RoleReviewer, engine.RoleQA:
		return slot
	}
	return ""
}

// verdicts is GET /api/runs/:id/verdicts: the run's verdict (if it gave
// one), with whether later evidence contradicts it.
func (a *API) verdicts(c *gin.Context) {
	if auth.ActorFrom(c.Request).IsAgent() {
		httpx.Coded(c, http.StatusForbidden, "PERMISSION_DENIED", errors.New("agents cannot read verdicts"), nil)
		return
	}
	r, ok := a.visible(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	out := []runs.Verdict{}
	v, found, err := a.d.Store().VerdictFor(ctx, r.ID)
	if err != nil {
		fail(c, err)
		return
	}
	if found {
		if contradicted, err := analytics.Contradicted(ctx, a.d.Store().DB(), []string{r.ID}); err == nil {
			v.Contradicted = contradicted[r.ID]
		}
		out = append(out, v)
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{"verdicts": out})
}
