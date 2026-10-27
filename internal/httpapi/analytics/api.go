// Package analytics serves agent analytics: insights, scorecards, tiers,
// auto-routing settings and picks, suggestions and the retention setting.
// Every figure is a SQL aggregate and a deterministic rule
// (internal/analytics); nothing here calls a model.
package analytics

import (
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"nodevas/internal/analytics"
	"nodevas/internal/auth"
	"nodevas/internal/db"
	"nodevas/internal/engine"
	"nodevas/internal/httpapi/httpx"
	"nodevas/internal/identity"
	"nodevas/internal/project"
	"nodevas/internal/runnerwire"
	"nodevas/internal/runs"
)

// maxBody bounds every analytics request body.
const maxBody = 16 << 10

// Options are what the handlers read and write.
type Options struct {
	DB   *db.DB
	PM   *project.ProjectManager
	Runs *runs.Store
	// Audit records an event on the audit trail; nil records nothing.
	Audit func(r *http.Request, action, target string, detail map[string]any)
	// Projects lists the project names the request's actor may read.
	Projects func(r *http.Request) []string
	// Now is the clock; nil is time.Now.
	Now func() time.Time
}

// API exposes internal/analytics over HTTP.
type API struct{ o Options }

// New builds the handlers.
func New(o Options) *API {
	if o.Now == nil {
		o.Now = time.Now
	}
	return &API{o: o}
}

func invalid(c *gin.Context, err error) {
	httpx.Coded(c, http.StatusBadRequest, "INVALID_ARGUMENT", err, nil)
}

// person is the request's actor, or a 403 for an agent: analytics is a
// person's.
func person(c *gin.Context) (identity.Actor, bool) {
	actor := auth.ActorFrom(c.Request)
	if actor.IsAgent() {
		httpx.Coded(c, http.StatusForbidden, "PERMISSION_DENIED", errors.New("agent analytics is for people, not agents"), nil)
		return actor, false
	}
	return actor, true
}

