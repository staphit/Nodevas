/**
 * Dependency wire context menu [B-06].
 *
 * Meaning and looks are set separately: the relation decides whether the edge
 * blocks, or counts as a prerequisite at all; the line only decides how it is
 * drawn. "自動" keeps whichever line the relation implies.
 */

import type { EdgeLine, EdgeRelation, Graph } from "../../types";
import {
  LINE_LABELS,
  RELATION_LABELS,
  edgeRelation,
} from "../../domain/graph/edgeStyle";
import type { LaneContextMenu } from "../LaneView";

const RELATION_HINTS: Record<EdgeRelation, string> = {
  "": "阻擋目標，直到來源完成",
  optional: "不阻擋，但仍算前置關係",
  deprecated: "只留下線，不列入前置與分析",
};

export interface EdgeMenuProps {
  contextMenu: Extract<LaneContextMenu, { kind: "edge" }>;
  graph: Graph | null;
  nodeTitle: (id: string | undefined) => string;
  setEdgeStyles: (
    targets: { from: string; to: string }[],
    patch: { relation?: EdgeRelation; line?: EdgeLine },
  ) => Promise<void>;
  convertEdgesToLogicGate: (
    edges: { from: string; to: string }[],
    menu: { x: number; y: number },
  ) => void;
  setContextMenu: (menu: LaneContextMenu | null) => void;
}

export function EdgeMenu({
  contextMenu,
  graph,
  nodeTitle,
  setEdgeStyles,
  convertEdgesToLogicGate,
  setContextMenu,
}: EdgeMenuProps) {
  const edgeOf = (target: { from: string; to: string }) =>
    (graph?.edges ?? []).find(
      (edge) => edge.from === target.from && edge.to === target.to,
    );
  const everyRelationIs = (relation: EdgeRelation) =>
    contextMenu.edges.every((target) => edgeRelation(edgeOf(target)) === relation);
  const everyLineIs = (line: EdgeLine) =>
    contextMenu.edges.every((target) => (edgeOf(target)?.line ?? "") === line);

  const apply = (patch: { relation?: EdgeRelation; line?: EdgeLine }) =>
    void setEdgeStyles(contextMenu.edges, patch)
      .then(() => setContextMenu(null))
      .catch(reportError);

  return (
    <>
      <div className="lane-context-title">
        {contextMenu.edges.length === 1
          ? `關係線 · ${nodeTitle(contextMenu.edges[0].from)} → ${nodeTitle(
              contextMenu.edges[0].to,
            )}`
          : `${nodeTitle(contextMenu.edges[0]?.to)} · ${
              contextMenu.edges.length
            } 條前置關係`}
      </div>
      <div className="lane-context-group-label">關係語意</div>
      {(Object.keys(RELATION_LABELS) as EdgeRelation[]).map((relation) => (
        <button
          key={relation || "required"}
          type="button"
          role="menuitemradio"
          aria-checked={everyRelationIs(relation)}
          onClick={() => apply({ relation })}
        >
          {everyRelationIs(relation) ? "✓ " : ""}
          {RELATION_LABELS[relation]}
          <small>{RELATION_HINTS[relation]}</small>
        </button>
      ))}
      {(everyRelationIs("optional") || everyRelationIs("deprecated")) && (
        <>
          <div className="lane-context-group-label">收進邏輯閘</div>
          <button
            type="button"
            role="menuitem"
            onClick={() =>
              convertEdgesToLogicGate(contextMenu.edges, {
                x: contextMenu.x,
                y: contextMenu.y,
              })
            }
          >
            轉成邏輯閘
            <small>
              建立一個{everyRelationIs("optional") ? "選用" : "棄用"}閘接管這
              {contextMenu.edges.length > 1 ? `${contextMenu.edges.length} 條` : "條"}
              關係；閘為多對多，會補齊所有輸入對輸出的連線。
            </small>
          </button>
        </>
      )}
      <div className="lane-context-group-label">線條外觀</div>
      <button
        type="button"
        role="menuitemradio"
        aria-checked={everyLineIs("")}
        onClick={() => apply({ line: "" })}
      >
        {everyLineIs("") ? "✓ " : ""}
        自動（跟隨語意）
      </button>
      {(Object.keys(LINE_LABELS) as Exclude<EdgeLine, "">[]).map((line) => (
        <button
          key={line}
          type="button"
          role="menuitemradio"
          aria-checked={everyLineIs(line)}
          onClick={() => apply({ line })}
        >
          {everyLineIs(line) ? "✓ " : ""}
          {LINE_LABELS[line]}
        </button>
      ))}
    </>
  );
}
