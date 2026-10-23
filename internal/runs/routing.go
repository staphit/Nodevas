package runs

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"nodevas/internal/runnerwire"
)

// Routing (W5): each owner's ordered rules that pick an agent, model,
// fallback chain or pipeline for a node. There are no built-in routes; the
// rules only pre-fill what a person dispatches.

// Limits on routing fields.
const (
	// MaxRouteName caps agent, model, rule ID, runner ID, tag and kind values.
	MaxRouteName = 64
	// maxRouteProject caps RouteRule.Project.
	maxRouteProject = 256
)

var jobKinds = map[string]bool{
	runnerwire.JobImplement: true, runnerwire.JobReview: true, runnerwire.JobRevise: true,
}

var routeMatches = map[string]bool{
	runnerwire.RouteTag: true, runnerwire.RouteKind: true,
	runnerwire.RouteProject: true, runnerwire.RouteDefault: true,
}

// CleanFailureKind keeps a failure kind the contract knows; anything else is
// a task failure, which never falls back.
func CleanFailureKind(kind string) string {
	switch kind {
	case "", runnerwire.FailureRateLimit, runnerwire.FailureQuota, runnerwire.FailureModel,
		runnerwire.FailureAgentAbsent, runnerwire.FailureTask:
		return kind
	}
	return runnerwire.FailureTask
}

// CleanVerdict keeps a known verdict and drops anything else.
func CleanVerdict(verdict string) string {
	switch verdict {
	case runnerwire.VerdictApprove, runnerwire.VerdictChanges:
		return verdict
	}
	return ""
}

func encodeChain(chain []runnerwire.AgentModel) (string, error) {
	if len(chain) == 0 {
		return "", nil
	}
	raw, err := json.Marshal(chain)
	return string(raw), err
}

func decodeChain(raw string) []runnerwire.AgentModel {
	if raw == "" {
		return nil
	}
	var out []runnerwire.AgentModel
	if json.Unmarshal([]byte(raw), &out) != nil || len(out) == 0 {
		return nil
	}
	return out
}

func validName(s string) bool {
	return len(s) <= MaxRouteName && utf8.ValidString(s) && !strings.ContainsAny(s, "\x00\r\n")
}

// ValidateAgentModel checks one agent/model pair. requireModel is false where
// "" means the runner's default model (a dispatch); rules name one.
func ValidateAgentModel(am runnerwire.AgentModel, what string, requireModel bool) error {
	if strings.TrimSpace(am.Agent) == "" || !validName(am.Agent) {
		return invalidf("%s: agent must be 1 to %d characters", what, MaxRouteName)
	}
	if (requireModel && strings.TrimSpace(am.Model) == "") || !validName(am.Model) {
		return invalidf("%s: model must be 1 to %d characters", what, MaxRouteName)
	}
	return nil
}

// ValidateChain checks a fallback chain.
func ValidateChain(chain []runnerwire.AgentModel, requireModel bool) error {
	if len(chain) > runnerwire.MaxFallbacks {
		return invalidf("at most %d fallbacks", runnerwire.MaxFallbacks)
	}
	for i, am := range chain {
		if err := ValidateAgentModel(am, fmt.Sprintf("fallback %d", i+1), requireModel); err != nil {
			return err
		}
	}
	return nil
}

// ValidatePipeline checks a pipeline's steps and rounds.
func ValidatePipeline(p runnerwire.Pipeline, requireModel bool) error {
	if p.MaxRounds < 1 || p.MaxRounds > runnerwire.MaxPipelineRounds {
		return invalidf("pipeline maxRounds must be 1 to %d", runnerwire.MaxPipelineRounds)
	}
	if err := ValidateAgentModel(p.Implement, "pipeline implement", requireModel); err != nil {
		return err
	}
	return ValidateAgentModel(p.Review, "pipeline review", requireModel)
}

