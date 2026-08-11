/** Standalone logic-gate context menu [B-06]: operator, inputs, output. */

import {
  logicGateIsComplete,
  logicGateOutputs,
  logicGateRelation,
} from "../../domain";
import type { Graph, LogicGate, LogicGateOperator } from "../../types";
import { LOGIC_GATE_LABELS } from "../canvas/LogicGate";
import { NEW_GATE_OPERATORS } from "./GraphMenu";

export interface LogicGateMenuProps {
  graph: Graph | null;
  editedLogicGate: LogicGate | undefined;
  logicGates: LogicGate[];
  setStandaloneLogicGateOperator: (gateId: string, operator: LogicGateOperator) => void;
  toggleLogicGateInput: (gateId: string, nodeId: string, enabled: boolean) => void;
  setLogicGateOutput: (gateId: string, targetId: string) => void;
  toggleLogicGateOutput: (gateId: string, targetId: string, enabled: boolean) => void;
  deleteStandaloneLogicGate: (gateId: string) => void;
}

export function LogicGateMenu({
  graph,
  editedLogicGate,
  logicGates,
  setStandaloneLogicGateOperator,
  toggleLogicGateInput,
  setLogicGateOutput,
  toggleLogicGateOutput,
  deleteStandaloneLogicGate,
}: LogicGateMenuProps) {
  return (
    <>
      {editedLogicGate ? (
      <>
        <div className="lane-context-title condition-menu-title">
          <span>編輯邏輯閘</span>
          <small>{editedLogicGate.id}</small>
        </div>
        <div
          className={`standalone-gate-state${
            logicGateIsComplete(editedLogicGate) ? " complete" : " incomplete"
          }`}
        >
          <b>
            {logicGateIsComplete(editedLogicGate)
              ? "條件已生效"
              : "接線尚未完成"}
          </b>
          <small>
            {editedLogicGate.operator === "must"
              ? "MUST 需要 1 個輸入與 1 個輸出"
              : logicGateRelation(editedLogicGate.operator)
                ? `${LOGIC_GATE_LABELS[editedLogicGate.operator]}需要至少 1 個輸入與 1 個輸出`
                : `${editedLogicGate.operator.toUpperCase()} 需要至少 2 個輸入與 1 個輸出`}
          </small>
        </div>
        <label className="lane-context-field">
          <span className="lane-context-label">判定方式</span>
          <select
            value={editedLogicGate.operator}
            onChange={(event) =>
              setStandaloneLogicGateOperator(
                editedLogicGate.id,
                event.target.value as LogicGateOperator,
              )
            }
          >
            {NEW_GATE_OPERATORS.map(({ operator, label, hint }) => (
              <option key={operator} value={operator}>
                {label} · {hint}
              </option>
            ))}
          </select>
        </label>
        <label className="lane-context-field">
          <span className="lane-context-label">輸出節點</span>
          {logicGateRelation(editedLogicGate.operator) ? (
            // A relation gate is many-to-many, so its outputs are picked the
            // same way its inputs are.
            <div className="standalone-gate-inputs">
              <div>
                {(graph?.nodes ?? [])
                  .filter((node) => !editedLogicGate.inputs.includes(node.id))
                  .map((node) => {
                    const owner = logicGates.find(
                      (gate) =>
                        gate.id !== editedLogicGate.id &&
                        logicGateOutputs(gate).includes(node.id),
                    );
                    return (
                      <label key={node.id}>
                        <input
                          type="checkbox"
                          checked={logicGateOutputs(editedLogicGate).includes(node.id)}
                          disabled={Boolean(owner)}
                          onChange={(event) =>
                            toggleLogicGateOutput(
                              editedLogicGate.id,
                              node.id,
                              event.target.checked,
                            )
                          }
                        />
                        <span>{node.title || node.id}</span>
                        <small>{owner ? `已由 ${owner.id} 控制` : node.id}</small>
                      </label>
                    );
                  })}
              </div>
            </div>
          ) : (
            <select
              value={editedLogicGate.output ?? ""}
              onChange={(event) =>
                setLogicGateOutput(editedLogicGate.id, event.target.value)
              }
            >
              <option value="">尚未指派</option>
              {(graph?.nodes ?? [])
                .filter((node) => !editedLogicGate.inputs.includes(node.id))
                .map((node) => {
                  const owner = logicGates.find(
                    (gate) =>
                      gate.id !== editedLogicGate.id &&
                      logicGateOutputs(gate).includes(node.id),
                  );
                  return (
                    <option key={node.id} value={node.id} disabled={Boolean(owner)}>
                      {node.title || node.id}
                      {owner ? `（已由 ${owner.id} 控制）` : ""}
                    </option>
                  );
                })}
            </select>
          )}
          <small className="lane-context-field-help">
            {logicGateRelation(editedLogicGate.operator)
              ? `指派後，所有輸入到該節點的連線都會標記為「${
                  LOGIC_GATE_LABELS[editedLogicGate.operator]
                }」，由此閘統一管理。`
              : "指派後，此邏輯閘會接管該節點的必要前置條件，不會再產生第二個舊式閘門。"}
          </small>
        </label>
        <div className="standalone-gate-inputs">
          <span className="lane-context-label">輸入節點</span>
          <div>
            {(graph?.nodes ?? [])
              .filter((node) => node.id !== editedLogicGate.output)
              .map((node) => {
                const checked = editedLogicGate.inputs.includes(node.id);
                const mustFull =
                  editedLogicGate.operator === "must" &&
                  editedLogicGate.inputs.length >= 1 &&
                  !checked;
                return (
                  <label key={node.id}>
                    <input
                      type="checkbox"
                      checked={checked}
                      disabled={mustFull}
                      onChange={(event) =>
                        toggleLogicGateInput(
                          editedLogicGate.id,
                          node.id,
                          event.target.checked,
                        )
                      }
                    />
                    <span>{node.title || node.id}</span>
                    <small>{node.id}</small>
                  </label>
                );
              })}
          </div>
        </div>
        <div className="lane-context-help">
          也可用 Alt＋拖曳節點接到閘門；Alt＋拖曳閘門接到輸出節點。
        </div>
        <button
          type="button"
          className="danger standalone-gate-delete"
          onClick={() => deleteStandaloneLogicGate(editedLogicGate.id)}
        >
          刪除邏輯閘
        </button>
      </>
    ) : (
      <div className="lane-context-help">邏輯閘已不存在。</div>
    )}
    </>
  );
}
