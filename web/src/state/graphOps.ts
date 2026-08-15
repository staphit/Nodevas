/**
 * Command → server-op translation [P2].
 *
 * A whole-file PUT /api/graph refuses to overwrite a revision it has not seen,
 * which is right for one editor and wrong for two: dragging a card and renaming
 * a different node touch the same file, so one loses to a 409. These are the
 * commands that map cleanly onto the smallest-thing-changed ops the server
 * applies under its write lock, so they can skip the whole-file lock entirely.
 *
 * A command that returns null here is not op-able (groups, wires, gates and
 * logic gates, …) and stays on PUT /api/graph.
 */

import type { GraphOp } from "../api";
import type { GraphCommand } from "../domain";
import type { Graph } from "../types";

export function graphCommandToOps(
  command: GraphCommand,
  resultingGraph?: Graph,
): GraphOp[] | null {
  switch (command.type) {
    case "canvas.moveNodes":
      // A move that also slides the board — a card dragged past the top-left
      // corner — moves the groups, notes, gates and wire vertices with it, and
      // there is no op for that. Sending only the positions would apply the
      // move and silently drop the slide, so the whole command goes on the
      // PUT path where `canvas.shiftBoard` runs.
      if (command.shift) return null;
      return Object.entries(command.positions).map(([nodeId, point]) => ({
        kind: "move",
        nodeId,
        x: point.x,
        y: point.y,
      }));

    case "canvas.setTimelineOrder":
      return [{ kind: "timeline-order", order: command.order }];

    case "canvas.removeEdge":
      return [{ kind: "remove-edge", from: command.from, to: command.to }];

    case "canvas.setEdgeStyle":
      // Relation changes also rewrite the target's requires expression. Send
      // the final stored style to the Store, which applies both under one lock.
      if (!resultingGraph) return null;
      if (command.edges.length === 0) return null;
      {
        const ops: GraphOp[] = [];
        for (const { from, to } of command.edges) {
          const edge = resultingGraph.edges?.find(
            (candidate) => candidate.from === from && candidate.to === to,
          );
          // The domain command deliberately treats a stale selection as a no-op.
          // Keep that contract on the PUT path instead of emitting an invalid op
          // that would reject the whole batch.
          if (!edge) return null;
          ops.push({
            kind: "set-edge-style",
            from,
            to,
            relation: edge.relation ?? "",
            line: edge.line ?? "",
          });
        }
        return ops;
      }

    case "node.updateMetadata": {
      const { patch } = command;
      const op: GraphOp = { kind: "node-metadata", nodeId: command.nodeId };
      // A field present in the patch is set; an absent one is left alone — the
      // same nil-means-leave-alone rule the server op follows, which is what
      // lets two people edit different fields of one node without clobbering.
      // An empty value clears the field (the server marshals it away).
      if ("title" in patch) op.title = patch.title ?? "";
      if ("kind" in patch) op.nodeKind = patch.kind ?? "";
      if ("priority" in patch) op.priority = patch.priority ?? "";
      if ("deadline" in patch) op.deadline = patch.deadline ?? "";
      if ("tags" in patch) op.tags = patch.tags ?? [];
      return [op];
    }

    case "node.setStyle": {
      // The node-size op only carries width+height. A resize sets exactly those;
      // a colour/shape/alignment change carries other keys and must stay on the
      // whole-file path.
      const { patch } = command;
      const keys = Object.keys(patch);
      const sizeOnly =
        keys.length > 0 && keys.every((key) => key === "width" || key === "height");
      if (sizeOnly && patch.width != null && patch.height != null) {
        return [
          {
            kind: "node-size",
            nodeId: command.nodeId,
            width: patch.width,
            height: patch.height,
          },
        ];
      }
      return null;
    }

    default:
      return null;
  }
}

/** Dependency ops are materialized by the Store; local drafts are not final. */
export function graphOpsNeedServerGraph(ops: readonly GraphOp[] | null): boolean {
  return Boolean(
    ops?.some(
      (op) =>
        op.kind === "add-edge" ||
        op.kind === "remove-edge" ||
        op.kind === "set-edge-style",
    ),
  );
}
