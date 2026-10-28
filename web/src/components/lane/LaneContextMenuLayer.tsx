/**
 * The board's right-click menu: one floating panel, seven bodies.
 *
 * Which body is shown follows the menu kind. The panel measures itself to stay
 * on screen, and the node form — the one big enough to cover what it is about
 * to create — can be dragged out of the way by its heading.
 */

import { useEffect, useMemo, useState } from "react";
import { useFloatingPanel } from "../floatingPanel";
import { useI18n } from "../../i18n";
import type { Graph, GraphEdge, LogicGate, PlanStatusDefinition } from "../../types";
import type { useGraphWiring } from "../canvas/useGraphWiring";
import type { useNodeConditions } from "../canvas/useNodeConditions";
import type { useNodeCreation } from "../canvas/useNodeCreation";
import type { usePlanMenu } from "../timeline/usePlanMenu";
import { EdgeMenu, type EdgeMenuProps } from "../menus/EdgeMenu";
import { GraphMenu, type GraphMenuProps } from "../menus/GraphMenu";
import { LogicGateMenu } from "../menus/LogicGateMenu";
import { NodeCreateMenu } from "../menus/NodeCreateMenu";
import { NodeActionsMenu, type NodeActionsMenuProps } from "../menus/NodeActionsMenu";
import { NodeMenu } from "../menus/NodeMenu";
import { PlanMenu } from "../menus/PlanMenu";
import type { LaneContextMenu } from "../LaneView";

const NO_EDGES: readonly GraphEdge[] = [];

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
      if (event.key !== "Escape") return;
      // Claimed in the capture phase, so the drawer and the board below do not
      // also read it as theirs: one Escape closes one thing.
      event.stopPropagation();
      close();
    };
    window.addEventListener("pointerdown", close);
    document.addEventListener("keydown", onKeyDown, true);
    return () => {
      window.removeEventListener("pointerdown", close);
      document.removeEventListener("keydown", onKeyDown, true);
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
  openTab: NodeActionsMenuProps["openTab"];
  selectNode: (id: string) => void;
  duplicateNode: NodeActionsMenuProps["duplicateNode"];
  deleteNode: NodeActionsMenuProps["deleteNode"];
  createGroupFromSelection: NodeActionsMenuProps["createGroupFromSelection"];
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
  createGroupFromSelection,
  openNodeCreationMenu,
  createCanvasGroup,
  createCanvasAnnotation,
  setEdgeStyles,
  wiring,
  conditions,
  nodeCreation,
  planMenu,
}: LaneContextMenuLayerProps) {
  const { t } = useI18n();
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
        contextMenu.kind === "node-actions" ? " node-actions-menu" : ""
      }${
        contextMenu.kind === "logic-gate" ? " standalone-gate-menu" : ""
      }${dragging ? " dragging" : ""}`}
      role="menu"
      // The node's menu is named after its node; the others carry their own
      // heading, read as part of the menu.
      aria-label={
        contextMenu.kind === "node-actions"
          ? t("board.nodeMenu.label", { title: nodeTitle(contextMenu.nodeId) })
          : undefined
      }
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
          key={contextMenu.edges.map((edge) => `${edge.from}->${edge.to}`).join(",")}
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
      ) : contextMenu.kind === "node-actions" ? (
        <NodeActionsMenu
          key={contextMenu.nodeId}
          contextMenu={contextMenu}
          graph={graph}
          nodeTitle={nodeTitle}
          openTab={openTab}
          selectNode={selectNode}
          duplicateNode={duplicateNode}
          deleteNode={deleteNode}
          createGroupFromSelection={createGroupFromSelection}
          setContextMenu={setContextMenu}
        />
      ) : contextMenu.kind === "node" ? (
        <NodeMenu
          // A fresh menu per node, so "Advanced conditions" opens or closes
          // for what this node holds, not for the node edited before it.
          key={contextMenu.nodeId}
          {...conditions}
          contextMenu={contextMenu}
          nodeTitle={nodeTitle}
          edges={graph?.edges ?? NO_EDGES}
          assignExistingLogicGateToNode={wiring.assignExistingLogicGateToNode}
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
