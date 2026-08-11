/**
 * Node context menu [B-06].
 *
 * Everything about one node's dependencies: which conditions feed it, how they
 * combine, whether a first-class logic gate owns them, plus open/duplicate/
 * delete.
 */

import type { EdgeRelation, GraphEdge, GraphNode, LogicGate } from "../../types";
import { RELATION_LABELS, edgeRelation } from "../../domain/graph/edgeStyle";
import { IconPlus } from "../../icons";
import { confirmAction } from "../ConfirmDialog";
import { OP_PRESENTATION } from "../canvas/GateWiring";
import type { LaneContextMenu } from "../LaneView";

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
  return (
    <>
      <div className="lane-context-title condition-menu-title">
        <span>設定進入條件</span>
        <small>{nodeTitle(contextMenu.nodeId)}</small>
      </div>
      <div className="condition-menu-intro">
        指定哪些節點要先成立，以及有多項條件時如何判定。
      </div>
      <div className="condition-menu-node-actions">
        <button
          type="button"
          onClick={() => {
            setContextMenu(null);
            void openTab(contextMenu.nodeId).catch(reportError);
          }}
        >
          開啟資訊
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
          建立副本
        </button>
        <button
          type="button"
          className="danger"
          onClick={() => {
            const id = contextMenu.nodeId;
            setContextMenu(null);
            void confirmAction({
              title: `移到垃圾桶？`,
              description: `${nodeTitle(id)} 將可從專案總管的垃圾桶還原。`,
              confirmLabel: "移到垃圾桶",
              tone: "danger",
            }).then((confirmed) => {
              if (confirmed) return deleteNode(id);
            }).catch(reportError);
          }}
        >
          移到垃圾桶
        </button>
      </div>
      {assignableLogicGates.length > 0 && (
        <div className="assign-existing-gate">
          <div>
            <b>使用既有邏輯閘</b>
            <small>先建立閘門，再把它指派給此節點。</small>
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
            <option value="">不使用獨立邏輯閘</option>
            {assignableLogicGates.map((gate) => (
              <option key={gate.id} value={gate.id}>
                {gate.operator.toUpperCase()} · {gate.id} · 輸入 {gate.inputs.length}
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
              編輯此邏輯閘
            </button>
          )}
        </div>
      )}
      {controllingLogicGate ? (
        <div className="controlled-by-standalone-gate">
          <b>前置條件由獨立邏輯閘管理</b>
          <span>
            {controllingLogicGate.operator.toUpperCase()} ·{" "}
            {controllingLogicGate.id}
          </span>
          <small>請用「編輯此邏輯閘」調整輸入節點與判定方式。</small>
        </div>
      ) : (
        <>
      {incomingConditions.length > 0 && (
        <div className="lane-context-summary">
          <b>目前的前置節點</b>
          <p>「選用」不阻擋此節點；「棄用」連前置關係都不算，只留下線。</p>
          {incomingConditions.map((edge) => (
            <label
              className="lane-context-relation"
              key={`${edge.from}->${edge.to}`}
            >
              <span>{nodeTitle(edge.from)}</span>
              <select
                value={edgeRelation(edge)}
                aria-label={`${nodeTitle(edge.from)} 的關係`}
                onChange={(event) =>
                  setDependencyRelation(
                    contextMenu.nodeId,
                    edge.from,
                    event.target.value as EdgeRelation,
                  )
                }
              >
                {(Object.keys(RELATION_LABELS) as EdgeRelation[]).map((relation) => (
                  <option key={relation} value={relation}>
                    {RELATION_LABELS[relation]}
                  </option>
                ))}
              </select>
            </label>
          ))}
        </div>
      )}
      {(hasEditableGateOperator || incomingConditions.length > 0) && (
        <label className="lane-context-field">
          <span className="lane-context-label">多項條件的判定方式</span>
          <select
            value={conditionOperator}
            onChange={(event) =>
              changeGateOperator(contextMenu.nodeId, event.target.value)
            }
          >
            <option value="and">全部條件成立（AND）</option>
            <option value="or">至少一項成立（OR）</option>
            <option value="xor">恰好一項成立（XOR）</option>
            <option value="nand">不可全部成立（NAND）</option>
            <option value="nor">所有條件都不成立（NOR）</option>
          </select>
          <small className="lane-context-field-help">
            {OP_PRESENTATION[conditionOperator]?.description}
          </small>
        </label>
      )}
      <label className="lane-context-field">
        <span className="lane-context-label">要加入的前置節點</span>
        <select
          value={conditionSource}
          disabled={availableConditionSources.length === 0}
          onChange={(event) => setConditionSource(event.target.value)}
        >
          {availableConditionSources.length === 0 ? (
            <option value="">沒有可新增的節點</option>
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
        <span className="lane-context-label">新關係的語意</span>
        <select
          value={conditionRelation}
          onChange={(event) =>
            setConditionRelation(event.target.value as EdgeRelation)
          }
        >
          <option value="">必要（阻擋進行）</option>
          <option value="optional">選用（不阻擋）</option>
          <option value="deprecated">棄用（不算前置）</option>
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
          <b>加入前置節點</b>
          <small>
            {incomingConditions.length > 0
              ? `依「${OP_PRESENTATION[conditionOperator]?.label ?? conditionOperator.toUpperCase()}」判定`
              : "建立第一條前置關係"}
          </small>
        </span>
        <span className="condition-primary-arrow" aria-hidden>→</span>
      </button>
      <div className="lane-context-help">
        加入後，可點擊關係圖上的邏輯閘再次調整。
      </div>
        </>
      )}
    </>
  );
}
