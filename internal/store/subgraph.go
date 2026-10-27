// Creating many nodes and their wiring in one all-or-nothing write.

package store

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

	"nodevas/internal/engine"
	"nodevas/internal/identity"
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
	// RED PHASE STUB: not stored yet.
	Acceptance []string `json:"acceptance,omitempty"`
}

// SubgraphEdge makes an existing node (To) wait for From, a key or an id.
// New-to-new wiring goes through DependsOn.
type SubgraphEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// SubgraphSpec is one create_subgraph request.
type SubgraphSpec struct {
	Nodes []SubgraphNode `json:"nodes"`
	Edges []SubgraphEdge `json:"edges,omitempty"`
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
}

// ErrInvalidSubgraph is a request create_subgraph refuses; Field names the
// offending part, e.g. "nodes[2].dependsOn[0]".
type ErrInvalidSubgraph struct {
	Field, Msg string
}

func (e *ErrInvalidSubgraph) Error() string {
	if e.Field == "" {
		return e.Msg
	}
	return e.Field + ": " + e.Msg
}

// ErrSubgraphTooLarge is a request over the size limits.
type ErrSubgraphTooLarge struct{ Msg string }

func (e *ErrSubgraphTooLarge) Error() string { return e.Msg }

func invalidSubgraph(field, format string, args ...any) error {
	return &ErrInvalidSubgraph{Field: field, Msg: fmt.Sprintf(format, args...)}
}

