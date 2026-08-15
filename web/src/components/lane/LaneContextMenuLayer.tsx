/**
 * The board's right-click menu: one floating panel, six bodies.
 *
 * Which body is shown follows the menu kind. The panel measures itself to stay
 * on screen, and the node form — the one big enough to cover what it is about
 * to create — can be dragged out of the way by its heading.
 */

import { useEffect, useMemo, useState } from "react";
import { useFloatingPanel } from "../floatingPanel";
import type { Graph, LogicGate, PlanStatusDefinition } from "../../types";
import type { useGraphWiring } from "../canvas/useGraphWiring";
import type { useNodeConditions } from "../canvas/useNodeConditions";
import type { useNodeCreation } from "../canvas/useNodeCreation";
import type { usePlanMenu } from "../timeline/usePlanMenu";
import { EdgeMenu, type EdgeMenuProps } from "../menus/EdgeMenu";
import { GraphMenu, type GraphMenuProps } from "../menus/GraphMenu";
import { LogicGateMenu } from "../menus/LogicGateMenu";
import { NodeCreateMenu } from "../menus/NodeCreateMenu";
import { NodeMenu, type NodeMenuProps } from "../menus/NodeMenu";
import { PlanMenu } from "../menus/PlanMenu";
import type { LaneContextMenu } from "../LaneView";

/**
 * The open menu, and the two ways of dismissing one: pressing Escape or
 * clicking anywhere outside it.
 */
export function useLaneContextMenu() {
  const [contextMenu, setContextMenu] = useState<LaneContextMenu | null>(null);

  useEffect(() => {
    if (!contextMenu) return;
    const close = () => setContextMenu(null);
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") close();
    };
    window.addEventListener("pointerdown", close);
    window.addEventListener("keydown", onKeyDown);
    return () => {
      window.removeEventListener("pointerdown", close);
      window.removeEventListener("keydown", onKeyDown);
    };
  }, [contextMenu]);

  return { contextMenu, setContextMenu };
}

export interface LaneContextMenuLayerProps {
  contextMenu: LaneContextMenu;
  setContextMenu: (menu: LaneContextMenu | null) => void;
  graph: Graph | null;
  nodeTitle: (id: string | undefined) => string;
  logicGates: LogicGate[];
  planStatusDefinitions: PlanStatusDefinition[];
  openTab: NodeMenuProps["openTab"];
  selectNode: (id: string) => void;
  duplicateNode: NodeMenuProps["duplicateNode"];
  deleteNode: NodeMenuProps["deleteNode"];
  openNodeCreationMenu: GraphMenuProps["openNodeCreationMenu"];
  createCanvasGroup: GraphMenuProps["createCanvasGroup"];
  createCanvasAnnotation: GraphMenuProps["createCanvasAnnotation"];
  setEdgeStyles: EdgeMenuProps["setEdgeStyles"];
  wiring: ReturnType<typeof useGraphWiring>;
  conditions: ReturnType<typeof useNodeConditions>;
  nodeCreation: ReturnType<typeof useNodeCreation>;
  planMenu: ReturnType<typeof usePlanMenu>;
}

export function LaneContextMenuLayer({
  contextMenu,
  setContextMenu,
  graph,
  nodeTitle,
  logicGates,
  planStatusDefinitions,
  openTab,
  selectNode,
  duplicateNode,
  deleteNode,
  openNodeCreationMenu,
  createCanvasGroup,
  createCanvasAnnotation,
  setEdgeStyles,
  wiring,
  conditions,
  nodeCreation,
  planMenu,
}: LaneContextMenuLayerProps) {
  // Below the phone breakpoint there is no room to place a form beside where it
  // was opened from: the menu is nearly as wide as the screen and the tallest of
  // them is taller than it, so there is no placement that both fits and stays
  // near the pointer. It goes to the top-left corner instead and scrolls inside
  // its own max-height, which is what a full-width sheet does anyway.
  const narrow = window.innerWidth <= 720;
  const anchor = useMemo(
    () => (narrow ? { left: 8, top: 8 } : { left: contextMenu.x, top: contextMenu.y }),
    [narrow, contextMenu.x, contextMenu.y],
  );
  const { ref, placement, dragging, dragHandleProps } =
    useFloatingPanel<HTMLDivElement>(anchor);
  return (
    <div
      ref={ref}
      className={`lane-context-menu${
        contextMenu.kind === "node-create" ? " node-create-menu" : ""
      }${contextMenu.kind === "node" ? " condition-menu" : ""}${
        contextMenu.kind === "logic-gate" ? " standalone-gate-menu" : ""
      }${dragging ? " dragging" : ""}`}
      role="menu"
      style={placement}
      onPointerDown={(event) => event.stopPropagation()}
    >
      {contextMenu.kind === "graph" ? (
        <GraphMenu
          contextMenu={contextMenu}
          openNodeCreationMenu={openNodeCreationMenu}
          createCanvasGroup={createCanvasGroup}
          createCanvasAnnotation={createCanvasAnnotation}
          createStandaloneLogicGate={conditions.createStandaloneLogicGate}
        />
      ) : contextMenu.kind === "node-create" ? (
        <NodeCreateMenu
          {...nodeCreation}
          contextMenu={contextMenu}
          planStatusDefinitions={planStatusDefinitions}
          customStatuses={graph?.ui?.customStatuses ?? []}
          setContextMenu={setContextMenu}
          dragHandleProps={dragHandleProps}
        />
      ) : contextMenu.kind === "edge" ? (
        <EdgeMenu
          contextMenu={contextMenu}
          graph={graph}
          nodeTitle={nodeTitle}
          setEdgeStyles={setEdgeStyles}
          convertEdgesToLogicGate={conditions.convertEdgesToLogicGate}
          setContextMenu={setContextMenu}
        />
      ) : contextMenu.kind === "logic-gate" ? (
        <LogicGateMenu
          graph={graph}
          editedLogicGate={conditions.editedLogicGate}
          logicGates={logicGates}
          setStandaloneLogicGateOperator={wiring.setStandaloneLogicGateOperator}
          toggleLogicGateInput={wiring.toggleLogicGateInput}
          setLogicGateOutput={wiring.setLogicGateOutput}
          toggleLogicGateOutput={wiring.toggleLogicGateOutput}
          deleteStandaloneLogicGate={wiring.deleteStandaloneLogicGate}
        />
      ) : contextMenu.kind === "node" ? (
        <NodeMenu
          {...conditions}
          contextMenu={contextMenu}
          nodeTitle={nodeTitle}
          assignExistingLogicGateToNode={wiring.assignExistingLogicGateToNode}
          openTab={openTab}
          selectNode={selectNode}
          duplicateNode={duplicateNode}
          deleteNode={deleteNode}
          setContextMenu={setContextMenu}
        />
      ) : (
        <PlanMenu
          {...planMenu}
          contextMenu={contextMenu}
          graph={graph}
          nodeTitle={nodeTitle}
          planStatusDefinitions={planStatusDefinitions}
        />
      )}
    </div>
  );
}
