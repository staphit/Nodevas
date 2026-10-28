/**
 * A node's dependency editor [B-06].
 *
 * Reached from the node menu's "Add dependency…" and from a wire's gate. The
 * default view is plain "depends on": the nodes this one waits for, and a way
 * to add one. How several conditions combine, optional/deprecated relations
 * and standalone logic gates sit under "Advanced conditions", which opens by
 * itself whenever the node already uses one of them. Open, duplicate, trash
 * and the agent actions are the short menu's (NodeActionsMenu).
 */

import { useEffect, useMemo, useState } from "react";
import type { EdgeRelation, GraphEdge, GraphNode, LogicGate } from "../../types";
import { edgeRelation } from "../../domain/graph/edgeStyle";
import { nodesWaitingOn } from "../../domain/graph/logicGate";
import { requiresNodeRefs } from "../../domain/graph/requires";
import { IconPlus } from "../../icons";
import type { LaneContextMenu } from "../LaneView";
import { localizedRelationLabel, useI18n } from "../../i18n";
import {
  advancedConditionKind,
  hasAdvancedCondition,
} from "../canvas/simpleDependency";

export interface NodeMenuProps {
  contextMenu: Extract<LaneContextMenu, { kind: "node" }>;
  nodeTitle: (id: string | undefined) => string;
  /** The board's dependency edges, to leave out nodes that would close a loop. */
  edges: readonly GraphEdge[];
  incomingConditions: GraphEdge[];
  availableConditionSources: GraphNode[];
  conditionSource: string;
  setConditionSource: (id: string) => void;
  conditionOperator: string;
  changeGateOperator: (targetId: string, operator: string) => void;
  conditionRelation: EdgeRelation;
  setConditionRelation: (relation: EdgeRelation) => void;
  hasEditableGateOperator: boolean;
  /** The node being edited; its `requires` decides what counts as advanced. */
  conditionTarget?: GraphNode;
  addDependency: (targetId: string) => void;
  setDependencyRelation: (
    targetId: string,
    sourceId: string,
    relation: EdgeRelation,
  ) => void;
  controllingLogicGate: LogicGate | undefined;
  assignableLogicGates: LogicGate[];
  assignExistingLogicGateToNode: (targetId: string, gateId: string) => void;
  setContextMenu: (menu: LaneContextMenu | null) => void;
}

