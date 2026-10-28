// Agent roles on the board: which role may do what to which node type, the
// acceptance lock, and the workflow stage gates (spec: agent roles and
// workflow stages, section 2.4).
//
// A legacy actor (a person, or an agent without a role) passes every role
// check unchanged; the role checks only ever narrow what an agent may do.

package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"nodevas/internal/engine"
	"nodevas/internal/identity"
)

// Role and stage error codes, shared with the web client.
const (
	CodeRoleDenied         = "ROLE_DENIED"              // 403
	CodeStageGatePending   = "STAGE_GATE_PENDING"       // 409
	CodeAcceptanceLocked   = "ACCEPTANCE_LOCKED"        // 409
	CodeRolesAgentEdit     = "AGENT_ROLES_AGENT_DENIED" // 403
	CodeWorkflowAgentEdit  = "WORKFLOW_AGENT_DENIED"    // 403
	CodeAcceptanceRequired = "ACCEPTANCE_REQUIRED"      // 409
	CodeUnknownRole        = "UNKNOWN_ROLE"             // 403
)

// ErrRoleDenied is a role, stage or acceptance refusal. Status is the HTTP
// status the API answers with.
type ErrRoleDenied struct {
	NodeID, Role, Code, Msg string
	Status                  int
}

func (e *ErrRoleDenied) Error() string { return e.Msg }

func roleDenied(nodeID, role, format string, args ...any) error {
	return &ErrRoleDenied{NodeID: nodeID, Role: role, Code: CodeRoleDenied, Status: http.StatusForbidden,
		Msg: fmt.Sprintf(format, args...)}
}

// isSystemActor reports whether actor is the hub acting on its own behalf.
func isSystemActor(actor identity.Actor) bool { return actor == identity.System }

// actorRole resolves the actor's role against the project. ok is false for a
// legacy actor (no role checks apply).
func actorRole(actor identity.Actor, g *engine.Graph) (engine.AgentRole, bool, error) {
	if !actor.HasRole() {
		return engine.AgentRole{}, false, nil
	}
	role, found := g.RoleOf(actor.AgentRole)
	if !found {
		return engine.AgentRole{}, false, &ErrRoleDenied{Role: actor.AgentRole, Code: CodeUnknownRole,
			Status: http.StatusForbidden, Msg: fmt.Sprintf("unknown agent role %q in this project", actor.AgentRole)}
	}
	return role, true, nil
}

// builtinRoleClass is the rank a built-in role id carries ("" for a custom or
// unknown id, whose rank the server resolves against the project before the
// request reaches the store).
func builtinRoleClass(id string) identity.AgentClass {
	for _, r := range engine.BuiltinAgentRoles() {
		if r.ID == id {
			return r.Rank
		}
	}
	return ""
}

// effectiveClass is the class checkNodeWrite ranks an agent at: a role never
// raises what the request declared.
func effectiveClass(actor identity.Actor) identity.AgentClass {
	if !actor.HasRole() {
		return actor.Agent
	}
	if rank := builtinRoleClass(actor.AgentRole); rank != "" {
		return identity.LowerClass(actor.Agent, rank)
	}
	return actor.Agent
}

// executorBodyTypes are the story types whose body an Executor writes as its
// work (the scene or event it was dispatched on).
var executorBodyTypes = map[string]bool{"scene": true, "event": true}

// checkRole decides whether a roled actor may use cap on node n (nil for a
// capability that is not about one node).
func checkRole(actor identity.Actor, g *engine.Graph, c engine.Capability, n *engine.Node) error {
	role, ok, err := actorRole(actor, g)
	if err != nil || !ok {
		return err
	}
	nodeID, typeID := "", ""
	if n != nil {
		nodeID, typeID = n.ID, g.TypeOf(n).ID
	}
	slot := role.Slot()
	// An Executor dispatched on a scene or event writes that node's body: the
	// prose is its work product, as code is for a task.
	if c == engine.CapBody && slot == engine.RoleExecutor && !role.HasCap(c) {
		if n != nil && actor.RunNodeID != "" && n.ID == actor.RunNodeID && executorBodyTypes[typeID] && role.MayWriteType(typeID) {
			return nil
		}
		return roleDenied(nodeID, role.ID, "role %s may only write the body of the scene or event it was dispatched on", role.ID)
	}
	if !role.HasCap(c) {
		return roleDenied(nodeID, role.ID, "role %s may not %s (needs the %s capability)", role.ID, capVerb(c), c)
	}
	switch c {
	case engine.CapBody, engine.CapClaim, engine.CapStatus, engine.CapCreate:
		if n != nil && !role.MayWriteType(typeID) {
			return roleDenied(nodeID, role.ID, "role %s may not %s a %s node", role.ID, capVerb(c), typeID)
		}
	}
	if slot == engine.RoleExecutor && actor.RunID != "" && n != nil {
		switch c {
		case engine.CapBody, engine.CapClaim, engine.CapStatus:
			if n.ID != actor.RunNodeID {
				return roleDenied(nodeID, role.ID, "role %s works only node %q, the node its run was dispatched on", role.ID, actor.RunNodeID)
			}
		}
	}
	if slot == engine.RoleQA && c == engine.CapCreate && n != nil {
		if !qaOwnsNewNode(actor, n) {
			want := "goal:<id>"
			if actor.RunNodeID != "" {
				want = engine.GoalTag(actor.RunNodeID)
			}
			return roleDenied(nodeID, role.ID, "role qa creates fix nodes only under its own goal (tag %s)", want)
		}
	}
	return nil
}

