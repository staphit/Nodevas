// Creating many nodes and their wiring in one all-or-nothing write.

package store

import (
	"errors"
	"fmt"
	"log"
	"os"
	"regexp"
	"strings"

	"nodevas/internal/engine"
	"nodevas/internal/identity"
	"nodevas/internal/locale"
)

// Limits on one create_subgraph call. A planner's breakdown is a handful to a
// few dozen nodes; these leave room without letting one call flood a board.
const (
	maxSubgraphNodes          = 100
	maxSubgraphBodyBytes      = 64 << 10
	maxSubgraphTotalBodyBytes = 1 << 20
	maxSubgraphLinks          = 1000
	maxSubgraphEdges          = 200
)

// MaxCopilotDraftNodes caps a call whose batch comes from a Copilot draft
// (via "copilot-draft:..."): a draft is a person's preview, reviewed node by
// node before one Create, so it stays well under the general limit.
const MaxCopilotDraftNodes = 40

// CopilotDraftVia prefixes the batch via of a commit made from a Copilot
// draft: "copilot-draft:<draft id>".
const CopilotDraftVia = "copilot-draft:"

// CopilotPlanVia prefixes the batch via of a commit made from a coding
// agent's plan draft (P6): "copilot-plan:<entry id>". Held to the general cap
// (maxSubgraphNodes, 100), as the plan run was.
const CopilotPlanVia = "copilot-plan:"

// Bounds of a position on the board's grid, as the web client keeps them
// (web/src/components/canvas/useBoardColumns.ts MAX_COL, MAX_ROW).
const (
	MaxPositionCol = 499
	MaxPositionRow = 200
)

var subgraphKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)