// checkSubgraphShape is everything about a request that needs no graph.
func checkSubgraphShape(spec SubgraphSpec) error {
	if len(spec.Nodes) == 0 {
		return invalidSubgraph("nodes", "at least one node is required")
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
			return invalidSubgraph(field+".key", "key %q must be 1-64 letters, digits, '_', '.' or '-'", node.Key)
		}
		if keys[node.Key] {
			return invalidSubgraph(field+".key", "duplicate key %q", node.Key)
		}
		keys[node.Key] = true
		if strings.TrimSpace(node.Title) == "" {
			return invalidSubgraph(field+".title", "title is required")
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
	return nil
}

// CreateSubgraph creates every node of spec and its wiring in one graph write,
// or nothing. Each node is checked as CreateNode checks one, and the wiring
// goes through the same graph ops link_nodes uses, so every permission and
// logic-gate rule applies; the whole result is then validated once, and any
// new error (a cycle, an unknown id) refuses the call.
func (s *Store) CreateSubgraph(actor identity.Actor, spec SubgraphSpec) (SubgraphResult, error) {
	var zero SubgraphResult
	if err := checkSubgraphShape(spec); err != nil {
		return zero, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	g, graphRev, err := s.loadGraphLocked()
	if err != nil {
		return zero, err
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
			return zero, invalidSubgraph(field+".key", "key %q is also an existing node id", in.Key)
		}
		nf, err := engine.ParseNodeFile([]byte(in.Body))
		if err != nil {
			return zero, invalidSubgraph(field+".body", "%v", err)
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
			err.Msg = field + ".kind: " + err.Msg
			return zero, err
		}
		access[i] = normalizeWriteAccess(n.WriteAccess)
		files[i], nodes[i] = nf, n
	}
	ids, err := s.nextNodeIDsLocked(g, len(nodes))
	if err != nil {
		return zero, err
	}
	keyIDs := make(map[string]string, len(nodes))
	result := SubgraphResult{IDs: keyIDs}
	for i, n := range nodes {
		n.ID = ids[i]
		keyIDs[spec.Nodes[i].Key] = n.ID
		result.Created = append(result.Created, SubgraphCreated{
			Key: spec.Nodes[i].Key, ID: n.ID, Title: n.Title, Kind: n.Kind})
	}
	resolve := func(ref, field string) (string, error) {
		if id, ok := keyIDs[ref]; ok {
			return id, nil
		}
		if existing[ref] {
			return ref, nil
		}
		return "", invalidSubgraph(field, "%q is neither a key of this call nor an existing node id", ref)
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
	touched := map[string]bool{}
	apply := func(op GraphOp, field string) error {
		if err := applyGraphOp(actor, g, op, touched); err != nil {
			var writeDenied *ErrNodeWriteDenied
			var typeDenied *ErrNodeTypeDenied
			if errors.As(err, &writeDenied) || errors.As(err, &typeDenied) {
				return err
			}
			return invalidSubgraph(field, "%v", err)
		}
		return nil
	}
	for i, in := range spec.Nodes {
		for j, dep := range in.DependsOn {
			field := fmt.Sprintf("nodes[%d].dependsOn[%d]", i, j)
			from, err := resolve(dep, field)
			if err != nil {
				return zero, err
			}
			if err := apply(GraphOp{Kind: "add-edge", From: from, To: nodes[i].ID}, field); err != nil {
				return zero, err
			}
			result.Dependencies++
		}
		for j, ref := range in.Refs {
			field := fmt.Sprintf("nodes[%d].refs[%d]", i, j)
			target, err := resolve(ref, field)
			if err != nil {
				return zero, err
			}
			if err := apply(GraphOp{Kind: "add-ref", NodeID: nodes[i].ID, Ref: target}, field); err != nil {
				return zero, err
			}
			result.References++
		}
	}
	for i, edge := range spec.Edges {
		field := fmt.Sprintf("edges[%d]", i)
		from, err := resolve(edge.From, field+".from")
		if err != nil {
			return zero, err
		}
		if !existing[edge.To] {
			return zero, invalidSubgraph(field+".to",
				"%q is not an existing node id; wire new nodes to each other with dependsOn", edge.To)
		}
		if err := apply(GraphOp{Kind: "add-edge", From: from, To: edge.To}, field); err != nil {
			return zero, err
		}
		result.Dependencies++
	}

	userIDs := make(map[string]struct{}, len(g.Users))
	for _, user := range g.Users {
		userIDs[user.ID] = struct{}{}
	}
	for i, n := range nodes {
		n.WriteAccess = access[i]
		if err := validateNodeForStorage(n, userIDs); err != nil {
			return zero, invalidSubgraph(fmt.Sprintf("nodes[%d] (key %s)", i, spec.Nodes[i].Key), "%v", err)
		}
	}
	if err := ValidateGraphForStorage(g); err != nil {
		return zero, invalidSubgraph("", "%v", err)
	}
	after := engine.Validate(g)
	for _, issue := range after {
		if issue.Severity == "error" && !before[issue] {
			return zero, invalidSubgraph("", "%s", subgraphIssueText(issue))
		}
	}

	data, err := s.marshalGraph(g)
	if err != nil {
		return zero, err
	}
	created := make(map[string]bool, len(nodes))
	updates := make([]fileUpdate, 0, len(nodes)+len(touched)+1)
	for i, n := range nodes {
		created[n.ID] = true
		if _, err := s.statPath(s.NodePath(n.ID)); err == nil {
			return zero, fmt.Errorf("node file %q already exists", n.ID)
		} else if !errors.Is(err, os.ErrNotExist) {
			return zero, err
		}
		engine.SyncFrontmatter(files[i], n)
		out, err := engine.ComposeNodeFile(files[i])
		if err != nil {
			return zero, err
		}
		updates = append(updates, fileUpdate{path: s.NodePath(n.ID), data: out, checkRevision: true, expectedRev: ""})
	}
	// Existing nodes whose requires gained an operand keep their frontmatter
	// snapshot in step, as ApplyGraphOps does.
	for _, n := range g.Nodes {
		if n == nil || created[n.ID] || !touched[n.ID] {
			continue
		}
		update, err := s.prepareNodeFileUpdate(n)
		if err != nil {
			return zero, fmt.Errorf("sync node %s: %w", n.ID, err)
		}
		if update != nil {
			updates = append(updates, *update)
		}
	}
	updates = append(updates, fileUpdate{path: s.GraphPath(), data: data, checkRevision: true, expectedRev: graphRev})
	if err := s.applyUpdatesLocked(actor, updates); err != nil {
		return zero, err
	}
	result.Rev = Rev(data)
	result.Issues = after
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
