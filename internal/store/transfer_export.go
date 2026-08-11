// Snapshotting a node selection out of its source project.

package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"nodevas/internal/engine"
	"nodevas/internal/engine/dsl"
)

// ExportNodes snapshots a node selection so it can be recreated elsewhere.
//
// forMove additionally refuses selections that cannot afterwards leave the
// source graph, so a move fails before anything is written rather than after.
func (s *Store) ExportNodes(ids []string, forMove bool) (*nodeTransferPayload, error) {
	unique, err := uniqueNodeIDs(ids)
	if err != nil {
		return nil, err
	}
	selection := make(map[string]bool, len(unique))
	for _, id := range unique {
		selection[id] = true
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	g, _, err := s.loadGraphLocked()
	if err != nil {
		return nil, err
	}

	payload := &nodeTransferPayload{
		selection:     selection,
		sourceRoot:    s.root,
		documents:     map[string][]byte{},
		pages:         map[string]nodePagesManifest{},
		pageFiles:     map[string]map[string][]byte{},
		attachments:   map[string]map[string][]byte{},
		positions:     map[string]engine.Position{},
		styles:        map[string]engine.NodeStyle{},
		entryOverride: map[string]bool{},
		plans:         map[string][]engine.PlanMilestone{},
		gates:         map[string]engine.GatePlacement{},
		wireVertices:  map[string][]engine.Position{},
		edgeLabels:    map[string]engine.EdgeLabelPlacement{},
		flags:         map[string]any{},
	}

	// Graph order, not request order: the copy should read the way the
	// original does.
	found := map[string]bool{}
	kept := make([]*engine.Node, 0, len(g.Nodes))
	for _, node := range g.Nodes {
		if node == nil {
			continue
		}
		if !selection[node.ID] {
			kept = append(kept, node)
			continue
		}
		found[node.ID] = true
		copied := *node
		copied.Tags = append([]string(nil), node.Tags...)
		copied.Effects = append([]engine.Effect(nil), node.Effects...)
		payload.nodes = append(payload.nodes, &copied)
	}
	for _, id := range unique {
		if !found[id] {
			return nil, fmt.Errorf("node %q not found", id)
		}
	}
	if forMove {
		if err := checkNodesRemovable(g, kept, selection); err != nil {
			return nil, err
		}
	}

	for _, edge := range g.Edges {
		if edge != nil && selection[edge.From] && selection[edge.To] {
			payload.edges = append(payload.edges, *edge)
		}
	}

	// Project-level definitions the selection depends on. Without these the
	// target graph would fail validation or the copy would lose meaning.
	usersByID := map[string]engine.User{}
	for _, user := range g.Users {
		usersByID[user.ID] = user
	}
	seenUser := map[string]bool{}
	usedFlags := map[string]bool{}
	for _, node := range payload.nodes {
		if node.Assignee != "" && !seenUser[node.Assignee] {
			if user, ok := usersByID[node.Assignee]; ok {
				seenUser[node.Assignee] = true
				payload.users = append(payload.users, user)
			}
		}
		for _, effect := range node.Effects {
			if name := effectFlagName(effect.Set); name != "" {
				usedFlags[name] = true
			}
		}
		if expr, parseErr := node.RequiresExpr(); parseErr == nil && expr != nil {
			for _, flag := range dsl.FlagRefs(expr) {
				usedFlags[flag] = true
			}
		}
	}
	for name := range usedFlags {
		if value, ok := g.Flags[name]; ok {
			payload.flags[name] = value
		}
	}

	if g.UI != nil {
		for id := range selection {
			if position, ok := g.UI.Positions[id]; ok {
				payload.positions[id] = position
			}
			if style, ok := g.UI.NodeStyles[id]; ok {
				payload.styles[id] = style
			}
			if override, ok := g.UI.EntryOverrides[id]; ok {
				payload.entryOverride[id] = override
			}
			if plans, ok := g.UI.Plans[id]; ok {
				payload.plans[id] = append([]engine.PlanMilestone(nil), plans...)
			}
			if gate, ok := g.UI.Gates[id]; ok {
				payload.gates[id] = gate
			}
		}
		for _, nodeID := range g.UI.TimelineOrder {
			if selection[nodeID] {
				payload.timelineOrder = append(payload.timelineOrder, nodeID)
			}
		}
		// A logic gate only travels whole: a gate missing an input is a
		// different gate, and one missing its output is not wired to anything.
		for _, gate := range g.UI.LogicGates {
			complete := len(gate.Inputs) > 0
			for _, output := range gate.OutputNodes() {
				if !selection[output] {
					complete = false
					break
				}
			}
			for _, input := range gate.Inputs {
				if !selection[input] {
					complete = false
					break
				}
			}
			if !complete {
				continue
			}
			copied := gate
			copied.Inputs = append([]string(nil), gate.Inputs...)
			if len(gate.Outputs) > 0 {
				copied.Outputs = append([]string(nil), gate.Outputs...)
			}
			payload.logicGates = append(payload.logicGates, copied)
		}
		for key, vertices := range g.UI.WireVertices {
			if transferWireKeyInSelection(key, selection) {
				payload.wireVertices[key] = append([]engine.Position(nil), vertices...)
			}
		}
		for key, label := range g.UI.EdgeLabels {
			from, to, ok := strings.Cut(key, "->")
			if ok && selection[from] && selection[to] {
				payload.edgeLabels[key] = label
			}
		}
		// Plan milestones and lifecycle stamps name statuses that only exist
		// because this project defines them.
		usedPlanStatus := map[engine.Status]bool{}
		for _, plans := range payload.plans {
			for _, plan := range plans {
				usedPlanStatus[plan.Status] = true
			}
		}
		for _, definition := range g.UI.PlanStatuses {
			if usedPlanStatus[engine.Status(definition.ID)] {
				payload.planStatuses = append(payload.planStatuses, definition)
			}
		}
	}

	journal, err := s.readJournalForNodesLocked(selection)
	if err != nil {
		return nil, err
	}
	payload.journal = journal
	if g.UI != nil {
		usedStatus := map[engine.Status]bool{}
		for _, event := range journal {
			usedStatus[event.To] = true
			usedStatus[event.From] = true
		}
		for _, definition := range g.UI.CustomStatuses {
			if usedStatus[engine.Status(definition.ID)] {
				payload.customStatuses = append(payload.customStatuses, definition)
			}
		}
	}

	for _, id := range unique {
		document, err := s.ReadFile(s.NodePath(id))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		if err == nil {
			payload.documents[id] = document
		}
		manifest, err := s.LoadNodePagesManifest(id)
		if err != nil {
			return nil, err
		}
		if len(manifest.Pages) > 0 {
			payload.pages[id] = manifest
			files := map[string][]byte{}
			for _, page := range manifest.Pages {
				path := s.NodePagePath(id, page.ID, page.Format)
				data, err := s.ReadFile(path)
				if errors.Is(err, os.ErrNotExist) {
					continue
				}
				if err != nil {
					return nil, err
				}
				files[filepath.Base(path)] = data
			}
			payload.pageFiles[id] = files
		}
		attachments, err := s.readDirFiles(s.NodeFilesDir(id))
		if err != nil {
			return nil, err
		}
		if len(attachments) > 0 {
			payload.attachments[id] = attachments
		}
	}
	return payload, nil
}

// transferWireKeyInSelection reports whether a wire-vertex key belongs
// entirely to the selection. Keys are "from->to" or "gate:<target node>".
func transferWireKeyInSelection(key string, selection map[string]bool) bool {
	if target, ok := strings.CutPrefix(key, "gate:"); ok {
		return selection[target]
	}
	from, to, ok := strings.Cut(key, "->")
	return ok && selection[from] && selection[to]
}

// readJournalForNodesLocked returns the raw lifecycle events belonging to a
// node selection, in file order. It reads the journal rather than a replayed
// RunState because replay expands transitions into synthetic events, and only
// what was actually recorded should travel.
func (s *Store) readJournalForNodesLocked(selection map[string]bool) ([]engine.HistoryEvent, error) {
	var events []engine.HistoryEvent
	statusIDs := map[string]bool{}
	// Read the rotated segment before the live one so events stay in the order
	// they were recorded. After a compaction the live journal only holds the
	// tail, so reading it alone would silently drop everything a transfer is
	// supposed to carry.
	for _, path := range []string{s.RotatedJournalPath(), s.JournalPath()} {
		data, err := s.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, line := range strings.Split(string(data), "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			var event engine.HistoryEvent
			if err := json.Unmarshal([]byte(line), &event); err != nil {
				continue // a torn or foreign line is not this transfer's problem
			}
			switch {
			case event.Event == "status" && selection[event.Node]:
				if event.ID != "" {
					statusIDs[event.ID] = true
				}
				events = append(events, event)
			case event.Event == "move" && event.Ref != "" && statusIDs[event.Ref]:
				events = append(events, event)
			}
		}
	}
	return events, nil
}
