// Merging the project-level definitions (users, plan statuses, custom
// statuses, logic gates) a transferred selection depends on into the target
// project.

package store

import (
	"fmt"
	"strings"

	"nodevas/internal/engine"
)

// mergeUsers makes the selection's assignees exist in this project and returns
// the source-id -> target-id mapping. An id that is unmapped means the
// assignee could not be carried and must be cleared.
func mergeUsers(g *engine.Graph, users []engine.User, warn func(string, ...any)) map[string]string {
	mapping := make(map[string]string, len(users))
	byID := make(map[string]engine.User, len(g.Users))
	byName := make(map[string]engine.User, len(g.Users))
	for _, user := range g.Users {
		byID[user.ID] = user
		byName[strings.ToLower(strings.TrimSpace(user.Name))] = user
	}
	for _, user := range users {
		name := strings.ToLower(strings.TrimSpace(user.Name))
		if existing, ok := byID[user.ID]; ok && strings.EqualFold(existing.Name, user.Name) {
			mapping[user.ID] = existing.ID
			continue
		}
		// Same person, different id: the display name is what the user
		// recognizes, so it decides the match.
		if existing, ok := byName[name]; ok {
			mapping[user.ID] = existing.ID
			continue
		}
		if _, taken := byID[user.ID]; taken {
			warn("成員「%s」的識別碼在目標專案已被他人使用，相關指派已清除", user.Name)
			continue
		}
		g.Users = append(g.Users, user)
		byID[user.ID] = user
		byName[name] = user
		mapping[user.ID] = user.ID
	}
	return mapping
}

// mergePlanStatuses adds the custom expected-milestone types the plans need,
// returning any id remapping caused by a label that already exists here.
func mergePlanStatuses(
	g *engine.Graph,
	statuses []engine.PlanStatusDefinition,
	warn func(string, ...any),
) map[engine.Status]engine.Status {
	mapping := map[engine.Status]engine.Status{}
	byID := map[string]engine.PlanStatusDefinition{}
	byLabel := map[string]engine.PlanStatusDefinition{}
	for _, definition := range g.UI.PlanStatuses {
		byID[definition.ID] = definition
		byLabel[strings.ToLower(strings.TrimSpace(definition.Label))] = definition
	}
	for _, definition := range statuses {
		label := strings.ToLower(strings.TrimSpace(definition.Label))
		if existing, ok := byID[definition.ID]; ok {
			if !strings.EqualFold(existing.Label, definition.Label) {
				warn("預計狀態「%s」在目標專案已存在但名稱不同（%s），沿用目標專案的定義",
					definition.Label, existing.Label)
			}
			continue
		}
		if existing, ok := byLabel[label]; ok {
			mapping[engine.Status(definition.ID)] = engine.Status(existing.ID)
			continue
		}
		g.UI.PlanStatuses = append(g.UI.PlanStatuses, definition)
		byID[definition.ID] = definition
		byLabel[label] = definition
	}
	return mapping
}

// mergeCustomStatuses adds the project-defined lifecycle states the moved
// history refers to, so the stamps still render with their own label.
func mergeCustomStatuses(g *engine.Graph, statuses []engine.StatusDefinition) {
	existing := map[string]bool{}
	for _, definition := range g.UI.CustomStatuses {
		existing[definition.ID] = true
	}
	for _, definition := range statuses {
		if existing[definition.ID] {
			continue
		}
		g.UI.CustomStatuses = append(g.UI.CustomStatuses, definition)
		existing[definition.ID] = true
	}
}

// importLogicGates recreates the gates that travelled whole, under gate ids
// that are free in this project.
func importLogicGates(
	g *engine.Graph,
	gates []engine.LogicGate,
	selection map[string]bool,
	rename func(string) string,
	warn func(string, ...any),
) {
	if len(gates) == 0 {
		return
	}
	used := make(map[string]bool, len(g.UI.LogicGates))
	outputs := make(map[string]bool, len(g.UI.LogicGates))
	for _, gate := range g.UI.LogicGates {
		used[gate.ID] = true
		for _, output := range gate.OutputNodes() {
			outputs[output] = true
		}
	}
	for _, gate := range gates {
		copied := gate
		copied.ID = freeLogicGateID(gate.ID, used)
		used[copied.ID] = true
		copied.Inputs = make([]string, 0, len(gate.Inputs))
		for _, input := range gate.Inputs {
			copied.Inputs = append(copied.Inputs, rename(input))
		}
		if original := gate.OutputNodes(); len(original) > 0 {
			kept := make([]string, 0, len(original))
			for _, output := range original {
				renamed := rename(output)
				if outputs[renamed] {
					continue
				}
				outputs[renamed] = true
				kept = append(kept, renamed)
			}
			if len(kept) == 0 {
				warn("邏輯閘「%s」的輸出節點在目標專案已由其他邏輯閘驅動，未帶入", gate.ID)
				continue
			}
			if len(kept) < len(original) {
				warn("邏輯閘「%s」的部分輸出節點已由其他邏輯閘驅動，未帶入", gate.ID)
			}
			if len(gate.Outputs) > 0 {
				copied.Output = ""
				copied.Outputs = kept
			} else {
				copied.Output = kept[0]
			}
		}
		if applied, ok := rewriteRequires(gate.Applied, selection, rename); ok {
			copied.Applied = applied
		} else {
			copied.Applied = ""
		}
		g.UI.LogicGates = append(g.UI.LogicGates, copied)
	}
}

func freeLogicGateID(preferred string, used map[string]bool) string {
	if preferred == "" || !engine.ValidNodeID(preferred) {
		preferred = "gate"
	}
	if !used[preferred] {
		return preferred
	}
	for n := 2; ; n++ {
		candidate := fmt.Sprintf("%s-%d", preferred, n)
		if !used[candidate] {
			return candidate
		}
	}
}
