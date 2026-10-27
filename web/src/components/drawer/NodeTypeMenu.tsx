/**
 * The node type, in the panel's header: the same chip the card wears, and a
 * menu to change it. A type change is a metadata edit like any other, so it
 * goes through `node.updateMetadata` and the same op path.
 *
 * A node may still carry an old free-text kind ("bug") that names no type in
 * this project. It behaves as Task; the menu says so, which is where someone
 * wondering why would look — the board's Problems list only carries one hint
 * for the whole project.
 */

import { isKnownKind, normalizeKind } from "../../domain/graph/nodeTypes";
import { reportError, useApp } from "../../store";
import { useI18n } from "../../i18n";
import { NodeTypeChip, nodeTypeLabel, useNodeType, useNodeTypes } from "../../nodeTypeTheme";
import { DrawerMenu } from "./DrawerMenu";

export function NodeTypeMenu({ id }: { id: string }) {
  const { t } = useI18n();
  const nodeTypes = useNodeTypes();
  const current = useNodeType({ nodeId: id });
  const updateNodeMetadata = useApp((s) => s.updateNodeMetadata);
  const unknownKind = useApp((s) => {
    const kind = s.graph?.nodes?.find((node) => node.id === id)?.kind;
    const normalized = normalizeKind(kind);
    return kind?.trim() && !isKnownKind(s.graph, normalized) ? kind.trim() : "";
  });
  const label = nodeTypeLabel(current, t);
  const firstCustom = nodeTypes.findIndex((type) => !type.builtin);
  const unknownHint = unknownKind ? t("nodeType.unknownHint", { kind: unknownKind }) : "";
  return (
    <DrawerMenu
      label={t("drawer.nodeType", { type: label })}
      trigger={<NodeTypeChip type={current} />}
      triggerClassName="drawer-head-button drawer-type-button"
      triggerTitle={
        unknownHint
          ? `${t("drawer.nodeType", { type: label })}\n${unknownHint}`
          : t("drawer.nodeType", { type: label })
      }
      items={nodeTypes.map((type, index) => ({
        id: type.id,
        label: nodeTypeLabel(type, t),
        checked: type.id === current.id,
        separatorBefore: index === firstCustom && index > 0,
        onSelect: () => {
          if (type.id === current.id) return;
          void updateNodeMetadata(id, { kind: type.id }).then((result) => {
            if (!result.ok) reportError(new Error(result.message));
          });
        },
      }))}
    >
      {unknownHint ? () => <p className="node-type-unknown-hint">{unknownHint}</p> : undefined}
    </DrawerMenu>
  );
}