// qaOwnsNewNode reports whether a node QA creates sits under QA's own goal.
func qaOwnsNewNode(actor identity.Actor, n *engine.Node) bool {
	for _, tag := range n.Tags {
		if actor.RunNodeID != "" {
			if tag == engine.GoalTag(actor.RunNodeID) {
				return true
			}
		} else if strings.HasPrefix(tag, "goal:") && len(tag) > len("goal:") {
			return true
		}
	}
	return false
}

// checkRoleLink is checkRole for wiring: a researcher may add or remove
// references (context) but never requires edges (the plan).
func checkRoleLink(actor identity.Actor, g *engine.Graph, n *engine.Node, refOnly bool) error {
	if err := checkRole(actor, g, engine.CapLink, n); err != nil {
		return err
	}
	role, ok, err := actorRole(actor, g)
	if err != nil || !ok {
		return err
	}
	if !refOnly && role.Slot() == engine.RoleResearcher {
		nodeID := ""
		if n != nil {
			nodeID = n.ID
		}
		return roleDenied(nodeID, role.ID, "role %s may link references only, not dependencies", role.ID)
	}
	return nil
}

// checkRoleDelete refuses every roled agent: no built-in role deletes.
func checkRoleDelete(actor identity.Actor, nodeID string) error {
	if !actor.HasRole() {
		return nil
	}
	return roleDenied(nodeID, actor.AgentRole, "role %s may not delete nodes; ask a person", actor.AgentRole)
}

func capVerb(c engine.Capability) string {
	switch c {
	case engine.CapRead:
		return "read the board"
	case engine.CapClaim:
		return "claim or release"
	case engine.CapStatus:
		return "set the status of"
	case engine.CapBody:
		return "write the body of"
	case engine.CapCreate:
		return "create"
	case engine.CapLink:
		return "link"
	case engine.CapMeta:
		return "edit the metadata of"
	case engine.CapAcceptance:
		return "write acceptance criteria"
	case engine.CapMessage:
		return "message"
	}
	return string(c)
}

// acceptanceAttribution is the By an agent's acceptance write records.
func acceptanceAttribution(actor identity.Actor) string {
	if actor.HasRole() {
		if actor.RunID != "" {
			return fmt.Sprintf("%s (run %s)", actor.AgentRole, actor.RunID)
		}
		return actor.AgentRole
	}
	if actor.Name != "" {
		return actor.Name
	}
	return actor.ID
}

// normalizeAcceptance is what an actor's acceptance write stores: the lock
// stays as it was (only the hub's LockAcceptance changes it) and By names
// the writer when the criteria changed. Ids are filled in.
func normalizeAcceptance(actor identity.Actor, before, after *engine.Acceptance) *engine.Acceptance {
	if after == nil {
		if before != nil && before.Locked && !actor.IsAgent() {
			// A person clearing locked criteria keeps the lock on an empty list.
			return &engine.Acceptance{Items: []engine.AcceptanceItem{}, Locked: true, By: acceptanceAttribution(actor)}
		}
		return nil
	}
	out := &engine.Acceptance{Items: engine.AssignAcceptanceIDs(after.Items), Locked: after.Locked, By: after.By}
	if out.Items == nil {
		out.Items = []engine.AcceptanceItem{}
	}
	if actor.IsAgent() {
		// An agent's lock and attribution are what they were; the check
		// below refuses a change it tried to make.
		return out
	}
	if before != nil {
		out.Locked = before.Locked
	} else {
		out.Locked = false
	}
	if before.EqualItems(out) && before != nil {
		out.By = before.By
	} else {
		out.By = acceptanceAttribution(actor)
	}
	if !out.Locked && len(out.Items) == 0 {
		return nil
	}
	return out
}

