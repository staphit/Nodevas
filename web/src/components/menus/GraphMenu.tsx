/** Canvas context menu [B-06]: what can be created at this spot. */

import type { LogicGateOperator } from "../../types";
import type { LaneContextMenu } from "../LaneView";
import { useI18n } from "../../i18n";

export const NEW_GATE_OPERATORS: { operator: LogicGateOperator }[] = [
  { operator: "must" },
  { operator: "and" },
  { operator: "or" },
  { operator: "nand" },
  { operator: "nor" },
  { operator: "optional" },
  { operator: "deprecated" },
];

export interface GraphMenuProps {
  contextMenu: Extract<LaneContextMenu, { kind: "graph" }>;
  openNodeCreationMenu: (menu: Extract<LaneContextMenu, { kind: "graph" }>) => void;
  createCanvasGroup: (menu: Extract<LaneContextMenu, { kind: "graph" }>) => void;
  createCanvasAnnotation: (menu: Extract<LaneContextMenu, { kind: "graph" }>) => void;
  createStandaloneLogicGate: (
    menu: Extract<LaneContextMenu, { kind: "graph" }>,
    operator: LogicGateOperator,
  ) => void;
}

export function GraphMenu({
  contextMenu,
  openNodeCreationMenu,
  createCanvasGroup,
  createCanvasAnnotation,
  createStandaloneLogicGate,
}: GraphMenuProps) {
  const { t } = useI18n();
  const operatorHint: Record<LogicGateOperator, string> = {
    must: t("graphMenu.mustHint"),
    and: t("graphMenu.andHint"),
    or: t("graphMenu.orHint"),
    xor: t("graphMenu.xorHint"),
    nand: t("graphMenu.nandHint"),
    nor: t("graphMenu.norHint"),
    optional: t("graphMenu.optionalHint"),
    deprecated: t("graphMenu.deprecatedHint"),
  };
  return (
    <>
      <div className="lane-context-title">{t("graphMenu.title")}</div>
      <button type="button" role="menuitem" onClick={() => openNodeCreationMenu(contextMenu)}>
        {t("graphMenu.newNode")}
      </button>
      <button type="button" role="menuitem" onClick={() => createCanvasGroup(contextMenu)}>
        {t("graphMenu.newGroup")}
      </button>
      <button type="button" role="menuitem" onClick={() => createCanvasAnnotation(contextMenu)}>
        {t("graphMenu.newAnnotation")}
      </button>
      <div className="lane-context-title logic-gate-create-title">{t("graphMenu.newGate")}</div>
      <div className="logic-gate-create-grid">
        {NEW_GATE_OPERATORS.map(({ operator }) => (
          <button
            key={operator}
            type="button"
            role="menuitem"
            onClick={() => createStandaloneLogicGate(contextMenu, operator)}
          >
            <b>{operator.toUpperCase()}</b>
            <span>{operatorHint[operator]}</span>
          </button>
        ))}
      </div>
      <div className="lane-context-help">
        {t("graphMenu.help")}
      </div>
    </>
  );
}
