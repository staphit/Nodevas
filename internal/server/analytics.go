package server

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"nodevas/internal/analytics"
	"nodevas/internal/audit"
	"nodevas/internal/auth"
	"nodevas/internal/autopilot"
	"nodevas/internal/db"
	analyticsapi "nodevas/internal/httpapi/analytics"
	explorerapi "nodevas/internal/httpapi/explorer"
	"nodevas/internal/identity"
	"nodevas/internal/runnerwire"
	"nodevas/internal/runs"
	"nodevas/internal/safego"
)

// Agent analytics on the server: background cost settlement and raw-history
// retention, the analytics and Explorer handlers, and auto routing for the
// dispatch dialog and the autopilot. Analytics is SQL and rules only; it
// never calls a model.

// retentionInterval is how often raw history is pruned.
const retentionInterval = 6 * time.Hour

// startAnalytics settles legacy run costs once and prunes raw history now
// and every six hours, until the server stops.
func (s *Server) startAnalytics(database *db.DB) {
	if database == nil || s.runs == nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.goBackground("analytics", func() {
		defer cancel()
		go func() {
			select {
			case <-s.stop:
				cancel()
			case <-ctx.Done():
			}
		}()
		if n, err := s.runs.Store().BackfillCosts(ctx); err != nil && ctx.Err() == nil {
			slog.Warn("analytics: settle legacy run costs", slog.Any("error", err))
		} else if n > 0 {
			slog.Info("analytics: settled legacy run costs", slog.Int("runs", n))
		}
		ticker := time.NewTicker(retentionInterval)
		defer ticker.Stop()
		for {
			// One bad pass (a panic) is logged and the loop carries on.
			safego.Call("analytics retention", func() { s.pruneHistory(ctx, database) })
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	})
}

// pruneHistory applies the retention setting once, and notes what it
// removed on the audit trail.
func (s *Server) pruneHistory(ctx context.Context, database *db.DB) {
	days, err := analytics.RetentionDays(ctx, database)
	if err != nil {
		if ctx.Err() == nil {
			slog.Warn("analytics: read retention", slog.Any("error", err))
		}
		return
	}
	res, err := analytics.Prune(ctx, database, time.Now(), days)
	if err != nil {
		if ctx.Err() == nil {
			slog.Warn("analytics: prune raw history", slog.Any("error", err))
		}
		return
	}
	if res.Total() > 0 && s.audit != nil {
		s.audit.RecordOrLog(ctx, audit.Event{At: time.Now(), ActorID: identity.Local.ID, ActorName: "nodevas",
			Action: "analytics.retention.prune", Target: analytics.RetentionSettingKey,
			Detail: map[string]any{"days": days, "events": res.Events, "actions": res.Actions, "picks": res.Picks, "patches": res.Patches}})
	}
}

// analyticsAPI is the /api/analytics handler set.
func (s *Server) analyticsAPI() *analyticsapi.API {
	return analyticsapi.New(analyticsapi.Options{
		DB: s.analyticsDB, PM: s.pm, Runs: s.runs.Store(),
		Audit:    s.auditRequest,
		Projects: s.readableRunProjects,
	})
}

// explorerAPI is the /api/explorer handler set.
func (s *Server) explorerAPI() *explorerapi.API {
	return explorerapi.New(explorerapi.Options{
		PM: s.pm, Runners: s.runners, Repo: s.repo, Runs: s.runs.Store(), Agents: s.agents, DB: s.analyticsDB,
	})
}

// auditRequest records an event a handler decides on, as the request's
// actor, on the request's project.
func (s *Server) auditRequest(r *http.Request, action, target string, detail map[string]any) {
	if s.audit == nil {
		return
	}
	actor := auth.ActorFrom(r)
	project := ""
	if s.pm != nil {
		if st := s.pm.Store(); st != nil {
			project = st.Root()
		}
		if name := r.URL.Query().Get("project"); name != "" {
			if st, err := s.pm.StoreFor(name); err == nil {
				project = st.Root()
			}
		}
	}
	s.audit.RecordOrLog(r.Context(), audit.Event{At: time.Now(), Project: project, ActorID: actor.ID, ActorName: actor.Name,
		Action: action, Target: target, Detail: detail})
}

// readableRunProjects names the projects with runs that r's actor may read.
func (s *Server) readableRunProjects(r *http.Request) []string {
	if s.analyticsDB == nil || s.pm == nil {
		return nil
	}
	rows, err := s.analyticsDB.QueryContext(r.Context(), `SELECT DISTINCT project FROM runs ORDER BY project`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if rows.Scan(&name) != nil {
			return out
		}
		st, err := s.pm.StoreFor(name)
		if err != nil || !s.projectReadable(r, st.Root()) {
			continue
		}
		out = append(out, name)
	}
	return out
}

// autoRouteRequest is GET /api/runs/route's auto routing.
func (s *Server) autoRouteRequest(r *http.Request, ownerID, project, nodeID, role string, manual runnerwire.RouteSuggestion) runnerwire.RouteSuggestion {
	return s.autoRoute(r.Context(), ownerID, project, nodeID, role, manual)
}

// autopilotRoute is the autopilot's auto routing (autopilot.Options.Route).
func (s *Server) autopilotRoute(ctx context.Context, in autopilot.RouteRequest) runnerwire.RouteSuggestion {
	return s.autoRoute(ctx, in.OwnerID, in.Project, in.NodeID, in.Role, in.Manual)
}

// autoRoute resolves what analytics.Route needs (the role's slot, the
// owner's runners, budgets and prices) and asks it. Manual rules always
// win; any failure keeps the rules' pick.
func (s *Server) autoRoute(ctx context.Context, ownerID, project, nodeID, role string, manual runnerwire.RouteSuggestion) runnerwire.RouteSuggestion {
	if s.analyticsDB == nil || s.runs == nil || s.runners == nil || ownerID == "" {
		return manual
	}
	slot := role
	if s.pm != nil {
		if st, err := s.pm.StoreFor(project); err == nil {
			if g, _, err := st.LoadGraph(); err == nil {
				if found, ok := g.RoleOf(role); ok {
					slot = found.Slot()
				}
			}
		}
	}
	in := analytics.RouteInput{OwnerID: ownerID, Project: project, NodeID: nodeID, Role: role, Slot: slot,
		Manual: manual, Now: time.Now()}
	if manual.RuleID == "" {
		settings, err := analytics.LoadSettings(ctx, s.analyticsDB, ownerID, project)
		if err != nil || !settings.AutoRouting {
			return manual
		}
		store := s.runs.Store()
		if in.Budget, err = store.BudgetStatus(ctx, ownerID); err != nil {
			return manual
		}
		if in.RoleBudget, err = store.RoleBudgetStatus(ctx, ownerID, role); err != nil {
			return manual
		}
		in.Prices = store.PriceTable(ctx)
		list, err := s.runners.List(ctx, identity.Actor{ID: ownerID})
		if err != nil {
			return manual
		}
		for _, r := range list {
			if r.OwnerID != ownerID || r.Info == nil {
				continue
			}
			in.Runners = append(in.Runners, analytics.RunnerCandidate{ID: r.ID, Name: r.Name,
				Online: s.runners.Online(r.ID), Info: *r.Info})
		}
	}
	out, err := analytics.Route(ctx, s.analyticsDB, in)
	if err != nil {
		slog.Warn("analytics: auto route", slog.String("project", project), slog.Any("error", err))
		return manual
	}
	return out
}

// changesScorecards reports a write that can change agent scorecards: a
// verdict, a person's review vote, or anything else under /api/runs that
// starts, settles or merges a run. Run settings (budget, roles, routing) do
// not.
func changesScorecards(method, path string) bool {
	if !isUnsafeMethod(method) {
		return false
	}
	if path == "/api/verdicts" || path == "/api/runs" {
		return true
	}
	rest, ok := strings.CutPrefix(path, "/api/runs/")
	if !ok {
		return false
	}
	switch rest {
	case "budget", "roles", "routing":
		return false
	}
	return true
}

// withScorecardInvalidation drops the cached scorecards after a successful
// write that changes them (changesScorecards). Runs settled over the runner
// socket are caught by the dispatcher observer in UseRunnersDB.
func (s *Server) withScorecardInvalidation(c *gin.Context) {
	c.Next()
	if c.Writer.Status() < http.StatusBadRequest && changesScorecards(c.Request.Method, c.Request.URL.Path) {
		analytics.InvalidateScorecards()
	}
}

// invalidateScorecardsOn is the dispatcher observer that drops the cached
// scorecards when a run ends or a pipeline moves.
func invalidateScorecardsOn(u runs.Update) {
	if u.Pipeline != nil || runnerwire.RunFinal(u.Run.Status) {
		analytics.InvalidateScorecards()
	}
}
