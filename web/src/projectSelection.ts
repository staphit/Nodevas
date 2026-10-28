import { useState } from "react";

/**
 * The board's selected node: one for the graph and timeline panes together,
 * which they show and change through App.
 */
export function useProjectNodeSelection(
  _project: string,
): [string | null, (nodeId: string | null) => void] {
  const [selectedNodeId, setSelectedNodeId] = useState<string | null>(null);
  return [selectedNodeId, setSelectedNodeId];
}
