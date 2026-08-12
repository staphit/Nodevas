/**
 * Node context menu [B-06].
 *
 * Everything about one node's dependencies: which conditions feed it, how they
 * combine, whether a first-class logic gate owns them, plus open/duplicate/
 * delete.
 */

import type { EdgeRelation, GraphEdge, GraphNode, LogicGate } from "../../types";
import { edgeRelation } from "../../domain/graph/edgeStyle";
import { IconPlus } from "../../icons";
import { confirmAction } from "../ConfirmDialog";
import type { LaneContextMenu } from "../LaneView";
import { localizedRelationLabel, useI18n } from "../../i18n";

export interface NodeMenuProps {
  contextMenu: Extract<LaneContextMenu, { kind: "node" }>;
  nodeTitle: (id: string | undefined) => string;
  incomingConditions: GraphEdge[];
  availableConditionSources: GraphNode[];
  conditionSource: string;
  setConditionSource: (id: string) => void;
  conditionOperator: string;
  changeGateOperator: (targetId: string, operator: string) => void;
  conditionRelation: EdgeRelation;
  setConditionRelation: (relation: EdgeRelation) => void;
  hasEditableGateOperator: boolean;
  addDependency: (targetId: string) => void;
  setDependencyRelation: (
    targetId: string,
    sourceId: string,
    relation: EdgeRelation,
  ) => void;
  controllingLogicGate: LogicGate | undefined;
  assignableLogicGates: LogicGate[];
  assignExistingLogicGateToNode: (targetId: string, gateId: string) => void;
  openTab: (id: string) => Promise<void>;
  selectNode: (id: string) => void;
  duplicateNode: (id: string) => Promise<string>;
  deleteNode: (id: string) => Promise<void>;
  setContextMenu: (menu: LaneContextMenu | null) => void;
}

export function NodeMenu({
  contextMenu,
  nodeTitle,
  incomingConditions,
  availableConditionSources,
  conditionSource,
  setConditionSource,
  conditionOperator,
  changeGateOperator,
  conditionRelation,
  setConditionRelation,
  hasEditableGateOperator,
  addDependency,
  setDependencyRelation,
  controllingLogicGate,
  assignableLogicGates,
  assignExistingLogicGateToNode,
  openTab,
  selectNode,
  duplicateNode,
  deleteNode,
  setContextMenu,
}: NodeMenuProps) {
  const { t, language } = useI18n();
  return (
    <>
      <div className="lane-context-title condition-menu-title">
        <span>{t("nodeMenu.title")}</span>
        <small>{nodeTitle(contextMenu.nodeId)}</small>
      </div>
      <div className="condition-menu-intro">
        {t("nodeMenu.intro")}
      </div>
      <div className="condition-menu-node-actions">
        <button
          type="button"
          onClick={() => {
            setContextMenu(null);
            void openTab(contextMenu.nodeId).catch(reportError);
          }}
        >
          {t("nodeMenu.open")}
        </button>
        <button
          type="button"
          onClick={() => {
            setContextMenu(null);
            void duplicateNode(contextMenu.nodeId)
              .then((id) => selectNode(id))
              .catch(reportError);
          }}
        >
          {t("nodeMenu.duplicate")}
        </button>
        <button
          type="button"
          className="danger"
          onClick={() => {
            const id = contextMenu.nodeId;
            setContextMenu(null);
            void confirmAction({
              title: t("nodeMenu.trashTitle"),
              description: t("nodeMenu.trashDescription", { title: nodeTitle(id) }),
              confirmLabel: t("batch.moveToTrash"),
              tone: "danger",
            }).then((confirmed) => {
              if (confirmed) return deleteNode(id);
            }).catch(reportError);
          }}
        >
          {t("nodeMenu.trash")}
        </button>
      </div>
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
      <label className="lane-context-field">
        <span className="lane-context-label">{t("nodeMenu.addSource")}</span>
        <select
          value={conditionSource}
          disabled={availableConditionSources.length === 0}
          onChange={(event) => setConditionSource(event.target.value)}
        >
          {availableConditionSources.length === 0 ? (
            <option value="">{t("nodeMenu.noSources")}</option>
          ) : (
            availableConditionSources.map((node) => (
              <option key={node.id} value={node.id}>
                {node.title || node.id}
              </option>
            ))
          )}
        </select>
      </label>
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
            {incomingConditions.length > 0
              ? t("nodeMenu.addWithOperator", { operator: t(`nodeMenu.${conditionOperator}`) })
              : t("nodeMenu.firstPrerequisite")}
          </small>
        </span>
        <span className="condition-primary-arrow" aria-hidden>→</span>
      </button>
      <div className="lane-context-help">
        {t("nodeMenu.help")}
      </div>
        </>
      )}
    </>
  );
}
