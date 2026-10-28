import { useCallback, useState } from "react";

/**
 * The board's selected node: one for the graph and timeline panes together,
 * which they show and change through App. It belongs to the project it was
 * made in. Node ids repeat across projects (node-0001 in each), so a selection
 * carried over a switch would pick an unrelated card of the new project; it
 * reads as none there until something is selected.
 */
export function useProjectNodeSelection(
  project: string,
): [string | null, (nodeId: string | null) => void] {
  const [selection, setSelection] = useState<{ project: string; nodeId: string | null }>({
    project,
    nodeId: null,
  });
  // Bound to the project it was handed out for: a select that arrives late
  // from the one just left stays with it.
  const select = useCallback(
    (nodeId: string | null) => setSelection({ project, nodeId }),
    [project],
  );
  return [selection.project === project ? selection.nodeId : null, select];
}