export function NodeMenu({
  contextMenu,
  nodeTitle,
  edges,
  incomingConditions,
  availableConditionSources,
  conditionSource,
  setConditionSource,
  conditionOperator,
  changeGateOperator,
  conditionRelation,
  setConditionRelation,
  hasEditableGateOperator,
  conditionTarget,
  addDependency,
  setDependencyRelation,
  controllingLogicGate,
  assignableLogicGates,
  assignExistingLogicGateToNode,
  setContextMenu,
}: NodeMenuProps) {
  const { t, language } = useI18n();
  const requires = conditionTarget?.requires?.trim() ?? "";
  // A node the expression already names is a prerequisite even before its
  // wire is drawn, so it is not offered again; nor is one that already waits
  // on this node, which would close a loop the add is refused for.
  const nodeId = contextMenu.nodeId;
  const choices = useMemo(() => {
    const named = new Set(requiresNodeRefs(requires));
    const downstream = nodesWaitingOn(edges, nodeId);
    return availableConditionSources.filter((node) => !named.has(node.id) && !downstream.has(node.id));
  }, [availableConditionSources, requires, edges, nodeId]);
  useEffect(() => {
    if (choices.some((node) => node.id === conditionSource)) return;
    setConditionSource(choices[0]?.id ?? "");
  }, [choices, conditionSource, setConditionSource]);
  const conditionKind = advancedConditionKind(requires);
  const hasRelationOverride = incomingConditions.some(
    (edge) => edgeRelation(edge) !== "",
  );
  // Anything beyond "depends on all of these" is advanced. When the node
  // already uses it, the disclosure opens and its summary names it, so stored
  // conditions are never tucked out of sight. Only stored data counts: the
  // relation drafted for a new dependency is picked from inside the open
  // disclosure, and the Add button already names it.
  const advanced =
    Boolean(controllingLogicGate) ||
    hasAdvancedCondition(requires, incomingConditions);
  const [advancedOpen, setAdvancedOpen] = useState(advanced);
  useEffect(() => {
    if (advanced) setAdvancedOpen(true);
  }, [advanced]);
  const advancedBadge = controllingLogicGate
    ? t(`logicGate.op.${controllingLogicGate.operator}`)
    : conditionKind === "custom"
      ? t("dependency.customCondition")
      : conditionKind
        ? t(`gate.label.${conditionKind}`)
        : hasRelationOverride
          ? t("dependency.relationsChanged")
          : null;

  return (
    <>
      <div className="lane-context-title condition-menu-title">
        <span>{t("nodeMenu.title")}</span>
        <small>{nodeTitle(contextMenu.nodeId)}</small>
      </div>
      <div className="condition-menu-intro">
        {t("nodeMenu.intro")}
      </div>
      {controllingLogicGate ? (
        <div className="controlled-by-standalone-gate">
          <b>{t("nodeMenu.gateManaged")}</b>
          <span>
            {t(`logicGate.op.${controllingLogicGate.operator}`)} ·{" "}
            {controllingLogicGate.id}
          </span>
          <small>{t("nodeMenu.gateManagedHint")}</small>
        </div>
      ) : (
        <>
          <div className="lane-context-summary dependency-list">
            <b>{t("nodeMenu.dependsOn")}</b>
            {incomingConditions.length === 0 ? (
              <p>{t("nodeMenu.noDependencies")}</p>
            ) : (
              <ul>
                {incomingConditions.map((edge) => {
                  const relation = edgeRelation(edge);
                  return (
                    <li key={`${edge.from}->${edge.to}`}>
                      <span>{nodeTitle(edge.from)}</span>
                      {relation !== "" && (
                        <small className={`dependency-badge relation-${relation}`}>
                          {localizedRelationLabel(relation, language)}
                        </small>
                      )}
                    </li>
                  );
                })}
              </ul>
            )}
          </div>
          <label className="lane-context-field">
            <span className="lane-context-label">{t("nodeMenu.addSource")}</span>
            <select
              value={conditionSource}
              disabled={choices.length === 0}
              onChange={(event) => setConditionSource(event.target.value)}
            >
              {choices.length === 0 ? (
                <option value="">{t("nodeMenu.noSources")}</option>
              ) : (
                choices.map((node) => (
                  <option key={node.id} value={node.id}>
                    {node.title || node.id}
                  </option>
                ))
              )}
            </select>
          </label>
          <button
            type="button"
            role="menuitem"
            className="condition-primary-action"
            disabled={!conditionSource}
            onClick={() => addDependency(contextMenu.nodeId)}
          >
            <span className="condition-primary-icon" aria-hidden>
              <IconPlus size={15} />
            </span>
            <span className="condition-primary-copy">
              <b>{t("nodeMenu.addPrerequisite")}</b>
              <small>
                {conditionRelation !== ""
                  ? t(`nodeMenu.${conditionRelation}`)
                  : incomingConditions.length === 0
                    ? t("nodeMenu.firstPrerequisite")
                    : conditionOperator === "and"
                      ? t("nodeMenu.addAllOf")
                      : t("nodeMenu.addWithOperator", {
                          operator: t(`nodeMenu.${conditionOperator}`),
                        })}
              </small>
            </span>
            <span className="condition-primary-arrow" aria-hidden>→</span>
          </button>
        </>
      )}
      <details
        className="dependency-advanced"
        open={advancedOpen}
        onToggle={(event) => setAdvancedOpen(event.currentTarget.open)}
      >
        <summary>
          <span>{t("dependency.advanced")}</span>
          {advancedBadge && (
            <small className="dependency-badge">{advancedBadge}</small>
          )}
        </summary>
        <div className="dependency-advanced-body">
          <p className="lane-context-field-help">{t("dependency.advancedHint")}</p>
          {assignableLogicGates.length > 0 && (
            <div className="assign-existing-gate">
              <div>
                <b>{t("nodeMenu.existingGate")}</b>
                <small>{t("nodeMenu.existingGateHint")}</small>
              </div>
              <select
                value={controllingLogicGate?.id ?? ""}
                onChange={(event) =>
                  assignExistingLogicGateToNode(
                    contextMenu.nodeId,
                    event.target.value,
                  )
                }
              >
                <option value="">{t("nodeMenu.noStandaloneGate")}</option>
                {assignableLogicGates.map((gate) => (
                  <option key={gate.id} value={gate.id}>
                    {t(`logicGate.op.${gate.operator}`)} · {gate.id} · {t("logicGate.input")} {gate.inputs.length}
                  </option>
                ))}
              </select>
              {controllingLogicGate && (
                <button
                  type="button"
                  onClick={() =>
                    setContextMenu({
                      kind: "logic-gate",
                      gateId: controllingLogicGate.id,
                      x: contextMenu.x,
                      y: contextMenu.y,
                    })
                  }
                >
                  {t("nodeMenu.editGate")}
                </button>
              )}
            </div>
          )}
          {!controllingLogicGate && (
            <>
              {conditionKind !== null && (
                <div className="lane-context-field">
                  <span className="lane-context-label">{t("dependency.expression")}</span>
                  <code className="dependency-expression">{requires}</code>
                </div>
              )}
              {(hasEditableGateOperator || incomingConditions.length > 0) && (
                <label className="lane-context-field">
                  <span className="lane-context-label">{t("nodeMenu.conditionMode")}</span>
                  <select
                    value={conditionOperator}
                    onChange={(event) =>
                      changeGateOperator(contextMenu.nodeId, event.target.value)
                    }
                  >
                    <option value="and">{t("nodeMenu.and")}</option>
                    <option value="or">{t("nodeMenu.or")}</option>
                    <option value="xor">{t("nodeMenu.xor")}</option>
                    <option value="nand">{t("nodeMenu.nand")}</option>
                    <option value="nor">{t("nodeMenu.nor")}</option>
                  </select>
                  <small className="lane-context-field-help">
                    {t(`nodeMenu.${conditionOperator}`)}
                  </small>
                </label>
              )}
              {incomingConditions.length > 0 && (
                <div className="lane-context-summary">
                  <b>{t("nodeMenu.currentPrereqs")}</b>
                  <p>{t("nodeMenu.relationHint")}</p>
                  {incomingConditions.map((edge) => (
                    <label
                      className="lane-context-relation"
                      key={`${edge.from}->${edge.to}`}
                    >
                      <span>{nodeTitle(edge.from)}</span>
                      <select
                        value={edgeRelation(edge)}
                        aria-label={t("nodeMenu.relationAria", { title: nodeTitle(edge.from) })}
                        onChange={(event) =>
                          setDependencyRelation(
                            contextMenu.nodeId,
                            edge.from,
                            event.target.value as EdgeRelation,
                          )
                        }
                      >
                        {(["", "optional", "deprecated"] as EdgeRelation[]).map((relation) => (
                          <option key={relation} value={relation}>
                            {localizedRelationLabel(relation, language)}
                          </option>
                        ))}
                      </select>
                    </label>
                  ))}
                </div>
              )}
              <label className="lane-context-field">
                <span className="lane-context-label">{t("nodeMenu.newRelation")}</span>
                <select
                  value={conditionRelation}
                  onChange={(event) =>
                    setConditionRelation(event.target.value as EdgeRelation)
                  }
                >
                  <option value="">{t("nodeMenu.required")}</option>
                  <option value="optional">{t("nodeMenu.optional")}</option>
                  <option value="deprecated">{t("nodeMenu.deprecated")}</option>
                </select>
              </label>
            </>
          )}
        </div>
      </details>
      <div className="lane-context-help">
        {t("nodeMenu.help")}
      </div>
    </>
  );
}
