/**
 * Editing what a wire means. The context menu, the batch selection and the
 * keyboard shortcut all end up here, so a relation always changes the same way
 * and reports the same notice.
 */

import type { CanvasCommand } from "../../store";
import type { CommandResult } from "../../state/operations";
import type { EdgeLine, EdgeRelation, Graph } from "../../types";
import {
  LINE_LABELS,
  RELATION_LABELS,
  edgeRelation,
} from "../../domain/graph/edgeStyle";
import { edgeKeyEndpoints } from "../canvas/geometry";
import type { GraphSelection } from "../LaneView";

export function useEdgeCommands({
  graph,
  graphSelection,
  updateCanvasLayout,
  setGraphNotice,
}: {
  graph: Graph | null;
  graphSelection: GraphSelection;
  updateCanvasLayout: (command: CanvasCommand) => Promise<CommandResult>;
  setGraphNotice: (notice: { text: string; kind: "ok" | "error" } | null) => void;
}) {
  const selectedEdgeEndpoints =
    graphSelection?.kind === "edge"
      ? { from: graphSelection.from, to: graphSelection.to }
      : graphSelection?.kind === "edges" && graphSelection.edges.length === 1
        ? graphSelection.edges[0]
      : graphSelection?.kind === "vertex"
        ? edgeKeyEndpoints(graphSelection.wireKey)
        : null;

  const setEdgeStyles = async (
    targets: { from: string; to: string }[],
    patch: { relation?: EdgeRelation; line?: EdgeLine },
  ) => {
    const result = await updateCanvasLayout({
      type: "canvas.setEdgeStyle",
      edges: targets,
      relation: patch.relation,
      line: patch.line,
    });
    if (!result.ok) {
      setGraphNotice({ text: result.message, kind: "error" });
      return;
    }
    const changed =
      patch.relation !== undefined
        ? RELATION_LABELS[patch.relation]
        : patch.line
          ? LINE_LABELS[patch.line as Exclude<EdgeLine, "">]
          : "自動線條";
    setGraphNotice({
      text: `${targets.length > 1 ? `${targets.length} 條關係` : "關係"}已設為${changed}`,
      kind: "ok",
    });
  };

  // The keyboard shortcut cycles the meaning: 必要 → 選用 → 棄用 → 必要.
  const toggleEdgeStyle = async (from: string, to: string) => {
    const current = (graph?.edges ?? []).find(
      (edge) => edge.from === from && edge.to === to,
    );
    if (!current) return;
    const order: EdgeRelation[] = ["", "optional", "deprecated"];
    const next = order[(order.indexOf(edgeRelation(current)) + 1) % order.length];
    await setEdgeStyles([{ from, to }], { relation: next });
  };

  return { selectedEdgeEndpoints, setEdgeStyles, toggleEdgeStyle };
}
