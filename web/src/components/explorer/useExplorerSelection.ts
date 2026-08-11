/**
 * Explorer tree selection [B-06].
 *
 * Node rows and project rows are two independent selections that obey the same
 * file-manager rules, so the modifier handling lives here once instead of being
 * written out twice in the sidebar.
 */

import { useEffect, useState } from "react";
import type { ProjectEntry } from "../../state/types";
import type { GraphNode } from "../../types";

/** What the pointer's modifier keys mean for a tree selection. */
export type SelectionModifiers = { additive: boolean; range: boolean };

/** Range between two ids in the order the rows are drawn, both ends kept. */
const rangeBetween = (order: string[], from: string, to: string): string[] => {
  const start = order.indexOf(from);
  const end = order.indexOf(to);
  if (start < 0 || end < 0) return [to];
  return start <= end
    ? order.slice(start, end + 1)
    : order.slice(end, start + 1);
};

const applySelection = (
  id: string,
  modifiers: SelectionModifiers,
  order: string[],
  anchor: string | null,
  setAnchor: (value: string | null) => void,
  setSelection: (update: (current: string[]) => string[]) => void,
) => {
  if (modifiers.range && anchor) {
    const span = rangeBetween(order, anchor, id);
    // Ctrl + Shift adds the range; Shift alone replaces the selection. The
    // anchor stays put so the range can be resized by clicking again.
    setSelection((current) =>
      modifiers.additive
        ? [...current, ...span.filter((candidate) => !current.includes(candidate))]
        : span,
    );
    return;
  }
  setAnchor(id);
  setSelection((current) =>
    modifiers.additive
      ? current.includes(id)
        ? current.filter((candidate) => candidate !== id)
        : [...current, id]
      : [id],
  );
};

export function useExplorerSelection(
  nodes: GraphNode[] | null | undefined,
  projects: ProjectEntry[],
  visibleNodes: GraphNode[],
  visibleProjects: ProjectEntry[],
) {
  // Ctrl/⌘ click builds a selection in the tree; a plain click collapses it.
  const [selectedNodeIDs, setSelectedNodeIDs] = useState<string[]>([]);
  const [selectedProjectNames, setSelectedProjectNames] = useState<string[]>([]);

  // Shift extends from the last item clicked, the way a file manager does.
  const [nodeAnchor, setNodeAnchor] = useState<string | null>(null);
  const [projectAnchor, setProjectAnchor] = useState<string | null>(null);

  // A node deleted here or on the canvas must not stay selected.
  useEffect(() => {
    const live = new Set((nodes ?? []).map((node) => node.id));
    setSelectedNodeIDs((current) => {
      const next = current.filter((id) => live.has(id));
      return next.length === current.length ? current : next;
    });
  }, [nodes]);

  useEffect(() => {
    const live = new Set(projects.map((project) => project.name));
    setSelectedProjectNames((current) => {
      const next = current.filter((name) => live.has(name));
      return next.length === current.length ? current : next;
    });
  }, [projects]);

  const toggleNodeSelection = (id: string, modifiers: SelectionModifiers) => {
    applySelection(
      id,
      modifiers,
      visibleNodes.map((node) => node.id),
      nodeAnchor,
      setNodeAnchor,
      setSelectedNodeIDs,
    );
  };

  const toggleProjectSelection = (name: string, modifiers: SelectionModifiers) => {
    applySelection(
      name,
      modifiers,
      visibleProjects.map((project) => project.name),
      projectAnchor,
      setProjectAnchor,
      setSelectedProjectNames,
    );
  };

  return {
    selectedNodeIDs,
    setSelectedNodeIDs,
    toggleNodeSelection,
    selectedProjectNames,
    setSelectedProjectNames,
    toggleProjectSelection,
  };
}
