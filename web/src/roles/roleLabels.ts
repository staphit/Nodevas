// Role names in the reader's language: built-ins are translated, custom
// roles show the name their project gave them.

import { useMemo } from "react";

import { isBuiltinRole, resolveAgentRoles } from "../domain/graph/roles";
import { useI18n } from "../i18n";
import { useApp } from "../store";
import type { AgentRole, Graph } from "../types";

type Translate = (key: string, values?: Record<string, string | number>) => string;

/** "Reviewer", "審查者", or a custom role's own name. */
export function roleName(role: Pick<AgentRole, "id" | "label" | "builtin">, t: Translate): string {
  return role.builtin ? t(`roles.name.${role.id}`) : role.label || role.id;
}

/** A role id's name in this project, or the id when the project has no such role. */
export function roleNameById(graph: Graph | null | undefined, id: string, t: Translate): string {
  if (isBuiltinRole(id)) return t(`roles.name.${id}`);
  const role = resolveAgentRoles(graph).find((candidate) => candidate.id === id);
  return role ? roleName(role, t) : id;
}

/** The project's roles as select options, and a lookup from id to name. */
export function useRoleOptions() {
  const { t } = useI18n();
  const graph = useApp((s) => s.graph);
  const roles = resolveAgentRoles(graph);
  return useMemo(() => {
    const options = roles.map((role) => ({ value: role.id, label: roleName(role, t) }));
    const byId = new Map(options.map((option) => [option.value, option.label]));
    const roleLabel = (id: string) => byId.get(id);
    return { options, roleLabel };
  }, [roles, t]);
}
