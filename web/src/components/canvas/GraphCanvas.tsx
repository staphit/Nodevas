/**
 * The relationship graph's canvas layer.
 *
 * Everything the board draws when the pane is in graph mode: canvas
 * decorations, dependency wiring, logic gates and the node cards with their
 * resize handles. It owns no state — LaneView keeps the interaction hooks and
 * hands their state and handlers down, so what is drawn is decided in one
 * place and how it reacts in another.
 */

import { Fragment } from "react";
import { reportError } from "../../store";
import { StatusShape, statusTheme } from "../../statusTheme";
import { localizedStatusLabel, useI18n } from "../../i18n";
import { kindIcon } from "../../icons";
import type {
  CanvasAnnotation,
  CanvasGroup,
  Graph,
  GraphNode,
  LogicGate,
  Status,
  StatusDefinition,
} from "../../types";
import type { GraphAnalysis } from "../../analysis";
import { logicGateOutputs } from "../../domain";
import type { NodeCommand } from "../../domain/commands";
import type { CommandResult } from "../../state/operations";
import { CanvasDecoration } from "./CanvasDecoration";
import { SnapGuides } from "./SnapGuides";
import type { SnapBox, SnapGuide } from "./snapping";

/** Somebody else's selection, mid-gesture. */
export interface PeerGhost {
  peerId: string;
  color: string;
  name: string;
  ids: string[];
  dx: number;
  dy: number;
}

/** Where somebody else's pointer is, in board coordinates. */
export interface PeerPointer {
  peerId: string;
  color: string;
  name: string;
  point: Point;
}
import { GateWiring, type Gate } from "./GateWiring";
import { StandaloneLogicGate } from "./LogicGate";
import type { useCardDrag, CardDragState } from "./useCardDrag";
import type { useCardResize, CardResizeState } from "./useCardResize";
import type { useConnectionDrag, ConnectionDragState } from "./useConnectionDrag";

import {
  CARD_HANDLES,
  CARD_MAX_H,
  CARD_MAX_W,
  CARD_MIN_H,
  CARD_MIN_W,
  ROW_H,
  edgePlacementKey,
  type Col,
  type Point,
  type Rect,
} from "./geometry";
import type { GraphSelection, LaneContextMenu } from "../LaneView";

type ConnectionHandlers = ReturnType<typeof useConnectionDrag>["handlers"];
type CardResizeHandlers = ReturnType<typeof useCardResize>["handlers"];
type CardDragHandlers = ReturnType<typeof useCardDrag>["handlers"];

export interface GraphCanvasProps {
  graph: Graph | null;
  /** Every node placed on the board, in draw order. */
  cols: Col[];
  colOf: Map<string, Col>;
  gates: Gate[];
  logicGates: LogicGate[];
  statuses: Record<string, Status>;
  customStatuses: StatusDefinition[];
  userNames: Map<string, string>;
  activeTab: string | null;
  errorNodes: Set<string>;
  violationNodeIDs: Set<string>;
  analysis: GraphAnalysis;
  matchesViewFilter: (node: GraphNode) => boolean;

  // Geometry, all in board coordinates.
  boardW: number;
  svgH: number;
  zoom: number;
  graphOffsetX: number;
  graphOffsetY: number;
  graphObstacles: Rect[];
  centerX: (column: Col) => number;
  rowTop: (row: number) => number;

  // Alignment. The boxes are shared by every gesture that can snap; the guides
  // are whatever the gesture currently running has landed on.
  snapEnabled: boolean;
  snapBoxes: readonly SnapBox[];
  snapGuides: SnapGuide[];
  onSnapGuides: (guides: SnapGuide[]) => void;

  /** Cards other people are dragging right now, in their own colour. */
  peerGhosts: PeerGhost[];
  /** Where other people's pointers are. */
  peerPointers: PeerPointer[];

