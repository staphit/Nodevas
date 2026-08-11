/**
 * Explorer ordering [B-03].
 *
 * The server returns projects in path order, which is a depth-first walk of
 * the tree. Any other ordering has to keep that shape — a child must still
 * follow its parent — so siblings are sorted inside each group and the tree is
 * flattened again, rather than sorting the flat list.
 */

import type { ProjectEntry } from "../../state/types";

export type ProjectSort = "name" | "name-desc" | "nodes" | "kind" | "manual";

export const PROJECT_SORT_LABELS: Record<ProjectSort, string> = {
  name: "名稱 A→Z",
  "name-desc": "名稱 Z→A",
  nodes: "節點數（多→少）",
  kind: "資料夾優先",
  manual: "手動排序",
};

function compare(
  sort: ProjectSort,
  rank: Map<string, number>,
  left: ProjectEntry,
  right: ProjectEntry,
): number {
  const byName = left.label.localeCompare(right.label, "zh-Hant", {
    numeric: true,
    sensitivity: "base",
  });
  switch (sort) {
    case "name":
      return byName;
    case "name-desc":
      return -byName;
    case "nodes":
      return (right.nodes ?? 0) - (left.nodes ?? 0) || byName;
    case "kind":
      return (
        Number(Boolean(right.isFolder)) - Number(Boolean(left.isFolder)) || byName
      );
    case "manual": {
      // The stored order is advisory — it can name projects that were deleted
      // and miss ones created since — so a project it does not mention sinks
      // below the ones it does, and ties there fall back to the name.
      const leftRank = rank.get(left.name);
      const rightRank = rank.get(right.name);
      if (leftRank === undefined && rightRank === undefined) return byName;
      if (leftRank === undefined) return 1;
      if (rightRank === undefined) return -1;
      return leftRank - rightRank;
    }
  }
}

/**
 * `order` is the workspace's stored manual order and is only read by the
 * `manual` sort; every other sort ignores it.
 */
export function sortProjectTree(
  projects: ProjectEntry[],
  sort: ProjectSort,
  order: string[] = [],
): ProjectEntry[] {
  const rank = new Map(order.map((name, index) => [name, index]));
  const children = new Map<string, ProjectEntry[]>();
  for (const project of projects) {
    const parent = project.parent ?? "";
    children.set(parent, [...(children.get(parent) ?? []), project]);
  }
  const known = new Set(projects.map((project) => project.name));
  const out: ProjectEntry[] = [];
  const walk = (parent: string) => {
    const group = [...(children.get(parent) ?? [])].sort((left, right) =>
      compare(sort, rank, left, right),
    );
    for (const project of group) {
      out.push(project);
      walk(project.name);
    }
  };
  walk("");
  // A project whose parent is missing from the list would otherwise vanish;
  // keep it at the end rather than dropping it from the tree.
  for (const project of projects) {
    const parent = project.parent ?? "";
    if (parent && !known.has(parent) && !out.includes(project)) out.push(project);
  }
  return out;
}

/**
 * The manual order to store after dragging `moved` next to `target`.
 *
 * `ordered` must already be flattened for display, so the result keeps every
 * level exactly as the user last saw it and only the one dragged level changes.
 * Reordering is confined to siblings, so a caller that passes two projects with
 * different parents gets the current order back unchanged.
 */
export function reorderProjectNames(
  ordered: ProjectEntry[],
  moved: string,
  target: string,
  placeBefore: boolean,
): string[] {
  const parentOf = new Map(
    ordered.map((project) => [project.name, project.parent ?? ""]),
  );
  const parent = parentOf.get(target);
  if (
    moved === target ||
    parent === undefined ||
    parentOf.get(moved) !== parent
  ) {
    return ordered.map((project) => project.name);
  }
  const siblings = new Map<string, string[]>();
  for (const project of ordered) {
    const key = project.parent ?? "";
    siblings.set(key, [...(siblings.get(key) ?? []), project.name]);
  }
  const group = (siblings.get(parent) ?? []).filter((name) => name !== moved);
  const at = group.indexOf(target);
  group.splice(placeBefore ? at : at + 1, 0, moved);
  siblings.set(parent, group);

  const out: string[] = [];
  const seen = new Set<string>();
  const walk = (key: string) => {
    for (const name of siblings.get(key) ?? []) {
      if (seen.has(name)) continue;
      seen.add(name);
      out.push(name);
      walk(name);
    }
  };
  walk("");
  // Orphans sit under a parent that is not in the list, so the walk above never
  // reaches them; they keep their displayed position at the end.
  for (const project of ordered) {
    if (!seen.has(project.name)) out.push(project.name);
  }
  return out;
}
