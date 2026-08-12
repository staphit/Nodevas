/**
 * Dependency wire context menu [B-06].
 *
 * Meaning and looks are set separately: the relation decides whether the edge
 * blocks, or counts as a prerequisite at all; the line only decides how it is
 * drawn. "自動" keeps whichever line the relation implies.
 */

import type { EdgeLine, EdgeRelation, Graph } from "../../types";
import { localizedLineLabel, localizedRelationLabel, useI18n } from "../../i18n";
import {
  LINE_LABELS,
  RELATION_LABELS,
  edgeRelation,
} from "../../domain/graph/edgeStyle";
import type { LaneContextMenu } from "../LaneView";

const RELATION_HINT_KEYS: Record<EdgeRelation, string> = {
  "": "edgeMenu.requiredHint",
  optional: "edgeMenu.optionalHint",
  deprecated: "edgeMenu.deprecatedHint",
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
  const { t } = useI18n();
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
          ? `${t("edgeMenu.connection")} · ${nodeTitle(contextMenu.edges[0].from)} → ${nodeTitle(
              contextMenu.edges[0].to,
            )}`
          : `${nodeTitle(contextMenu.edges[0]?.to)} · ${
              contextMenu.edges.length
            } ${t("edgeMenu.prerequisites")}`}
      </div>
      <div className="lane-context-group-label">{t("edgeMenu.relationMeaning")}</div>
      {(Object.keys(RELATION_LABELS) as EdgeRelation[]).map((relation) => (
        <button
          key={relation || "required"}
          type="button"
          role="menuitemradio"
          aria-checked={everyRelationIs(relation)}
          onClick={() => apply({ relation })}
        >
          {everyRelationIs(relation) ? "✓ " : ""}
          {localizedRelationLabel(relation)}
          <small>{t(RELATION_HINT_KEYS[relation])}</small>
        </button>
      ))}
      {(everyRelationIs("optional") || everyRelationIs("deprecated")) && (
        <>
          <div className="lane-context-group-label">{t("edgeMenu.logicGateGroup")}</div>
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
            {t("edgeMenu.convertToGate")}
            <small>
              {t("edgeMenu.convertHint", {
                relation: t(`logicGate.op.${everyRelationIs("optional") ? "optional" : "deprecated"}`),
                count: contextMenu.edges.length,
              })}
            </small>
          </button>
        </>
      )}
      <div className="lane-context-group-label">{t("edgeMenu.lineAppearance")}</div>
      <button
        type="button"
        role="menuitemradio"
        aria-checked={everyLineIs("")}
        onClick={() => apply({ line: "" })}
      >
        {everyLineIs("") ? "✓ " : ""}
        {t("edgeMenu.autoLine")}
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
          {localizedLineLabel(line)}
        </button>
      ))}
    </>
  );
}