// checkAcceptanceWrite decides an acceptance change on node n. createdByRun
// reports whether the actor's own run created n (QA may write criteria on the
// fix tasks it creates).
func checkAcceptanceWrite(actor identity.Actor, g *engine.Graph, n *engine.Node, before, after *engine.Acceptance, createdByRun bool) error {
	if acceptanceEqual(before, after) {
		return nil
	}
	nodeID := ""
	if n != nil {
		nodeID = n.ID
	}
	if after != nil && !g.AcceptanceAllowed(n) {
		return &ErrNodeTypeDenied{NodeID: nodeID, Type: g.TypeOf(n).ID, Code: CodeNodeTypeForbidsAgent, Status: http.StatusBadRequest,
			Msg: fmt.Sprintf("node %q is a %s node: it takes no acceptance criteria", nodeID, typeName(g.TypeOf(n)))}
	}
	if isSystemActor(actor) || !actor.IsAgent() {
		return nil
	}
	if !actor.HasRole() {
		return roleDenied(nodeID, "", "acceptance criteria are written by a person or a planner/QA run, not by this agent")
	}
	role, _, err := actorRole(actor, g)
	if err != nil {
		return err
	}
	if !role.HasCap(engine.CapAcceptance) {
		return roleDenied(nodeID, role.ID, "role %s may not write acceptance criteria (they are written before the work, by the planner or QA)", role.ID)
	}
	typeID := g.TypeOf(n).ID
	if !role.MayAcceptType(typeID) && !(role.Slot() == engine.RoleQA && createdByRun) {
		return roleDenied(nodeID, role.ID, "role %s may not write acceptance criteria on a %s node", role.ID, typeID)
	}
	if before != nil && before.Locked {
		return &ErrRoleDenied{NodeID: nodeID, Role: role.ID, Code: CodeAcceptanceLocked, Status: http.StatusConflict,
			Msg: fmt.Sprintf("node %q: the acceptance criteria are locked (an Executor was dispatched); ask a person to change them", nodeID)}
	}
	wasLocked := before != nil && before.Locked
	isLocked := after != nil && after.Locked
	if wasLocked != isLocked {
		return roleDenied(nodeID, role.ID, "only the hub locks acceptance criteria")
	}
	return nil
}

func acceptanceEqual(a, b *engine.Acceptance) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Locked == b.Locked && a.By == b.By && a.EqualItems(b)
}

// checkStatusGate refuses an Executor (or an external agent session without a
// role) that settles its node while a later stage (tests verified, CI,
// review, a goal's QA or review) is still to run: the server marks the node
// done once the pipeline or the autopilot approves it.
func checkStatusGate(actor identity.Actor, g *engine.Graph, n *engine.Node, status engine.Status) error {
	if n == nil {
		return nil
	}
	role, ok, err := actorRole(actor, g)
	if err != nil {
		return err
	}
	roleID := role.ID
	switch {
	case ok && role.Slot() != engine.RoleExecutor:
		return nil
	case !ok:
		// A legacy agent: a person, or a run the hub dispatched without a
		// role (a human's "skip the stages" override, a legacy project), is
		// not gated. An external session without a role is the worker of
		// before roles, which on a staged project is an Executor: it may not
		// skip the stages by settling the node itself.
		if !actor.IsAgent() || actor.RunID != "" {
			return nil
		}
		roleID = ""
	}
	var custom []engine.StatusDefinition
	if g.UI != nil {
		custom = g.UI.CustomStatuses
	}
	if !engine.Settled(status, custom) {
		return nil
	}
	wf := engine.EffectiveWorkflow(g, g.GoalOf(n))
	if wf != nil && !ok && g.AcceptanceAllowed(n) && g.TypeOf(n).AgentMode == engine.AgentAutopilot &&
		(wf.Stages.GoalQA || wf.Stages.GoalReview) {
		return &ErrRoleDenied{NodeID: n.ID, Code: CodeStageGatePending, Status: http.StatusConflict,
			Msg: fmt.Sprintf("node %q: the autopilot marks this goal done after its QA verification or review; report your work and stop", n.ID)}
	}
	if !wf.Staged() || (!ok && !g.AcceptanceAllowed(n)) {
		return nil
	}
	next := "the tests are verified"
	switch {
	case wf.Stages.Reviewer:
		next = "review"
	case wf.Stages.CI:
		next = "CI passes"
	case wf.Judge == engine.JudgeChecklist:
		next = "the checklist is judged"
	}
	return &ErrRoleDenied{NodeID: n.ID, Role: roleID, Code: CodeStageGatePending, Status: http.StatusConflict,
		Msg: fmt.Sprintf("node %q: the server marks this done after %s; report your work and stop", n.ID, next)}
}