// ValidateRouting checks a whole rule list and fills in missing rule IDs. It
// returns the rules to store.
func ValidateRouting(rules []runnerwire.RouteRule) ([]runnerwire.RouteRule, error) {
	if len(rules) > runnerwire.MaxRouteRules {
		return nil, invalidf("at most %d rules", runnerwire.MaxRouteRules)
	}
	out := make([]runnerwire.RouteRule, 0, len(rules))
	seen := map[string]bool{}
	for i, rule := range rules {
		at := fmt.Sprintf("rule %d", i+1)
		rule.ID = strings.TrimSpace(rule.ID)
		if rule.ID == "" {
			id, err := newID()
			if err != nil {
				return nil, err
			}
			rule.ID = id
		}
		if !validName(rule.ID) || seen[rule.ID] {
			return nil, invalidf("%s: id must be unique and at most %d characters", at, MaxRouteName)
		}
		seen[rule.ID] = true
		if !routeMatches[rule.Match] {
			return nil, invalidf("%s: match must be tag, kind, project or default", at)
		}
		rule.Project = strings.TrimSpace(rule.Project)
		if len(rule.Project) > maxRouteProject || !utf8.ValidString(rule.Project) || strings.ContainsAny(rule.Project, "\x00\r\n") {
			return nil, invalidf("%s: project is not a valid project name", at)
		}
		rule.Value = strings.TrimSpace(rule.Value)
		switch rule.Match {
		case runnerwire.RouteTag, runnerwire.RouteKind:
			if rule.Value == "" || !validName(rule.Value) {
				return nil, invalidf("%s: a %s rule needs a value of at most %d characters", at, rule.Match, MaxRouteName)
			}
		case runnerwire.RouteProject:
			if rule.Project == "" {
				return nil, invalidf("%s: a project rule needs a project", at)
			}
			rule.Value = ""
		default:
			rule.Value = ""
		}
		if !validName(rule.RunnerID) {
			return nil, invalidf("%s: runnerId is at most %d characters", at, MaxRouteName)
		}
		if err := ValidateAgentModel(rule.Target, at+" target", true); err != nil {
			return nil, err
		}
		if err := ValidateChain(rule.Fallbacks, true); err != nil {
			return nil, fmt.Errorf("%w (%s)", err, at)
		}
		if len(rule.Fallbacks) == 0 {
			rule.Fallbacks = nil
		}
		if rule.Pipeline != nil {
			if err := ValidatePipeline(*rule.Pipeline, true); err != nil {
				return nil, fmt.Errorf("%w (%s)", err, at)
			}
		}
		out = append(out, rule)
	}
	return out, nil
}

// RouteNode is what the rules look at in a node.
type RouteNode struct {
	Project string
	Kind    string
	Tags    []string
}

// Suggest picks the rule for a node. Match kinds are tried in priority order
// (tag, kind, project, default); within one, rules for the node's project come
// before rules for every project (Project ""), each in list order. The first
// match wins; RuleID "" means none did.
func Suggest(rules []runnerwire.RouteRule, node RouteNode) runnerwire.RouteSuggestion {
	matches := func(rule runnerwire.RouteRule) bool {
		switch rule.Match {
		case runnerwire.RouteTag:
			return slices.ContainsFunc(node.Tags, func(tag string) bool {
				return strings.EqualFold(strings.TrimSpace(tag), rule.Value)
			})
		case runnerwire.RouteKind:
			return strings.EqualFold(nodeKind(node.Kind), rule.Value)
		case runnerwire.RouteProject:
			return rule.Project != "" && rule.Project == node.Project
		case runnerwire.RouteDefault:
			return true
		}
		return false
	}
	for _, match := range []string{runnerwire.RouteTag, runnerwire.RouteKind, runnerwire.RouteProject, runnerwire.RouteDefault} {
		for _, scoped := range []bool{true, false} {
			for _, rule := range rules {
				if rule.Match != match || (rule.Project != "") != scoped {
					continue
				}
				if rule.Project != "" && rule.Project != node.Project {
					continue
				}
				if matches(rule) {
					return runnerwire.RouteSuggestion{
						RuleID: rule.ID, Match: rule.Match, RunnerID: rule.RunnerID,
						Target: rule.Target, Fallbacks: rule.Fallbacks, Pipeline: rule.Pipeline,
					}
				}
			}
		}
	}
	return runnerwire.RouteSuggestion{}
}