// projectName is the request's project as ?project= takes it.
func (a *API) projectName(c *gin.Context) (string, bool) {
	st := httpx.StoreFor(c.Request, a.o.PM)
	if a.o.PM == nil || st == nil {
		invalid(c, errors.New("no project selected"))
		return "", false
	}
	rel, err := filepath.Rel(a.o.PM.Workspace(), st.Root())
	if err != nil {
		invalid(c, errors.New("no project selected"))
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// days is ?days=, 1 to 365, default 30.
func days(c *gin.Context) (int, bool) {
	raw := strings.TrimSpace(c.Query("days"))
	if raw == "" {
		return analytics.DefaultDays, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > analytics.MaxDays {
		invalid(c, errors.New("days is between 1 and 365"))
		return 0, false
	}
	return n, true
}

func (a *API) insights(c *gin.Context) {
	actor, ok := person(c)
	if !ok {
		return
	}
	n, ok := days(c)
	if !ok {
		return
	}
	name, ok := a.projectName(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	q := analytics.Query{Project: name, Days: n, Now: a.o.Now()}
	if !actor.IsAdmin() {
		q.Viewer = actor.ID
	}
	out, err := analytics.InsightsFor(ctx, a.o.DB, q)
	if err != nil {
		httpx.Err(c, http.StatusInternalServerError, err)
		return
	}
	in, err := a.suggestInput(c, actor.ID, name, n)
	if err != nil {
		httpx.Err(c, http.StatusInternalServerError, err)
		return
	}
	suggestions, err := analytics.Suggestions(ctx, a.o.DB, in)
	if err != nil {
		httpx.Err(c, http.StatusInternalServerError, err)
		return
	}
	out.Suggestions = suggestions
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, out)
}

// suggestInput gathers what the suggestion rules read for owner in project.
func (a *API) suggestInput(c *gin.Context, owner, name string, n int) (analytics.SuggestInput, error) {
	ctx := c.Request.Context()
	in := analytics.SuggestInput{OwnerID: owner, Project: name, Days: n, Now: a.o.Now(), RoleDailyCost: map[string]float64{}}
	rules, err := a.o.Runs.Routing(ctx, owner)
	if err != nil {
		return in, err
	}
	in.Rules = rules
	if in.Settings, err = analytics.LoadSettings(ctx, a.o.DB, owner, name); err != nil {
		return in, err
	}
	roles, err := a.o.Runs.RoleSettings(ctx, owner)
	if err != nil {
		return in, err
	}
	for _, r := range roles {
		if r.Project == "" {
			in.RoleDailyCost[r.Role] = r.DailyCostUSD
		}
	}
	if st := httpx.StoreFor(c.Request, a.o.PM); st != nil {
		if g, _, err := st.LoadGraph(); err == nil && g != nil {
			if wf := g.Workflow.Normalized(); wf != nil && wf.Stages.Tester {
				in.TesterStage = true
			}
		}
	}
	return in, nil
}

func (a *API) overview(c *gin.Context) {
	actor, ok := person(c)
	if !ok {
		return
	}
	n, ok := days(c)
	if !ok {
		return
	}
	var projects []string
	if a.o.Projects != nil {
		projects = a.o.Projects(c.Request)
	}
	viewer := actor.ID
	if actor.IsAdmin() {
		viewer = ""
	}
	out, err := analytics.OverviewFor(c.Request.Context(), a.o.DB, projects, viewer, n, a.o.Now())
	if err != nil {
		httpx.Err(c, http.StatusInternalServerError, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, out)
}

// owner is whose scorecards a request reads: the actor's own, or with
// ?owner= an administrator's pick.
func owner(c *gin.Context, actor identity.Actor) (string, bool) {
	other := strings.TrimSpace(c.Query("owner"))
	if other == "" || other == actor.ID {
		return actor.ID, true
	}
	if !actor.IsAdmin() {
		httpx.Coded(c, http.StatusForbidden, "PERMISSION_DENIED", errors.New("only an administrator reads another person's scorecards"), nil)
		return "", false
	}
	return other, true
}

func (a *API) scorecards(c *gin.Context) {
	actor, ok := person(c)
	if !ok {
		return
	}
	n, ok := days(c)
	if !ok {
		return
	}
	track := strings.TrimSpace(c.Query("track"))
	switch track {
	case "", analytics.TrackAll, analytics.TrackCode, analytics.TrackStory:
	default:
		invalid(c, errors.New("track is all, code or story"))
		return
	}
	who, ok := owner(c, actor)
	if !ok {
		return
	}
	out, err := analytics.ScorecardsFor(c.Request.Context(), a.o.DB, analytics.Query{OwnerID: who, Days: n, Track: track, Now: a.o.Now()})
	if err != nil {
		httpx.Err(c, http.StatusInternalServerError, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, out)
}

func (a *API) tiers(c *gin.Context) {
	actor, ok := person(c)
	if !ok {
		return
	}
	who, ok := owner(c, actor)
	if !ok {
		return
	}
	out, err := analytics.TiersFor(c.Request.Context(), a.o.DB, who, "", a.o.Now())
	if err != nil {
		httpx.Err(c, http.StatusInternalServerError, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, out)
}

func (a *API) settings(c *gin.Context) {
	actor, ok := person(c)
	if !ok {
		return
	}
	name, ok := a.projectName(c)
	if !ok {
		return
	}
	out, err := analytics.LoadSettings(c.Request.Context(), a.o.DB, actor.ID, name)
	if err != nil {
		httpx.Err(c, http.StatusInternalServerError, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, out)
}

func (a *API) putSettings(c *gin.Context) {
	actor, ok := person(c)
	if !ok {
		return
	}
	name, ok := a.projectName(c)
	if !ok {
		return
	}
	var body analytics.Settings
	if !httpx.DecodeJSONLimit(c, &body, maxBody) {
		return
	}
	body.Project = name
	if err := analytics.SaveSettings(c.Request.Context(), a.o.DB, actor.ID, body); err != nil {
		if errors.Is(err, analytics.ErrInvalid) {
			invalid(c, err)
			return
		}
		httpx.Err(c, http.StatusInternalServerError, err)
		return
	}
	out, err := analytics.LoadSettings(c.Request.Context(), a.o.DB, actor.ID, name)
	if err != nil {
		httpx.Err(c, http.StatusInternalServerError, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, out)
}

func (a *API) routePicks(c *gin.Context) {
	actor, ok := person(c)
	if !ok {
		return
	}
	name, ok := a.projectName(c)
	if !ok {
		return
	}
	limit := 50
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > analytics.MaxPicksListed {
			invalid(c, fmt.Errorf("limit is between 1 and %d", analytics.MaxPicksListed))
			return
		}
		limit = n
	}
	who := actor.ID
	if actor.IsAdmin() {
		who = ""
	}
	picks, err := analytics.ListPicks(c.Request.Context(), a.o.DB, who, name, limit)
	if err != nil {
		httpx.Err(c, http.StatusInternalServerError, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{"picks": picks})
}

type applyBody struct {
	Key    string           `json:"key"`
	Change analytics.Change `json:"change"`
}

// apply makes the change a suggestion proposes, as the actor and only when
// it is still exactly what the suggestion says. Nothing is ever applied
// without a person asking.
func (a *API) apply(c *gin.Context) {
	actor, ok := person(c)
	if !ok {
		return
	}
	var body applyBody
	if !httpx.DecodeJSONLimit(c, &body, maxBody) {
		return
	}
	switch body.Change.Type {
	case analytics.ChangeRoutingRuleTarget, analytics.ChangeRoleBudget, analytics.ChangeAutoRouting:
	case analytics.ChangeOpenSettings:
		invalid(c, errors.New("this suggestion opens a setting; there is nothing to apply"))
		return
	default:
		invalid(c, errors.New("unknown change type"))
		return
	}
	name, ok := a.projectName(c)
	if !ok {
		return
	}
	n, ok := days(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	in, err := a.suggestInput(c, actor.ID, name, n)
	if err != nil {
		httpx.Err(c, http.StatusInternalServerError, err)
		return
	}
	in.IncludeDismissed = true
	current, err := analytics.Suggestions(ctx, a.o.DB, in)
	if err != nil {
		httpx.Err(c, http.StatusInternalServerError, err)
		return
	}
	var match *analytics.Suggestion
	for i := range current {
		if current[i].Key == body.Key {
			match = &current[i]
		}
	}
	if match == nil || match.Change == nil || !reflect.DeepEqual(*match.Change, body.Change) {
		httpx.Coded(c, http.StatusConflict, "STALE_SUGGESTION",
			errors.New("this suggestion changed; review it again"), map[string]any{"suggestion": match})
		return
	}
	change := body.Change
	switch change.Type {
	case analytics.ChangeRoutingRuleTarget:
		err = a.retarget(c, actor.ID, change)
	case analytics.ChangeRoleBudget:
		err = a.roleBudget(c, actor.ID, change)
	case analytics.ChangeAutoRouting:
		var s analytics.Settings
		if s, err = analytics.LoadSettings(ctx, a.o.DB, actor.ID, change.Project); err == nil {
			s.AutoRouting = change.On
			err = analytics.SaveSettings(ctx, a.o.DB, actor.ID, s)
		}
	}
	if err != nil {
		var stale *staleError
		switch {
		case errors.As(err, &stale):
			httpx.Coded(c, http.StatusConflict, "STALE_SUGGESTION", err, map[string]any{"suggestion": match})
		case errors.Is(err, runs.ErrInvalid), errors.Is(err, analytics.ErrInvalid):
			invalid(c, err)
		default:
			httpx.Err(c, http.StatusInternalServerError, err)
		}
		return
	}
	if a.o.Audit != nil {
		a.o.Audit(c.Request, "analytics.suggestion.applied", body.Key, map[string]any{"type": change.Type, "change": change})
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{"applied": true, "key": body.Key})
}

type staleError struct{ msg string }

func (e *staleError) Error() string { return e.msg }

// retarget points one of owner's routing rules at the suggested target,
// through the store's full validation.
func (a *API) retarget(c *gin.Context, owner string, change analytics.Change) error {
	if change.From == nil || change.To == nil || change.RuleID == "" {
		return fmt.Errorf("%w: a retarget names its rule, from and to", analytics.ErrInvalid)
	}
	ctx := c.Request.Context()
	rules, err := a.o.Runs.Routing(ctx, owner)
	if err != nil {
		return err
	}
	found := false
	for i := range rules {
		if rules[i].ID != change.RuleID {
			continue
		}
		if rules[i].Target != *change.From {
			return &staleError{msg: "the rule's target changed since the suggestion"}
		}
		rules[i].Target = runnerwire.AgentModel{Agent: change.To.Agent, Model: change.To.Model}
		found = true
	}
	if !found {
		return &staleError{msg: "the rule is gone"}
	}
	_, err = a.o.Runs.SetRouting(ctx, owner, rules)
	return err
}

// roleBudget sets a role's daily cost limit (every project), keeping its
// other limits.
func (a *API) roleBudget(c *gin.Context, owner string, change analytics.Change) error {
	if !engine.ValidRoleID(change.Role) || change.DailyCostUSD <= 0 {
		return fmt.Errorf("%w: a role budget names a role and a cost", analytics.ErrInvalid)
	}
	ctx := c.Request.Context()
	settings, err := a.o.Runs.RoleSettings(ctx, owner)
	if err != nil {
		return err
	}
	found := false
	for i := range settings {
		if settings[i].Role == change.Role && settings[i].Project == "" {
			settings[i].DailyCostUSD = change.DailyCostUSD
			found = true
		}
	}
	if !found {
		settings = append(settings, runnerwire.RoleSettings{Role: change.Role, DailyCostUSD: change.DailyCostUSD})
	}
	_, err = a.o.Runs.SetRoleSettings(ctx, owner, settings)
	return err
}

func (a *API) dismiss(c *gin.Context) {
	actor, ok := person(c)
	if !ok {
		return
	}
	var body struct {
		Key string `json:"key"`
	}
	if !httpx.DecodeJSONLimit(c, &body, maxBody) {
		return
	}
	if err := analytics.Dismiss(c.Request.Context(), a.o.DB, actor.ID, strings.TrimSpace(body.Key), a.o.Now()); err != nil {
		if errors.Is(err, analytics.ErrInvalid) {
			invalid(c, errors.New("key is required"))
			return
		}
		httpx.Err(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"dismissed": true})
}

// admin answers 403 unless the actor is an administrator (and a person).
func admin(c *gin.Context) bool {
	if _, ok := person(c); !ok {
		return false
	}
	if err := auth.RequireAdmin(c.Request); err != nil {
		httpx.Coded(c, http.StatusForbidden, "PERMISSION_DENIED", errors.New("administrator access required"), nil)
		return false
	}
	return true
}

func (a *API) retention(c *gin.Context) {
	if !admin(c) {
		return
	}
	n, err := analytics.RetentionDays(c.Request.Context(), a.o.DB)
	if err != nil {
		httpx.Err(c, http.StatusInternalServerError, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{"days": n})
}

func (a *API) putRetention(c *gin.Context) {
	if !admin(c) {
		return
	}
	var body struct {
		Days *int `json:"days"`
	}
	if !httpx.DecodeJSONLimit(c, &body, maxBody) {
		return
	}
	if body.Days == nil || !analytics.ValidRetentionDays(*body.Days) {
		invalid(c, fmt.Errorf("days is 0 (forever) or %d to %d", analytics.MinRetentionDays, analytics.MaxRetentionDays))
		return
	}
	if err := a.o.DB.SetSetting(c.Request.Context(), analytics.RetentionSettingKey, strconv.Itoa(*body.Days)); err != nil {
		httpx.Err(c, http.StatusInternalServerError, err)
		return
	}
	if a.o.Audit != nil {
		a.o.Audit(c.Request, "analytics.retention.set", analytics.RetentionSettingKey, map[string]any{"days": *body.Days})
	}
	c.JSON(http.StatusOK, gin.H{"days": *body.Days})
}
