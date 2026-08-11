/**
 * Card appearance panel.
 *
 * Everything about how a node looks on the canvas — shape, size, colours and
 * text placement — with a live preview, instead of a row of small controls
 * squeezed into the metadata form.
 *
 * The controls themselves live in `AppearanceControls`, which knows nothing
 * about the store; this file is only the part that binds them to a node: which
 * style is being edited, where a change is written, and how it reports failure.
 * The creation form reuses the same controls with a draft style instead.
 */

import { useCallback } from "react";
import {
  nodeById,
  operationScope,
  reportError,
  useApp,
  useOperation,
  type CommandResult,
} from "../../store";
import type { NodeStyle, Status } from "../../types";
import { OperationStatus } from "../InteractionPrimitives";
import { AppearanceControls } from "./AppearanceControls";

export function NodeAppearance({ id }: { id: string }) {
  const graph = useApp((state) => state.graph);
  const statuses = useApp((state) => state.statuses);
  const updateNode = useApp((state) => state.updateNode);
  const operation = useOperation(operationScope.node(id));

  const node = nodeById(graph, id);
  const style = graph?.ui?.nodeStyles?.[id];
  const customStatuses = graph?.ui?.customStatuses ?? [];
  const status: Status = statuses[id] ?? "ready";

  const commit = useCallback(
    (patch: Partial<NodeStyle>) => {
      void (updateNode({ type: "node.setStyle", nodeId: id, patch }) as Promise<
        CommandResult
      >).then((result) => {
        if (!result.ok) reportError(new Error(result.message));
      });
    },
    [id, updateNode],
  );

  return (
    <div className="node-appearance" role="tabpanel" aria-label="外觀">
      <div className="section-head">
        <h3>卡片外觀</h3>
        <span className="section-hint">寫入 graph.yaml，整個專案看到的都一樣</span>
        <OperationStatus
          status={operation.status}
          message={
            operation.status === "error" || operation.status === "conflict"
              ? operation.message
              : undefined
          }
        />
      </div>

      <AppearanceControls
        idPrefix={id}
        style={style ?? {}}
        onChange={commit}
        status={status}
        customStatuses={customStatuses}
        previewTitle={node?.title || id}
        sizeHint="也可以直接拖曳畫布上卡片四周的控制點。"
      />
    </div>
  );
}
