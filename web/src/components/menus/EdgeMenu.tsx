/**
 * Dependency wire context menu [B-06].
 *
 * Meaning and looks are set separately: the relation decides whether the edge
 * blocks, or counts as a prerequisite at all; the line only decides how it is
 * drawn. "自動" keeps whichever line the relation implies.
 *
 * A wire reads as "B depends on A" by default. The relations that change that
 * meaning — optional, deprecated, and folding wires into a logic gate — sit
 * under "Advanced conditions", which opens by itself when the wire already
 * uses one of them.
 */

import { useEffect, useState } from "react";
import type { EdgeLine, EdgeRelation, Graph } from "../../types";
import { localizedLineLabel, localizedRelationLabel, useI18n } from "../../i18n";
import { LINE_LABELS, edgeRelation } from "../../domain/graph/edgeStyle";
import type { LaneContextMenu } from "../LaneView";

const ADVANCED_RELATIONS: Exclude<EdgeRelation, "">[] = ["optional", "deprecated"];

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
  const advanced = contextMenu.edges.some(
    (target) => edgeRelation(edgeOf(target)) !== "",
  );
  const [advancedOpen, setAdvancedOpen] = useState(advanced);
  useEffect(() => {
    if (advanced) setAdvancedOpen(true);
  }, [advanced]);

  const apply = (patch: { relation?: EdgeRelation; line?: EdgeLine }) =>
    void setEdgeStyles(contextMenu.edges, patch)
      .then(() => setContextMenu(null))
      .catch(reportError);

  const relationButton = (relation: EdgeRelation) => (
    <button
      key={relation || "required"}
      type="button"
      role="menuitemradio"
      aria-checked={everyRelationIs(relation)}
      onClick={() => apply({ relation })}
    >
      {everyRelationIs(relation) ? "✓ " : ""}
      {relation === "" ? t("edgeMenu.dependsOn") : localizedRelationLabel(relation)}
      <small>{t(RELATION_HINT_KEYS[relation])}</small>
    </button>
  );

  return (
    <>
      <div className="lane-context-title">
        {contextMenu.edges.length === 1
          ? t("edgeMenu.dependsOnTitle", {
              target: nodeTitle(contextMenu.edges[0].to),
              source: nodeTitle(contextMenu.edges[0].from),
            })
          : `${nodeTitle(contextMenu.edges[0]?.to)} · ${
              contextMenu.edges.length
            } ${t("edgeMenu.prerequisites")}`}
      </div>
      <div className="lane-context-group-label">{t("edgeMenu.relationMeaning")}</div>
      {relationButton("")}
      <details
        className="dependency-advanced"
        open={advancedOpen}
        onToggle={(event) => setAdvancedOpen(event.currentTarget.open)}
      >
        <summary>
          <span>{t("dependency.advanced")}</span>
          {advanced && (
            <small className="dependency-badge">
              {ADVANCED_RELATIONS.filter((relation) =>
                contextMenu.edges.some(
                  (target) => edgeRelation(edgeOf(target)) === relation,
                ),
              )
                .map((relation) => localizedRelationLabel(relation))
                .join(" · ")}
            </small>
          )}
        </summary>
        <div className="dependency-advanced-body">
          {ADVANCED_RELATIONS.map(relationButton)}
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
        </div>
      </details>
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