  // Live gestures.
  drag: CardDragState | null;
  cardDragHandlers: CardDragHandlers;
  cardResize: CardResizeState | null;
  cardResizeHandlers: CardResizeHandlers;
  connectionDrag: ConnectionDragState | null;
  connectionHover: { nodeId: string; point: Point } | null;
  connectionHandlers: ConnectionHandlers;
  connectionPreviewEnd: Point | null | undefined;
  connectionPreviewError: string | null;
  updateConnectionDrag: ConnectionHandlers["onPointerMove"];
  finishConnectionDrag: ConnectionHandlers["onPointerUp"];
  startLogicGateConnection: ConnectionHandlers["startGate"];
  onCardPointerDown: (event: React.PointerEvent<HTMLElement>, id: string) => void;
  onCardPointerMove: (event: React.PointerEvent<HTMLElement>) => void;
  onCardPointerUp: (event: React.PointerEvent<HTMLElement>, id: string) => void;

  // Selection and menus.
  graphSelection: GraphSelection;
  setGraphSelection: (selection: GraphSelection) => void;
  selectedGraphNodeIDs: Set<string>;
  selectNode: (nodeId: string) => void;
  onSelectedNodeChange?: (id: string | null) => void;
  setContextMenu: (menu: LaneContextMenu | null) => void;
  openTab: (id: string) => Promise<unknown>;
  openConditionMenu: (nodeId: string, x: number, y: number) => void;

  // Persistence.
  updateCanvasDecoration: (
    kind: "group" | "annotation",
    id: string,
    patch: Partial<CanvasGroup & CanvasAnnotation>,
  ) => void;
  deleteCanvasDecoration: (kind: "group" | "annotation", id: string) => void;
  saveGatePlacement: (targetId: string, point: Point) => void;
  saveLogicGatePosition: (gateId: string, point: Point) => void;
  deleteStandaloneLogicGate: (gateId: string) => void;
  getWireVertices: (wireKey: string) => Point[];
  saveWireVertices: (wireKey: string, vertices: Point[]) => void;
  updateNode: (command: NodeCommand) => Promise<CommandResult>;
  setGraphNotice: (notice: { text: string; kind: "error" | "ok" } | null) => void;
}

