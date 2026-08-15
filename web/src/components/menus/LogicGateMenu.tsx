/** Standalone logic-gate context menu [B-06]: operator, inputs, output. */

import {
  logicGateIsComplete,
  logicGateOutputs,
  logicGateRelation,
} from "../../domain";
import type { Graph, LogicGate, LogicGateOperator } from "../../types";
import { NEW_GATE_OPERATORS } from "./GraphMenu";
import { useI18n } from "../../i18n";

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
  const { t } = useI18n();
  return (
    <>
      {editedLogicGate ? (
      <>
        <div className="lane-context-title condition-menu-title">
          <span>{t("logicGate.edit")}</span>
          <small>{editedLogicGate.id}</small>
        </div>
        <div
          className={`standalone-gate-state${
            logicGateIsComplete(editedLogicGate) ? " complete" : " incomplete"
          }`}
        >
          <b>
            {logicGateIsComplete(editedLogicGate)
              ? t("logicGate.complete")
              : t("logicGate.incomplete")}
          </b>
          <small>
            {editedLogicGate.operator === "must"
              ? t("logicGate.mustRequirement")
              : logicGateRelation(editedLogicGate.operator)
                ? t("logicGate.relationRequirement", { operator: t(`logicGate.op.${editedLogicGate.operator}`) })
                : t("logicGate.binaryRequirement", { operator: t(`logicGate.op.${editedLogicGate.operator}`) })}
          </small>
        </div>
        <label className="lane-context-field">
          <span className="lane-context-label">{t("logicGate.mode")}</span>
          <select
            value={editedLogicGate.operator}
            onChange={(event) =>
              setStandaloneLogicGateOperator(
                editedLogicGate.id,
                event.target.value as LogicGateOperator,
              )
            }
          >
            {NEW_GATE_OPERATORS.map(({ operator }) => (
              <option key={operator} value={operator}>
                {t(`logicGate.op.${operator}`)} · {t(`graphMenu.${operator}Hint`)}
              </option>
            ))}
          </select>
        </label>
        <label className="lane-context-field">
          <span className="lane-context-label">{t("logicGate.output")}</span>
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
                        <small>{owner ? t("logicGate.controlledBy", { id: owner.id }) : node.id}</small>
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
              <option value="">{t("logicGate.unassigned")}</option>
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
                      {owner ? ` (${t("logicGate.controlledBy", { id: owner.id })})` : ""}
                    </option>
                  );
                })}
            </select>
          )}
          <small className="lane-context-field-help">
            {logicGateRelation(editedLogicGate.operator)
              ? t("logicGate.relationHelp", { operator: t(`logicGate.op.${editedLogicGate.operator}`) })
              : t("logicGate.outputHelp")}
          </small>
        </label>
        <div className="standalone-gate-inputs">
          <span className="lane-context-label">{t("logicGate.input")}</span>
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
          {t("logicGate.help")}
        </div>
        <button
          type="button"
          className="danger standalone-gate-delete"
          onClick={() => deleteStandaloneLogicGate(editedLogicGate.id)}
        >
          {t("logicGate.delete")}
        </button>
      </>
    ) : (
      <div className="lane-context-help">{t("logicGate.missing")}</div>
    )}
    </>
  );
}