// SubgraphNode is one node to create. Key is the caller's name for it within
// the call; DependsOn and Refs name keys of this call or existing node ids.
type SubgraphNode struct {
	Key         string   `json:"key"`
	Title       string   `json:"title"`
	Kind        string   `json:"kind,omitempty"`
	Body        string   `json:"body,omitempty"`
	Priority    string   `json:"priority,omitempty"`
	Assignee    string   `json:"assignee,omitempty"`
	Deadline    string   `json:"deadline,omitempty"`
	WriteAccess string   `json:"writeAccess,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	DependsOn   []string `json:"dependsOn,omitempty"`
	Refs        []string `json:"refs,omitempty"`
	// Acceptance is the node's acceptance criteria, one line each.
	Acceptance []string `json:"acceptance,omitempty"`
}

// SubgraphEdge makes an existing node (To) wait for From, a key or an id.
// New-to-new wiring goes through DependsOn.
type SubgraphEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// SubgraphPosition is a grid cell on the board: Col 0..MaxPositionCol,
// Row 0..MaxPositionRow. It is stored as the node's ui.positions {x: col,
// y: row}.
type SubgraphPosition struct {
	Col int `json:"col"`
	Row int `json:"row"`
}

// SubgraphSpec is one create_subgraph request.
type SubgraphSpec struct {
	Nodes []SubgraphNode `json:"nodes"`
	Edges []SubgraphEdge `json:"edges,omitempty"`
	// Positions places new nodes, keyed by their key in this call. Nodes
	// without one are left to the client's layout, as before.
	Positions map[string]SubgraphPosition `json:"positions,omitempty"`
	// Batch, when set, records the call so UndoBatch can take it back.
	Batch *BatchSpec `json:"batch,omitempty"`
}

// SubgraphCreated is one node the call made.
type SubgraphCreated struct {
	Key   string `json:"key"`
	ID    string `json:"id"`
	Title string `json:"title"`
	Kind  string `json:"kind"`
}

// SubgraphResult maps the caller's keys to the new ids. Issues is the graph's
// validation after the write (warnings and hints; errors were refused).
type SubgraphResult struct {
	IDs     map[string]string `json:"ids"`
	Created []SubgraphCreated `json:"created"`
	Rev     string            `json:"rev"`
	Issues  []engine.Issue    `json:"issues"`
	// Dependencies and References count the wiring the call added.
	Dependencies int `json:"dependencies"`
	References   int `json:"references"`
	// BatchID names the recorded batch and Undoable says it was recorded;
	// both are empty for a call without spec.Batch, and for one whose
	// record could not be written (the nodes were still created).
	BatchID  string `json:"batchId,omitempty"`
	Undoable bool   `json:"undoable,omitempty"`
}

// ErrInvalidSubgraph is a request create_subgraph refuses; Field names the
// offending part, e.g. "nodes[2].dependsOn[0]".
type ErrInvalidSubgraph struct {
	Field, Msg string
	// said is Msg in both languages.
	said error
}

func (e *ErrInvalidSubgraph) Error() string {
	if e.Field == "" {
		return e.Msg
	}
	return e.Field + ": " + e.Msg
}

// Unwrap is the refusal in both languages, for the reader of a response.
func (e *ErrInvalidSubgraph) Unwrap() error { return e.said }

// ErrSubgraphTooLarge is a request over the size limits.
type ErrSubgraphTooLarge struct{ Msg string }

func (e *ErrSubgraphTooLarge) Error() string { return e.Msg }

func invalidSubgraph(field, english, chinese string, args ...any) error {
	said := locale.Errorf(english, chinese, args...)
	return &ErrInvalidSubgraph{Field: field, Msg: said.Error(), said: said}
}

// checkSubgraphShape is everything about a request that needs no graph.
func checkSubgraphShape(spec SubgraphSpec) error {
	if len(spec.Nodes) == 0 {
		return invalidSubgraph("nodes", "at least one node is required", "至少需要一個節點")
	}
	if len(spec.Nodes) > maxSubgraphNodes {
		return &ErrSubgraphTooLarge{Msg: fmt.Sprintf("too many nodes in one call (maximum %d)", maxSubgraphNodes)}
	}
	if len(spec.Edges) > maxSubgraphEdges {
		return &ErrSubgraphTooLarge{Msg: fmt.Sprintf("too many edges in one call (maximum %d)", maxSubgraphEdges)}
	}
	keys := make(map[string]bool, len(spec.Nodes))
	totalBody, links := 0, len(spec.Edges)
	for i, node := range spec.Nodes {
		field := fmt.Sprintf("nodes[%d]", i)
		if !subgraphKeyPattern.MatchString(node.Key) {
			return invalidSubgraph(field+".key", "key %q must be 1-64 letters, digits, '_', '.' or '-'", "key「%s」須為 1 到 64 個字母、數字、'_'、'.' 或 '-'", node.Key)
		}
		if keys[node.Key] {
			return invalidSubgraph(field+".key", "duplicate key %q", "重複的 key「%s」", node.Key)
		}
		keys[node.Key] = true
		if strings.TrimSpace(node.Title) == "" {
			return invalidSubgraph(field+".title", "title is required", "需要標題")
		}
		if len(node.Body) > maxSubgraphBodyBytes {
			return &ErrSubgraphTooLarge{Msg: fmt.Sprintf("%s.body is over %d KiB", field, maxSubgraphBodyBytes>>10)}
		}
		totalBody += len(node.Body)
		links += len(node.DependsOn) + len(node.Refs)
	}
	if totalBody > maxSubgraphTotalBodyBytes {
		return &ErrSubgraphTooLarge{Msg: fmt.Sprintf("bodies total over %d MiB", maxSubgraphTotalBodyBytes>>20)}
	}
	if links > maxSubgraphLinks {
		return &ErrSubgraphTooLarge{Msg: fmt.Sprintf("too many dependencies, references and edges (maximum %d)", maxSubgraphLinks)}
	}
	if len(spec.Positions) > len(spec.Nodes) {
		return invalidSubgraph("positions", "more positions than nodes", "位置比節點還多")
	}
	for key, position := range spec.Positions {
		field := "positions"
		if subgraphKeyPattern.MatchString(key) {
			field += "." + key
		}
		if !keys[key] {
			return invalidSubgraph(field, "%q is not a key of this call", "「%s」不是這次呼叫的 key", key)
		}
		if position.Col < 0 || position.Col > MaxPositionCol || position.Row < 0 || position.Row > MaxPositionRow {
			return invalidSubgraph(field, "position must be a column 0-%d and a row 0-%d", "位置須為第 0-%d 欄、第 0-%d 列", MaxPositionCol, MaxPositionRow)
		}
	}
	if spec.Batch != nil {
		if !batchViaPattern.MatchString(spec.Batch.Via) {
			return invalidSubgraph("batch.via", "via must be 1-128 letters, digits, '_', '.', ':' or '-'", "via 須為 1 到 128 個字母、數字、'_'、'.'、':' 或 '-'")
		}
		if strings.HasPrefix(spec.Batch.Via, CopilotDraftVia) && len(spec.Nodes) > MaxCopilotDraftNodes {
			return &ErrSubgraphTooLarge{Msg: fmt.Sprintf("too many nodes in one Copilot draft (maximum %d)", MaxCopilotDraftNodes)}
		}
	}
	return nil
}

// CreateSubgraph creates every node of spec and its wiring in one graph write,
// or nothing. Each node is checked as CreateNode checks one, and the wiring
// goes through the same graph ops link_nodes uses, so every permission and
// logic-gate rule applies; the whole result is then validated once, and any
// new error (a cycle, an unknown id) refuses the call.
//
// With spec.Batch set, the call is also recorded as a batch that UndoBatch
// can take back. A record that cannot be written does not undo the commit:
// the result then says Undoable false.
func (s *Store) CreateSubgraph(actor identity.Actor, spec SubgraphSpec) (SubgraphResult, error) {
	if err := checkSubgraphShape(spec); err != nil {
		return SubgraphResult{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	plan, err := s.prepareSubgraphLocked(actor, spec)
	if err != nil {
		return SubgraphResult{}, err
	}
	return s.commitSubgraphLocked(actor, spec, plan)
}

// preparedSubgraph is a checked call, ready to be written.
type preparedSubgraph struct {
	g        *engine.Graph
	graphRev string
	nodes    []*engine.Node
	files    []*engine.NodeFile
	touched  map[string]bool
	// links are the dependencies the call put into nodes that already
	// existed (spec.Edges): a batch undo removes them with the new nodes,
	// and a redo has to put them back.
	links  []BatchLink
	result SubgraphResult
}

// prepareSubgraphLocked makes every check of a create_subgraph call and builds
// the graph it would write, on a private copy. It writes nothing. The caller
// holds s.mu, for reading (a dry run) or writing (a commit).
func (s *Store) prepareSubgraphLocked(actor identity.Actor, spec SubgraphSpec) (*preparedSubgraph, error) {
	g, graphRev, err := s.loadGraphLocked()
	if err != nil {
		return nil, err
	}
	existing := make(map[string]bool, len(g.Nodes))
	for _, node := range g.Nodes {
		if node != nil {
			existing[node.ID] = true
		}
	}

	// The nodes, as CreateNode would make each one.
	files := make([]*engine.NodeFile, len(spec.Nodes))
	nodes := make([]*engine.Node, len(spec.Nodes))
	access := make([]string, len(spec.Nodes))
	for i, in := range spec.Nodes {
		field := fmt.Sprintf("nodes[%d]", i)
		if existing[in.Key] {
			return nil, invalidSubgraph(field+".key", "key %q is also an existing node id", "key「%s」也是既有的節點代號", in.Key)
		}
		nf, err := engine.ParseNodeFile([]byte(in.Body))
		if err != nil {
			return nil, invalidSubgraph(field+".body", "%v", "%v", err)
		}
		n := &engine.Node{
			Title: strings.TrimSpace(in.Title), Kind: in.Kind, Priority: in.Priority,
			Assignee: in.Assignee, Deadline: in.Deadline, WriteAccess: in.WriteAccess,
			Tags: append([]string(nil), in.Tags...),
		}
		hydrateNodeFromMeta(n, nf.Meta)
		n.Kind = engine.NormalizeNodeKind(n.Kind)
		if n.Kind == "" {
			n.Kind = engine.NodeKindTask
		}
		if !g.KnownNodeKind(n.Kind) {
			err := unknownNodeTypeError("", n.Kind).(*ErrNodeTypeDenied)
			err.Msg = field + ".kind: " + err.Error()
			return nil, err
		}
		if err := checkRole(actor, g, engine.CapCreate, n); err != nil {
			return nil, err
		}
		if len(in.DependsOn) > 0 || len(in.Refs) > 0 {
			if err := checkRole(actor, g, engine.CapLink, nil); err != nil {
				return nil, err
			}
		}
		if len(in.Acceptance) > 0 {
			if len(in.Acceptance) > engine.MaxAcceptanceItems {
				return nil, invalidSubgraph(field+".acceptance", "at most %d criteria", "最多 %d 條條件", engine.MaxAcceptanceItems)
			}
			acceptance := &engine.Acceptance{By: acceptanceAttribution(actor)}
			for _, text := range in.Acceptance {
				acceptance.Items = append(acceptance.Items, engine.AcceptanceItem{Text: strings.TrimSpace(text)})
			}
			acceptance.Items = engine.AssignAcceptanceIDs(acceptance.Items)
			if err := checkAcceptanceWrite(actor, g, n, nil, acceptance, true); err != nil {
				return nil, err
			}
			n.Acceptance = acceptance
		}
		access[i] = normalizeWriteAccess(n.WriteAccess)
		files[i], nodes[i] = nf, n
	}
	ids, err := s.nextNodeIDsLocked(g, len(nodes))
	if err != nil {
		return nil, err
	}
	keyIDs := make(map[string]string, len(nodes))
	plan := &preparedSubgraph{g: g, graphRev: graphRev, nodes: nodes, files: files, touched: map[string]bool{}}
	result := &plan.result
	result.IDs = keyIDs
	for i, n := range nodes {
		n.ID = ids[i]
		keyIDs[spec.Nodes[i].Key] = n.ID
		result.Created = append(result.Created, SubgraphCreated{
			Key: spec.Nodes[i].Key, ID: n.ID, Title: n.Title, Kind: n.Kind})
		// Nothing is written over an orphaned document of the same name.
		if _, err := s.statPath(s.NodePath(n.ID)); err == nil {
			return nil, locale.Errorf("node file %q already exists", "節點檔案「%s」已存在", n.ID)
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	resolve := func(ref, field string) (string, error) {
		if id, ok := keyIDs[ref]; ok {
			return id, nil
		}
		if existing[ref] {
			return ref, nil
		}
		return "", invalidSubgraph(field, "%q is neither a key of this call nor an existing node id", "「%s」既不是這次呼叫的 key，也不是既有的節點代號", ref)
	}

	// Errors only the result can show, measured against the board as it is,
	// so a problem the board already had does not refuse this call.
	before := map[engine.Issue]bool{}
	for _, issue := range engine.Validate(g) {
		before[issue] = true
	}

	// New nodes join open to everyone, so wiring them needs no write access;
	// their final access is restored before validation.
	for _, n := range nodes {
		n.WriteAccess = ""
		g.Nodes = append(g.Nodes, n)
	}
	touched := plan.touched
	apply := func(op GraphOp, field string) error {
		if err := applyGraphOp(actor, g, op, touched, func(string) bool { return false }); err != nil {
			var writeDenied *ErrNodeWriteDenied
			var typeDenied *ErrNodeTypeDenied
			if errors.As(err, &writeDenied) || errors.As(err, &typeDenied) || roleDeniedAs(err) {
				return err
			}
			return invalidSubgraph(field, "%v", "%v", err)
		}
		return nil
	}
	for i, in := range spec.Nodes {
		for j, dep := range in.DependsOn {
			field := fmt.Sprintf("nodes[%d].dependsOn[%d]", i, j)
			from, err := resolve(dep, field)
			if err != nil {
				return nil, err
			}
			if err := apply(GraphOp{Kind: "add-edge", From: from, To: nodes[i].ID}, field); err != nil {
				return nil, err
			}
			result.Dependencies++
		}
		for j, ref := range in.Refs {
			field := fmt.Sprintf("nodes[%d].refs[%d]", i, j)
			target, err := resolve(ref, field)
			if err != nil {
				return nil, err
			}
			if err := apply(GraphOp{Kind: "add-ref", NodeID: nodes[i].ID, Ref: target}, field); err != nil {
				return nil, err
			}
			result.References++
		}
	}
	for i, edge := range spec.Edges {
		field := fmt.Sprintf("edges[%d]", i)
		from, err := resolve(edge.From, field+".from")
		if err != nil {
			return nil, err
		}
		if !existing[edge.To] {
			return nil, invalidSubgraph(field+".to",
				"%q is not an existing node id; wire new nodes to each other with dependsOn", "「%s」不是既有的節點代號；新節點之間請用 dependsOn 連接", edge.To)
		}
		if err := apply(GraphOp{Kind: "add-edge", From: from, To: edge.To}, field); err != nil {
			return nil, err
		}
		plan.links = append(plan.links, BatchLink{From: from, To: edge.To})
		result.Dependencies++
	}

	// Where the caller placed the new nodes (grid cells; checkSubgraphShape
	// bounded them). Without positions the layout is left alone.
	if len(spec.Positions) > 0 {
		if g.UI == nil {
			g.UI = &engine.UIState{}
		}
		if g.UI.Positions == nil {
			g.UI.Positions = map[string]engine.Position{}
		}
		for key, position := range spec.Positions {
			g.UI.Positions[keyIDs[key]] = engine.Position{X: float64(position.Col), Y: float64(position.Row)}
		}
	}

	userIDs := make(map[string]struct{}, len(g.Users))
	for _, user := range g.Users {
		userIDs[user.ID] = struct{}{}
	}
	for i, n := range nodes {
		n.WriteAccess = access[i]
		if err := validateNodeForStorage(n, userIDs); err != nil {
			return nil, invalidSubgraph(fmt.Sprintf("nodes[%d] (key %s)", i, spec.Nodes[i].Key), "%v", "%v", err)
		}
	}
	if err := ValidateGraphForStorage(g); err != nil {
		return nil, invalidSubgraph("", "%v", "%v", err)
	}
	after := engine.Validate(g)
	for _, issue := range after {
		if issue.Severity == "error" && !before[issue] {
			return nil, invalidSubgraph("", "%s", "%s", subgraphIssueText(issue))
		}
	}
	result.Rev = graphRev
	result.Issues = after
	return plan, nil
}

// commitSubgraphLocked writes a prepared call: the new node files, the
// existing nodes whose requires changed, then the graph. The caller holds
// s.mu exclusively and prepared plan under that same hold.
func (s *Store) commitSubgraphLocked(actor identity.Actor, spec SubgraphSpec, plan *preparedSubgraph) (SubgraphResult, error) {
	var zero SubgraphResult
	g, nodes := plan.g, plan.nodes
	data, err := s.marshalGraph(g)
	if err != nil {
		return zero, err
	}
	created := make(map[string]bool, len(nodes))
	createdIDs := make([]string, 0, len(nodes))
	nodePaths := make([]string, 0, len(nodes))
	updates := make([]fileUpdate, 0, len(nodes)+len(plan.touched)+1)
	for i, n := range nodes {
		created[n.ID] = true
		createdIDs = append(createdIDs, n.ID)
		engine.SyncFrontmatter(plan.files[i], n)
		out, err := engine.ComposeNodeFile(plan.files[i])
		if err != nil {
			return zero, err
		}
		nodePaths = append(nodePaths, s.NodePath(n.ID))
		updates = append(updates, fileUpdate{path: s.NodePath(n.ID), data: out, checkRevision: true, expectedRev: ""})
	}
	// Existing nodes whose requires gained an operand keep their frontmatter
	// snapshot in step, as ApplyGraphOps does.
	for _, n := range g.Nodes {
		if n == nil || created[n.ID] || !plan.touched[n.ID] {
			continue
		}
		update, err := s.prepareNodeFileUpdate(n)
		if err != nil {
			return zero, locale.Errorf("sync node %s: %w", "同步節點 %s：%w", n.ID, err)
		}
		if update != nil {
			updates = append(updates, *update)
		}
	}
	updates = append(updates, fileUpdate{path: s.GraphPath(), data: data, checkRevision: true, expectedRev: plan.graphRev})
	if err := s.applyUpdatesLocked(actor, updates); err != nil {
		return zero, err
	}
	if actor.RunID != "" {
		s.recordCreatedByRunLocked(actor.RunID, createdIDs)
	}
	result := plan.result
	result.Rev = Rev(data)
	if spec.Batch != nil {
		if note := batchHistoryNote(spec.Batch.Via); note != "" {
			s.history().wrote(actor, note, nodePaths)
		}
		id, err := s.recordBatchLocked(actor, spec.Batch.Via, createdIDs, plan.links)
		if err != nil {
			log.Printf("subgraph committed, batch not recorded (not undoable): %v", err)
		} else {
			result.BatchID, result.Undoable = id, true
		}
	}
	return result, nil
}

// subgraphIssueText names the node an issue is about, which a bare validation
// message ("unknown node") does not.
func subgraphIssueText(issue engine.Issue) string {
	if issue.NodeID == "" {
		return issue.Msg
	}
	return fmt.Sprintf("node %s: %s", issue.NodeID, issue.Msg)
}