export function GraphCanvas({
  graph,
  cols,
  colOf,
  gates,
  logicGates,
  statuses,
  customStatuses,
  userNames,
  activeTab,
  errorNodes,
  violationNodeIDs,
  analysis,
  matchesViewFilter,
  boardW,
  svgH,
  zoom,
  graphOffsetX,
  graphOffsetY,
  graphObstacles,
  centerX,
  rowTop,
  snapEnabled,
  snapBoxes,
  snapGuides,
  onSnapGuides,
  peerGhosts,
  peerPointers,
  drag,
  cardDragHandlers,
  cardResize,
  cardResizeHandlers,
  connectionDrag,
  connectionHover,
  connectionHandlers,
  connectionPreviewEnd,
  connectionPreviewError,
  updateConnectionDrag,
  finishConnectionDrag,
  startLogicGateConnection,
  onCardPointerDown,
  onCardPointerMove,
  onCardPointerUp,
  graphSelection,
  setGraphSelection,
  selectedGraphNodeIDs,
  selectNode,
  onSelectedNodeChange,
  setContextMenu,
  openTab,
  openConditionMenu,
  updateCanvasDecoration,
  deleteCanvasDecoration,
  saveGatePlacement,
  saveLogicGatePosition,
  deleteStandaloneLogicGate,
  getWireVertices,
  saveWireVertices,
  updateNode,
  setGraphNotice,
}: GraphCanvasProps) {
  const { t } = useI18n();
  // A marquee grabs cards, but a wire between two grabbed cards belongs to the
  // same selection: it is drawn selected so the picked set reads as one shape.
  const selectedEdgeKeys = new Set<string>();
  if (graphSelection?.kind === "edge") {
    selectedEdgeKeys.add(
      edgePlacementKey(graphSelection.from, graphSelection.to),
    );
  } else if (graphSelection?.kind === "edges") {
    for (const edge of graphSelection.edges) {
      selectedEdgeKeys.add(edgePlacementKey(edge.from, edge.to));
    }
  } else if (
    graphSelection?.kind === "vertex" &&
    !graphSelection.wireKey.startsWith("gate:")
  ) {
    selectedEdgeKeys.add(graphSelection.wireKey);
  }
  if (selectedGraphNodeIDs.size > 1) {
    for (const edge of graph?.edges ?? []) {
      if (
        selectedGraphNodeIDs.has(edge.from) &&
        selectedGraphNodeIDs.has(edge.to)
      ) {
        selectedEdgeKeys.add(edgePlacementKey(edge.from, edge.to));
      }
    }
  }

  return (
    <>
      {(graph?.ui?.groups ?? []).map((item) => (
        <CanvasDecoration
          key={item.id}
          kind="group"
          item={item}
          offsetX={graphOffsetX}
          offsetY={graphOffsetY}
          zoom={zoom}
          snapEnabled={snapEnabled}
          snapBoxes={snapBoxes}
          onSnapGuides={onSnapGuides}
          onChange={(patch) => updateCanvasDecoration("group", item.id, patch)}
          onDelete={() => deleteCanvasDecoration("group", item.id)}
        />
      ))}
      {(graph?.ui?.annotations ?? []).map((item) => (
        <CanvasDecoration
          key={item.id}
          kind="annotation"
          item={item}
          offsetX={graphOffsetX}
          offsetY={graphOffsetY}
          zoom={zoom}
          snapEnabled={snapEnabled}
          snapBoxes={snapBoxes}
          onSnapGuides={onSnapGuides}
          onChange={(patch) => updateCanvasDecoration("annotation", item.id, patch)}
          onDelete={() => deleteCanvasDecoration("annotation", item.id)}
        />
      ))}
      <svg className="board-edges" width={boardW} height={svgH} aria-hidden>
        <defs>
          <marker
            id="arrowhead"
            markerWidth="8"
            markerHeight="8"
            refX="7"
            refY="4"
            orient="auto"
          >
            <path d="M 0 0 L 8 4 L 0 8 z" className="arrowhead-fill" />
          </marker>
          {/* A deprecated wire is greyed end to end, arrowhead included. */}
          <marker
            id="arrowhead-muted"
            markerWidth="8"
            markerHeight="8"
            refX="7"
            refY="4"
            orient="auto"
          >
            <path d="M 0 0 L 8 4 L 0 8 z" className="arrowhead-fill muted" />
          </marker>
        </defs>
        {connectionHover && !connectionDrag && (
          <circle
            className="connection-port-preview"
            cx={connectionHover.point.x}
            cy={connectionHover.point.y}
            r={6}
          />
        )}
        {connectionDrag && connectionPreviewEnd && (
          <g
            className={`connection-draft${
              connectionPreviewError ? " invalid" : ""
            }`}
          >
            <line
              className="connection-draft-line"
              x1={connectionDrag.start.x}
              y1={connectionDrag.start.y}
              x2={connectionPreviewEnd.x}
              y2={connectionPreviewEnd.y}
              markerEnd="url(#arrowhead)"
            />
            <circle
              className="connection-port source"
              cx={connectionDrag.start.x}
              cy={connectionDrag.start.y}
              r={5}
            />
            <circle
              className="connection-port target"
              cx={connectionPreviewEnd.x}
              cy={connectionPreviewEnd.y}
              r={5}
            />
          </g>
        )}
        {logicGates.map((gate) => (
          <StandaloneLogicGate
            key={gate.id}
            gate={gate}
            center={{
              x: graphOffsetX + gate.x,
              y: graphOffsetY + gate.y,
            }}
            inputCols={gate.inputs.flatMap((id) => {
              const column = colOf.get(id);
              return column ? [column] : [];
            })}
            outputCols={logicGateOutputs(gate).flatMap((id) => {
              const column = colOf.get(id);
              return column ? [column] : [];
            })}
            centerX={centerX}
            rowTop={rowTop}
            selected={
              graphSelection?.kind === "logic-gate" && graphSelection.id === gate.id
            }
            connectionSource={connectionDrag?.sourceGateId === gate.id}
            connectionTarget={connectionDrag?.targetGateId === gate.id}
            connectionTargetInvalid={
              connectionDrag?.targetGateId === gate.id && Boolean(connectionPreviewError)
            }
            onSelect={() => {
              setGraphSelection({ kind: "logic-gate", id: gate.id });
              onSelectedNodeChange?.(null);
            }}
            onOpenMenu={(x, y) => {
              setGraphSelection({ kind: "logic-gate", id: gate.id });
              onSelectedNodeChange?.(null);
              setContextMenu({ kind: "logic-gate", gateId: gate.id, x, y });
            }}
            onDelete={() => deleteStandaloneLogicGate(gate.id)}
            onMove={(point) => saveLogicGatePosition(gate.id, point)}
            onStartConnection={(event, start) =>
              startLogicGateConnection(event, gate.id, start)
            }
            onConnectionMove={updateConnectionDrag}
            onConnectionEnd={finishConnectionDrag}
            getWireVertices={getWireVertices}
            onWireVerticesChange={saveWireVertices}
            selectedVertex={
              graphSelection?.kind === "vertex" ? graphSelection : null
            }
            onSelectVertex={(wireKey, index) => {
              setGraphSelection({ kind: "vertex", wireKey, index });
              onSelectedNodeChange?.(null);
            }}
            obstacles={graphObstacles}
          />
        ))}
        {gates.map((gate) => (
          <GateWiring
            key={gate.to.node.id}
            gate={gate}
            centerX={centerX}
            rowTop={rowTop}
            storedPoint={(() => {
              const placement = graph?.ui?.gates?.[gate.to.node.id];
              return Number.isFinite(placement?.x) && Number.isFinite(placement?.y)
                ? {
                    x: graphOffsetX + placement!.x!,
                    y: graphOffsetY + placement!.y!,
                  }
                : undefined;
            })()}
            storedRatio={graph?.ui?.gates?.[gate.to.node.id]?.ratio}
            onPositionChange={saveGatePlacement}
            selectedEdges={selectedEdgeKeys}
            onSelectEdge={(from, to) => {
              setGraphSelection({ kind: "edge", from, to });
              onSelectedNodeChange?.(null);
            }}
            onOpenEdgeMenu={(edges, x, y) => {
              const first = edges[0];
              if (!first) return;
              // Right-clicking one of several selected wires acts on the whole
              // selection, the way it does for cards.
              const inSelection =
                graphSelection?.kind === "edges" &&
                graphSelection.edges.some(
                  (edge) => edge.from === first.from && edge.to === first.to,
                );
              const targets =
                inSelection && graphSelection?.kind === "edges"
                  ? graphSelection.edges
                  : edges;
              if (!inSelection) {
                setGraphSelection({
                  kind: "edge",
                  from: first.from,
                  to: first.to,
                });
              }
              onSelectedNodeChange?.(null);
              setContextMenu({ kind: "edge", edges: targets, x, y });
            }}
            getWireVertices={getWireVertices}
            onWireVerticesChange={saveWireVertices}
            selectedVertex={
              graphSelection?.kind === "vertex" ? graphSelection : null
            }
            onSelectVertex={(wireKey, index) => {
              setGraphSelection({ kind: "vertex", wireKey, index });
              onSelectedNodeChange?.(null);
            }}
            obstacles={graphObstacles}
            onEditGate={(targetId, x, y) => openConditionMenu(targetId, x, y)}
            onDeleteGate={(targetId) => {
              void updateNode({
                type: "node.setRequires",
                nodeId: targetId,
                requires: "",
                // Keep the edges: only the condition expression goes.
                refs: null,
              }).then((result) => {
                if (!result.ok) {
                  reportError(new Error(result.message));
                  return;
                }
                setGraphNotice({
                  text: t("canvas.deleteGateNotice"),
                  kind: "ok",
                });
              });
            }}
          />
        ))}
      </svg>
      {cols.map((c) => {
        const n = c.node;
        const st: Status = statuses[n.id] ?? "ready";
        const assigned = (n.assignee && userNames.get(n.assignee)) || t("canvas.unassigned");
        const isDragging = drag?.ids.includes(n.id) === true && drag.moved;
        const isConnectionTarget = connectionDrag?.targetId === n.id;
        const nodeStyle = graph?.ui?.nodeStyles?.[n.id];
        const filteredOut = !matchesViewFilter(n);
        const resizing = cardResize?.nodeId === n.id ? cardResize : null;
        const liveWidth = resizing ? resizing.width : c.width;
        const liveHeight = resizing ? resizing.height : c.height;
        const connectionTargetError =
          isConnectionTarget && connectionDrag ? connectionPreviewError : null;
        return (
          <Fragment key={n.id}>
          <div
            data-node-id={n.id}
            role="button"
            tabIndex={0}
            className={`col-card status-${st}${errorNodes.has(n.id) ? " has-error" : ""}${
              activeTab === n.id ? " active" : ""
            }${selectedGraphNodeIDs.has(n.id) ? " selected" : ""}${
              isDragging ? " lifting" : ""
            }${filteredOut ? " filtered-out" : ""}${
              analysis.overdue.has(n.id) ? " analysis-overdue" : ""
            }${violationNodeIDs.has(n.id) ? " analysis-violation" : ""}${
              analysis.blocked.has(n.id) ? " analysis-blocked" : ""
            }${analysis.blocking.has(n.id) ? " analysis-blocking" : ""}${
              analysis.criticalNodeIds.has(n.id) ? " analysis-critical" : ""
            }${analysis.entryNodeIds.has(n.id) ? " is-entry" : ""}${
              analysis.deprecatedNodeIds.has(n.id) ? " is-deprecated" : ""
            }${
              connectionDrag?.sourceId === n.id ? " connection-source" : ""
            }${
              connectionHover?.nodeId === n.id ? " connection-hover" : ""
            }${
              isConnectionTarget
                ? connectionTargetError
                  ? " connection-target invalid"
                  : " connection-target valid"
                : ""
            }${nodeStyle?.shape ? ` shape-${nodeStyle.shape}` : ""}${
              nodeStyle?.align ? ` align-${nodeStyle.align}` : ""
            }${nodeStyle?.valign ? ` valign-${nodeStyle.valign}` : ""}`}
            style={{
              left: centerX(c) - liveWidth / 2,
              top: rowTop(c.row) + ROW_H / 2 - liveHeight / 2,
              width: liveWidth,
              height: liveHeight,
              // Shape insets are computed from these.
              ["--card-w" as string]: `${liveWidth}px`,
              ["--card-h" as string]: `${liveHeight}px`,
              // Fill and outline travel as variables: a clipped shape
              // draws its own edge and cannot use the box border.
              ["--card-bg" as string]: nodeStyle?.color,
              ["--card-border-color" as string]: nodeStyle?.borderColor,
              borderColor: nodeStyle?.borderColor,
              color: nodeStyle?.textColor,
              transform: isDragging
                ? `translate(${drag.dx}px, ${drag.dy}px)`
                : undefined,
            }}
            onPointerDown={(e) => onCardPointerDown(e, n.id)}
            onPointerMove={onCardPointerMove}
            onPointerUp={(event) => onCardPointerUp(event, n.id)}
            onPointerCancel={(event) => {
              if (!finishConnectionDrag(event, false)) cardDragHandlers.cancel();
            }}
            onPointerLeave={() => {
              if (connectionHover?.nodeId === n.id && !connectionDrag) {
                connectionHandlers.clearHover();
              }
            }}
            onFocus={() => {
              if (!selectedGraphNodeIDs.has(n.id)) selectNode(n.id);
            }}
            onKeyDown={(event) => {
              if (event.key !== "Enter") return;
              event.preventDefault();
              event.stopPropagation();
              void openTab(n.id).catch(reportError);
            }}
            onContextMenu={(event) => {
              event.preventDefault();
              event.stopPropagation();
              selectNode(n.id);
              setContextMenu({
                kind: "node",
                nodeId: n.id,
                x: event.clientX,
                y: event.clientY,
              });
            }}
            onLostPointerCapture={() => {
              cardDragHandlers.cancel();
              if (connectionDrag?.sourceId === n.id) connectionHandlers.cancel();
            }}
            aria-selected={selectedGraphNodeIDs.has(n.id)}
            data-blocked-label={analysis.blocked.has(n.id) ? t("canvas.blockedBadge") : undefined}
            data-blocking-label={analysis.blocking.has(n.id) ? t("canvas.blockingBadge") : undefined}
            title={`${n.title || n.id} · ${assigned}`}
          >
            <span className="col-card-head">
              {analysis.entryNodeIds.has(n.id) && (
                <span
                  className="entry-flag"
                  title={
                    typeof graph?.ui?.entryOverrides?.[n.id] === "boolean"
                      ? t("canvas.entry.manual")
                      : t("canvas.entry.auto")
                  }
                >
                  {t("canvas.entryLabel")}
                </span>
              )}
              <StatusShape status={st} definitions={customStatuses} />
              <span
                className="col-card-st"
                style={{ color: statusTheme(st, customStatuses).color }}
              >
                {localizedStatusLabel(st, customStatuses)}
              </span>
              <span className="lane-kind">{kindIcon(n.kind || "task", 11)}</span>
              {n.priority && <span className={`priority-dot ${n.priority}`} />}
              {errorNodes.has(n.id) && <span className="error-dot" />}
            </span>
            <span className="col-card-title">{n.title || n.id}</span>
            <span
              className={`col-card-assignee${n.assignee ? "" : " unassigned"}`}
              data-assignee-prefix={t("canvas.assigneePrefix")}
            >
              {assigned}
            </span>
            {!!n.tags?.length && (
              <span className="col-card-tags">{n.tags.slice(0, 2).join(" · ")}</span>
            )}
          </div>
          {selectedGraphNodeIDs.has(n.id) && (
            // Sibling of the card: a clipped shape would otherwise clip
            // its own handles away.
            <div
              className="card-resize-frame"
              style={{
                left: centerX(c) - liveWidth / 2,
                top: rowTop(c.row) + ROW_H / 2 - liveHeight / 2,
                width: liveWidth,
                height: liveHeight,
              }}
            >
              {CARD_HANDLES.map((handle) => (
                <span
                  key={handle.name}
                  className={`col-card-handle ${handle.name}`}
                  role="slider"
                  tabIndex={0}
                  aria-label={t("canvas.resizeCard", {
                    handle: t(`canvas.handle.${handle.name}`),
                    title: n.title || n.id,
                  })}
                  aria-valuemin={handle.dirX ? CARD_MIN_W : CARD_MIN_H}
                  aria-valuemax={handle.dirX ? CARD_MAX_W : CARD_MAX_H}
                  aria-valuenow={Math.round(handle.dirX ? liveWidth : liveHeight)}
                  aria-valuetext={`${Math.round(liveWidth)}×${Math.round(liveHeight)}`}
                  title={t("canvas.resizeCardTitle")}
                  onPointerDown={(event) =>
                    cardResizeHandlers.onPointerDown(
                      event,
                      n.id,
                      handle.dirX,
                      handle.dirY,
                      liveWidth,
                      liveHeight,
                    )
                  }
                  onPointerMove={cardResizeHandlers.onPointerMove}
                  onPointerUp={cardResizeHandlers.onPointerUp}
                  onPointerCancel={cardResizeHandlers.onPointerCancel}
                  onKeyDown={(event) =>
                    cardResizeHandlers.onKeyDown(
                      event,
                      n.id,
                      handle.dirX,
                      handle.dirY,
                      liveWidth,
                      liveHeight,
                    )
                  }
                />
              ))}
            </div>
          )}
          </Fragment>
        );
      })}
      <SnapGuides
        guides={snapGuides}
        offsetX={graphOffsetX}
        offsetY={graphOffsetY}
        zoom={zoom}
      />
      {/* An outline rather than a copy of the card: it says where the card is
          going without competing with the one that is still where it was. */}
      {peerGhosts.flatMap((ghost) =>
        ghost.ids.map((nodeId) => {
          const column = colOf.get(nodeId);
          if (!column) return null;
          return (
            <div
              key={`${ghost.peerId}-${nodeId}`}
              className="peer-ghost"
              aria-hidden
              title={ghost.name}
              style={{
                left: centerX(column) - column.width / 2 + ghost.dx,
                top: rowTop(column.row) + ROW_H / 2 - column.height / 2 + ghost.dy,
                width: column.width,
                height: column.height,
                ["--peer-color" as string]: ghost.color,
              }}
            />
          );
        }),
      )}
      {peerPointers.map((peer) => (
        <div
          key={peer.peerId}
          className="peer-pointer"
          aria-hidden
          style={{
            left: graphOffsetX + peer.point.x,
            top: graphOffsetY + peer.point.y,
            ["--peer-color" as string]: peer.color,
          }}
        >
          <span className="peer-pointer-label">{peer.name}</span>
        </div>
      ))}
    </>
  );
}