// nodeKind is a node's kind as the board shows it: a node without one is a
// task.
func nodeKind(kind string) string {
	if strings.TrimSpace(kind) == "" {
		return "task"
	}
	return strings.TrimSpace(kind)
}

// ResolveAgentModel checks an agent and model against a runner's hello and
// returns the model to record ("" means the agent's default model). The
// error is a reason for a person.
func ResolveAgentModel(info *runnerwire.Info, am runnerwire.AgentModel) (string, error) {
	if info == nil {
		return "", fmt.Errorf("the runner has not connected yet, so its agents are unknown")
	}
	i := slices.IndexFunc(info.Agents, func(agent runnerwire.Agent) bool { return agent.Kind == am.Agent })
	if i < 0 || !info.Agents[i].Installed {
		return "", fmt.Errorf("agent %q is not installed on the runner", am.Agent)
	}
	agent := info.Agents[i]
	if am.Model == "" {
		for _, m := range agent.Models {
			if m.Default {
				return m.ID, nil
			}
		}
		return "default", nil
	}
	if !slices.ContainsFunc(agent.Models, func(m runnerwire.Model) bool { return m.ID == am.Model }) {
		return "", fmt.Errorf("model %q is not offered by %s on the runner", am.Model, agent.Kind)
	}
	return am.Model, nil
}

// Routing returns an owner's rules in order.
func (s *Store) Routing(ctx context.Context, ownerID string) ([]runnerwire.RouteRule, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT rule FROM run_routes WHERE owner_id = ? ORDER BY position`, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []runnerwire.RouteRule{}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var rule runnerwire.RouteRule
		if err := json.Unmarshal([]byte(raw), &rule); err != nil {
			return nil, err
		}
		out = append(out, rule)
	}
	return out, rows.Err()
}

// SetRouting validates and replaces an owner's rules, and returns them as
// stored (with generated IDs).
func (s *Store) SetRouting(ctx context.Context, ownerID string, rules []runnerwire.RouteRule) ([]runnerwire.RouteRule, error) {
	if ownerID == "" {
		return nil, invalidf("missing owner")
	}
	clean, err := ValidateRouting(rules)
	if err != nil {
		return nil, err
	}
	now := s.stamp()
	err = s.db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM run_routes WHERE owner_id = ?`, ownerID); err != nil {
			return err
		}
		for i, rule := range clean {
			raw, err := json.Marshal(rule)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO run_routes (owner_id, position, rule, updated_at) VALUES (?, ?, ?, ?)`,
				ownerID, int64(i), string(raw), now); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return clean, nil
}

// Note appends a hub-side log event to a run that has ended (a fallback, a
// pipeline step), after whatever its runner sent. A running run's events are
// its runner's alone.
func (s *Store) Note(ctx context.Context, runID, text string) error {
	return s.db.Tx(ctx, func(tx *sql.Tx) error {
		var status string
		if err := tx.QueryRowContext(ctx, `SELECT status FROM runs WHERE id = ?`, runID).Scan(&status); err != nil {
			return err
		}
		if !runnerwire.RunFinal(status) {
			// Its runner numbers the events; a hub line would take a seq the
			// runner is about to send.
			return nil
		}
		var last sql.NullInt64
		var count int
		if err := tx.QueryRowContext(ctx,
			`SELECT MAX(seq), COUNT(*) FROM run_events WHERE run_id = ?`, runID).Scan(&last, &count); err != nil {
			return err
		}
		if count >= runnerwire.MaxRunEvents+16 {
			return nil
		}
		event := cleanEvent(runnerwire.RunEvent{
			Seq: last.Int64 + 1, Type: runnerwire.EventLog, At: s.stamp(), Text: "hub: " + text,
		})
		payload, err := json.Marshal(event)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx,
			`INSERT INTO run_events (run_id, seq, type, at, payload) VALUES (?, ?, ?, ?, ?)`,
			runID, event.Seq, event.Type, event.At, string(payload))
		return err
	})
}
