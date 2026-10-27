/**
 * Node types in the UI: the resolved list for pickers, the localized name and
 * the small label chip every card wears so nobody has to memorize shapes.
 * The model itself lives in `domain/graph/nodeTypes.ts`.
 */

import { useMemo } from "react";
import { nodeTypeById, nodeTypesForLayout } from "./domain/graph/nodeTypes";
import { translate, useI18n } from "./i18n";
import { safeCssColor } from "./cssColor";
import { useApp } from "./store";
import type { GraphNode, NodeShape, NodeStyle, NodeType } from "./types";

/** Every type the project can use: built-ins in layout order, then custom ones. */
export function useNodeTypes(): NodeType[] {
  const graph = useApp((s) => s.graph);
  return useMemo(() => nodeTypesForLayout(graph), [graph]);
}

/** The resolved type of a node (by id) or of a type id. */
export function useNodeType(target: { nodeId: string } | { kind: string | undefined }): NodeType {
  const graph = useApp((s) => s.graph);
  const kind =
    "nodeId" in target
      ? graph?.nodes?.find((node: GraphNode) => node.id === target.nodeId)?.kind
      : target.kind;
  return useMemo(() => nodeTypeById(graph, kind), [graph, kind]);
}

/** Built-ins read in the viewer's language; custom types as their author named them. */
export function nodeTypeLabel(type: NodeType, t: (key: string) => string = (key) => translate(key)): string {
  return type.builtin ? t(`nodeType.${type.id}`) : type.label;
}

/** A per-node shape override wins over the type's default. */
export function effectiveShape(style: NodeStyle | undefined, type: NodeType): NodeShape {
  return style?.shape ?? type.shape;
}

/** The card's type label: small, in the type's colour. */
export function NodeTypeChip({
  type,
  className = "",
}: {
  type: NodeType;
  className?: string;
}) {
  const { t } = useI18n();
  const label = nodeTypeLabel(type, t);
  const color = safeCssColor(type.color);
  return (
    <span
      className={`col-card-type${className ? ` ${className}` : ""}`}
      data-node-type={type.id}
      style={color ? { ["--card-type-color" as string]: color } : undefined}
    >
      {label}
    </span>
  );
}