// checkAgentRolesEdit refuses an agent that changes the project's roles.
func checkAgentRolesEdit(actor identity.Actor, before, after []engine.AgentRoleDef) error {
	if !actor.IsAgent() || agentRoleDefsEqual(before, after) {
		return nil
	}
	return &ErrRoleDenied{Code: CodeRolesAgentEdit, Status: http.StatusForbidden,
		Msg: "agents may not change the project's agent roles"}
}

func agentRoleDefsEqual(a, b []engine.AgentRoleDef) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	return reflect.DeepEqual(a, b)
}

// checkWorkflowEdit refuses an agent that changes the project's workflow or
// a goal's override.
func checkWorkflowEdit(actor identity.Actor, nodeID string, before, after *engine.Workflow) error {
	if !actor.IsAgent() || reflect.DeepEqual(before, after) {
		return nil
	}
	msg := "agents may not change the project's workflow stages"
	if nodeID != "" {
		msg = fmt.Sprintf("agents may not change the workflow stages of %q", nodeID)
	}
	return &ErrRoleDenied{NodeID: nodeID, Code: CodeWorkflowAgentEdit, Status: http.StatusForbidden, Msg: msg}
}

// ---------- acceptance writes ----------

// SetAcceptance replaces a node's acceptance items (POST
// /api/nodes/:id/acceptance, the set_acceptance tool). Item ids are kept;
// new items get the next free aN id. Returns the new graph revision.
func (s *Store) SetAcceptance(actor identity.Actor, nodeID string, items []engine.AcceptanceItem, baseRev string) (string, error) {
	if !engine.ValidNodeID(nodeID) {
		return "", fmt.Errorf("invalid node id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if baseRev != "" {
		if err := s.checkRevision(s.GraphPath(), baseRev); err != nil {
			return "", err
		}
	}
	g, rev, err := s.loadGraphLocked()
	if err != nil {
		return "", err
	}
	n := g.NodeByID(nodeID)
	if n == nil {
		return "", fmt.Errorf("node %q not found", nodeID)
	}
	if err := checkNodeWrite(actor, n); err != nil {
		return "", err
	}
	before := n.Acceptance
	var after *engine.Acceptance
	if len(items) > 0 || (before != nil && before.Locked) {
		after = &engine.Acceptance{Items: engine.AssignAcceptanceIDs(items)}
		if before != nil {
			after.Locked, after.By = before.Locked, before.By
		}
		if after.Items == nil {
			after.Items = []engine.AcceptanceItem{}
		}
	}
	after = normalizeAcceptance(actor, before, after)
	if err := checkAcceptanceWrite(actor, g, n, before, after, s.createdByRunLocked(nodeID) == actor.RunID && actor.RunID != ""); err != nil {
		return "", err
	}
	if after != nil && actor.IsAgent() && !after.EqualItems(before) {
		after.By = acceptanceAttribution(actor)
	}
	n.Acceptance = after
	if err := engine.ValidateNodeAcceptance(g, n); err != nil {
		return "", err
	}
	return s.writeGraphLocked(actor, g, rev)
}

// LockAcceptance locks a node's acceptance as the hub system actor, at
// Executor dispatch. A node without criteria gets a locked empty list, so no
// agent can add criteria under an Executor that is already working; a type
// that takes no criteria is left as it is.
func (s *Store) LockAcceptance(nodeID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, rev, err := s.loadGraphLocked()
	if err != nil {
		return err
	}
	n := g.NodeByID(nodeID)
	if n == nil {
		return fmt.Errorf("node %q not found", nodeID)
	}
	if !g.AcceptanceAllowed(n) || (n.Acceptance != nil && n.Acceptance.Locked) {
		return nil
	}
	locked := engine.Acceptance{Items: []engine.AcceptanceItem{}}
	if n.Acceptance != nil {
		locked = *n.Acceptance
		locked.Items = append([]engine.AcceptanceItem{}, n.Acceptance.Items...)
	}
	locked.Locked = true
	n.Acceptance = &locked
	_, err = s.writeGraphLocked(identity.System, g, rev)
	return err
}

// writeGraphLocked stores g as graph.yaml (expecting rev) after validation.
// Node files are untouched: acceptance and workflow live in graph.yaml only.
// The caller holds s.mu.
func (s *Store) writeGraphLocked(actor identity.Actor, g *engine.Graph, rev string) (string, error) {
	if err := ValidateGraphForStorage(g); err != nil {
		return "", err
	}
	data, err := s.marshalGraph(g)
	if err != nil {
		return "", err
	}
	if err := s.applyUpdatesLocked(actor, []fileUpdate{{
		path: s.GraphPath(), data: data, checkRevision: true, expectedRev: rev,
	}}); err != nil {
		return "", err
	}
	return Rev(data), nil
}

// ---------- evidence ----------

// MaxEvidenceNoteBytes bounds one evidence summary.
const MaxEvidenceNoteBytes = 2 << 10

// AppendEvidence records a pipeline evidence event (red/green run, diff
// guard, judge checks) on a node's journal. Replay ignores it for status.
func (s *Store) AppendEvidence(nodeID, kind, summary string) error {
	if !engine.ValidNodeID(nodeID) {
		return fmt.Errorf("invalid node id")
	}
	kind = strings.TrimSpace(kind)
	if kind == "" || len(kind) > 32 {
		return fmt.Errorf("evidence needs a short kind")
	}
	if len(summary) > MaxEvidenceNoteBytes {
		summary = summary[:MaxEvidenceNoteBytes]
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	g, _, err := s.loadGraphLocked()
	if err != nil {
		return err
	}
	if g.NodeByID(nodeID) == nil {
		return fmt.Errorf("node %q not found", nodeID)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	line, err := engine.AppendJournalLine(engine.HistoryEvent{
		T: now, Event: engine.EventEvidence, Node: nodeID, By: "pipeline",
		Note: kind + ": " + summary, RecordedAt: now,
	})
	if err != nil {
		return err
	}
	return s.appendJournal(line)
}

// ---------- created-by-run ----------

// maxCreatedByEntries bounds the sidecar; the oldest entries go first.
const maxCreatedByEntries = 5000

type createdByFile struct {
	Nodes map[string]createdByEntry `json:"nodes"`
}

type createdByEntry struct {
	Run string `json:"run"`
	At  string `json:"at"`
}

func (s *Store) createdByPath() string {
	return filepath.Join(s.root, DataDir, "created_by_run.json")
}

func (s *Store) loadCreatedByLocked() *createdByFile {
	file := &createdByFile{Nodes: map[string]createdByEntry{}}
	raw, err := ReadProjectFileLimit(s.root, s.createdByPath(), 4<<20)
	if err != nil {
		return file
	}
	if json.Unmarshal(raw, file) != nil || file.Nodes == nil {
		return &createdByFile{Nodes: map[string]createdByEntry{}}
	}
	return file
}

func (s *Store) createdByRunLocked(nodeID string) string {
	return s.loadCreatedByLocked().Nodes[nodeID].Run
}

// recordCreatedByRunLocked notes that runID's agent created ids. Best effort:
// it attributes nodes, it does not gate the write that made them.
func (s *Store) recordCreatedByRunLocked(runID string, ids []string) {
	if runID == "" || len(ids) == 0 {
		return
	}
	file := s.loadCreatedByLocked()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, id := range ids {
		file.Nodes[id] = createdByEntry{Run: runID, At: now}
	}
	for len(file.Nodes) > maxCreatedByEntries {
		oldestID, oldest := "", ""
		for id, entry := range file.Nodes {
			if oldestID == "" || entry.At < oldest {
				oldestID, oldest = id, entry.At
			}
		}
		delete(file.Nodes, oldestID)
	}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return
	}
	if MkdirAllProjectPath(s.root, filepath.Dir(s.createdByPath()), 0o755) != nil {
		return
	}
	_ = s.WriteAtomic(s.createdByPath(), append(data, '\n'))
}

// CreatedByRun is the run whose agent created a node, or "".
func (s *Store) CreatedByRun(nodeID string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.createdByRunLocked(nodeID)
}

// roleDeniedAs reports whether err is a role refusal (for callers that wrap
// other errors).
func roleDeniedAs(err error) bool {
	var denied *ErrRoleDenied
	return errors.As(err, &denied)
}
