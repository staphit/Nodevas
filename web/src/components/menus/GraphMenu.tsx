/** Canvas context menu [B-06]: what can be created at this spot. */

import type { LogicGateOperator } from "../../types";
import type { LaneContextMenu } from "../LaneView";

export const NEW_GATE_OPERATORS: { operator: LogicGateOperator; label: string; hint: string }[] = [
  { operator: "must", label: "MUST", hint: "必須 · 1 個輸入" },
  { operator: "and", label: "AND", hint: "全部成立 · 2+ 輸入" },
  { operator: "or", label: "OR", hint: "任一成立 · 2+ 輸入" },
  { operator: "nand", label: "NAND", hint: "非全部 · 2+ 輸入" },
  { operator: "nor", label: "NOR", hint: "皆不成立 · 2+ 輸入" },
  { operator: "optional", label: "選用", hint: "不阻擋 · 1+ 輸入" },
  { operator: "deprecated", label: "棄用", hint: "不算前置 · 1+ 輸入" },
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
  return (
    <>
      <div className="lane-context-title">關係圖</div>
      <button type="button" role="menuitem" onClick={() => openNodeCreationMenu(contextMenu)}>
        新增節點
      </button>
      <button type="button" role="menuitem" onClick={() => createCanvasGroup(contextMenu)}>
        新增群組區域
      </button>
      <button type="button" role="menuitem" onClick={() => createCanvasAnnotation(contextMenu)}>
        新增畫布註解
      </button>
      <div className="lane-context-title logic-gate-create-title">新增邏輯閘</div>
      <div className="logic-gate-create-grid">
        {NEW_GATE_OPERATORS.map(({ operator, label, hint }) => (
          <button
            key={operator}
            type="button"
            role="menuitem"
            onClick={() => createStandaloneLogicGate(contextMenu, operator)}
          >
            <b>{label}</b>
            <span>{hint}</span>
          </button>
        ))}
      </div>
      <div className="lane-context-help">
        建立後可直接選擇輸入與輸出節點，也可用 Alt＋拖曳接線。未接完整會反紅。
      </div>
    </>
  );
}
