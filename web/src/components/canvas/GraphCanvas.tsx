/**
 * The relationship graph's canvas layer.
 *
 * Everything the board draws when the pane is in graph mode: canvas
 * decorations, dependency wiring, logic gates and the node cards with their
 * resize handles. It owns no state — LaneView keeps the interaction hooks and
 * hands their state and handlers down, so what is drawn is decided in one
 * place and how it reacts in another.
 */

import { useMemo, useRef, useState } from "react";
import { reportError } from "../../store";
import { formatList, localizedStatusLabel, useI18n } from "../../i18n";
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
import { GraphCard, type CardDetail, type CardEvents } from "./GraphCard";
import { PeerOverlay } from "./PeerOverlay";
import { GateWiring, gateLayout, type Gate, type GateLayout } from "./GateWiring";
import { ReferenceLines, setHoveredReferenceCard } from "./ReferenceLines";
import { StandaloneLogicGate } from "./LogicGate";
import type { useCardDrag, CardDragState } from "./useCardDrag";
import type { useCardResize, CardResizeState } from "./useCardResize";
import type { useConnectionDrag, ConnectionDragState } from "./useConnectionDrag";

import {
  ROW_H,
  edgePlacementKey,
  gateWireKey,
  type Col,
  type Point,
  type Rect,
} from "./geometry";
import {
  boundsOf,
  cardRect,
  rectsIntersect,
  type ViewWindow,
} from "./viewportCulling";
import { obstacleIndex, watchedIndex, type ObstacleIndex } from "./obstacleIndex";
import type { GraphSelection, LaneContextMenu } from "../LaneView";
import { useAgents } from "../../agents/agentStore";
import { safeCssColor } from "../../cssColor";
import { nodeTypeOf } from "../../domain/graph/nodeTypes";
import { effectiveShape, nodeTypeLabel } from "../../nodeTypeTheme";

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
  /**
   * The part of the board to draw on a culled board (see viewportCulling);
   * null or left out, every card and wire is drawn.
   */
  viewWindow?: ViewWindow;
  /** What every card leaves out at the board's scale (cardDetailAt). */
  cardDetail?: CardDetail;

  // Alignment. The boxes are shared by every gesture that can snap; the guides
  // are whatever the gesture currently running has landed on.
  snapEnabled: boolean;
  snapBoxes: readonly SnapBox[];
  snapGuides: SnapGuide[];
  onSnapGuides: (guides: SnapGuide[]) => void;

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
  viewWindow = null,
  cardDetail,
  snapEnabled,
  snapBoxes,
  snapGuides,
  onSnapGuides,
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
  // A missing list is a new [] on every board render; one shared empty list
  // keeps the cards' props equal so they can skip it.
  const statusDefinitions = customStatuses.length ? customStatuses : NO_STATUS_DEFINITIONS;

  // The cards are memoized, so they get one handler object for good and every
  // call is forwarded to whatever this render was handed.
  const handed = {
    onCardPointerDown,
    onCardPointerMove,
    onCardPointerUp,
    finishConnectionDrag,
    cardDragHandlers,
    cardResizeHandlers,
    connectionDrag,
    connectionHover,
    connectionHandlers,
    selectedGraphNodeIDs,
    selectNode,
    setContextMenu,
    openTab,
    centerX,
    rowTop,
    graphSelection,
    setGraphSelection,
    onSelectedNodeChange,
    openConditionMenu,
    saveGatePlacement,
    getWireVertices,
    saveWireVertices,
    updateNode,
    setGraphNotice,
    t,
  };
  const latest = useRef(handed);
  latest.current = handed;
  // The card holding keyboard focus. A culled board keeps it, and the cards
  // either side of it in tab order, mounted wherever the view goes.
  const [focusedId, setFocusedId] = useState<string | null>(null);
  // The layer over the cards that condition gates draw their boxes into.
  const [gateBadgeLayer, setGateBadgeLayer] = useState<SVGGElement | null>(null);
  const cardEvents = useMemo<CardEvents>(
    () => ({
      pointerDown: (event, id) => latest.current.onCardPointerDown(event, id),
      pointerMove: (event) => latest.current.onCardPointerMove(event),
      pointerUp: (event, id) => latest.current.onCardPointerUp(event, id),
      pointerCancel: (event) => {
        const now = latest.current;
        if (!now.finishConnectionDrag(event, false)) now.cardDragHandlers.cancel();
      },
      pointerEnter: (id) => setHoveredReferenceCard(id),
      pointerLeave: (id) => {
        const now = latest.current;
        setHoveredReferenceCard(null);
        if (now.connectionHover?.nodeId === id && !now.connectionDrag) {
          now.connectionHandlers.clearHover();
        }
      },
      lostPointerCapture: (id) => {
        const now = latest.current;
        now.cardDragHandlers.cancel();
        if (now.connectionDrag?.sourceId === id) now.connectionHandlers.cancel();
      },
      focus: (id) => {
        const now = latest.current;
        setFocusedId(id);
        if (!now.selectedGraphNodeIDs.has(id)) now.selectNode(id);
      },
      blur: (id) => setFocusedId((current) => (current === id ? null : current)),
      keyDown: (event, id) => {
        // The menu key and Shift+F10 open the node menu, as a right
        // click would, at the card's own corner.
        if (event.key === "ContextMenu" || (event.key === "F10" && event.shiftKey)) {
          event.preventDefault();
          event.stopPropagation();
          const rect = event.currentTarget.getBoundingClientRect();
          latest.current.setContextMenu({
            kind: "node-actions",
            nodeId: id,
            x: rect.left + 8,
            y: rect.bottom + 4,
          });
          return;
        }
        if (event.key !== "Enter") return;
        event.preventDefault();
        event.stopPropagation();
        void latest.current.openTab(id).catch(reportError);
      },
      contextMenu: (event, id) => {
        event.preventDefault();
        event.stopPropagation();
        latest.current.selectNode(id);
        latest.current.setContextMenu({
          kind: "node-actions",
          nodeId: id,
          x: event.clientX,
          y: event.clientY,
        });
      },
      moreClick: (event, id) => {
        event.stopPropagation();
        const rect = event.currentTarget.getBoundingClientRect();
        latest.current.selectNode(id);
        latest.current.setContextMenu({
          kind: "node-actions",
          nodeId: id,
          x: rect.left,
          y: rect.bottom + 4,
        });
      },
      resizePointerDown: (event, id, dirX, dirY, width, height) =>
        latest.current.cardResizeHandlers.onPointerDown(event, id, dirX, dirY, width, height),
      resizePointerMove: (event) => latest.current.cardResizeHandlers.onPointerMove(event),
      resizePointerUp: (event) => latest.current.cardResizeHandlers.onPointerUp(event),
      resizePointerCancel: (event) => latest.current.cardResizeHandlers.onPointerCancel(event),
      resizeKeyDown: (event, id, dirX, dirY, width, height) =>
        latest.current.cardResizeHandlers.onKeyDown(event, id, dirX, dirY, width, height),
    }),
    [],
  );
  // The gates are memoized too: their handlers forward to this render's, and
  // the geometry they read at render time comes with a new layout whenever
  // it moves (see gateLayoutOf).
  const gateEvents = useMemo(
    () => ({
      centerX: (column: Col) => latest.current.centerX(column),
      rowTop: (row: number) => latest.current.rowTop(row),
      getWireVertices: (wireKey: string) => latest.current.getWireVertices(wireKey),
      saveWireVertices: (wireKey: string, vertices: Point[]) =>
        latest.current.saveWireVertices(wireKey, vertices),
      saveGatePlacement: (targetId: string, point: Point) =>
        latest.current.saveGatePlacement(targetId, point),
      selectEdge: (from: string, to: string) => {
        latest.current.setGraphSelection({ kind: "edge", from, to });
        latest.current.onSelectedNodeChange?.(null);
      },
      openEdgeMenu: (edges: { from: string; to: string }[], x: number, y: number) => {
        const now = latest.current;
        const first = edges[0];
        if (!first) return;
        // Right-clicking one of several selected wires acts on the whole
        // selection, the way it does for cards.
        const selection = now.graphSelection;
        const inSelection =
          selection?.kind === "edges" &&
          selection.edges.some((edge) => edge.from === first.from && edge.to === first.to);
        const targets = inSelection && selection?.kind === "edges" ? selection.edges : edges;
        if (!inSelection) {
          now.setGraphSelection({ kind: "edge", from: first.from, to: first.to });
        }
        now.onSelectedNodeChange?.(null);
        now.setContextMenu({ kind: "edge", edges: targets, x, y });
      },
      selectVertex: (wireKey: string, index: number) => {
        latest.current.setGraphSelection({ kind: "vertex", wireKey, index });
        latest.current.onSelectedNodeChange?.(null);
      },
      editGate: (targetId: string, x: number, y: number) =>
        latest.current.openConditionMenu(targetId, x, y),
      deleteGate: (targetId: string) => {
        void latest.current
          .updateNode({
            type: "node.setRequires",
            nodeId: targetId,
            requires: "",
            // Keep the edges: only the condition expression goes.
            refs: null,
          })
          .then((result) => {
            if (!result.ok) {
              reportError(new Error(result.message));
              return;
            }
            latest.current.setGraphNotice({
              text: latest.current.t("canvas.deleteGateNotice"),
              kind: "ok",
            });
          });
      },
    }),
    [],
  );
  const lifted = drag?.moved ? new Set(drag.ids) : null;

  // Where a gate's condition box was put by hand, in board units.
  const gatePlacement = (gate: Gate): Point | undefined => {
    const placement = graph?.ui?.gates?.[gate.to.node.id];
    return Number.isFinite(placement?.x) && Number.isFinite(placement?.y)
      ? { x: graphOffsetX + placement!.x!, y: graphOffsetY + placement!.y! }
      : undefined;
  };

  // The board's obstacles, kept as the same list while nothing on it moves so
  // the gates drawn against them can skip a render, with an index to route a
  // gate against the cards around it instead of every card on the board.
  const routing = useRef<{
    obstacles: Rect[];
    index: ObstacleIndex;
    layouts: Map<string, CachedGateLayout>;
    gates: Gate[] | null;
    inputs: Map<Gate, GateInputs>;
    inputsFrom: unknown[];
  } | null>(null);
  if (!routing.current) {
    routing.current = {
      obstacles: graphObstacles,
      index: obstacleIndex(graphObstacles),
      layouts: new Map(),
      gates: null,
      inputs: new Map(),
      inputsFrom: [],
    };
  } else if (routing.current.obstacles !== graphObstacles) {
    const moved = movedRects(routing.current.obstacles, graphObstacles);
    if (moved === null || moved.length > 0) {
      // A layout that never looked where a card moved still holds.
      for (const [id, cached] of routing.current.layouts) {
        const reach = cached.reach;
        if (reach && (moved === null || moved.some((rect) => rectsIntersect(rect, reach)))) {
          routing.current.layouts.delete(id);
        }
      }
      routing.current.obstacles = graphObstacles;
      routing.current.index = obstacleIndex(graphObstacles);
    }
  }
  const { obstacles: boardObstacles, index: boardIndex, layouts: layoutCache } = routing.current;
  if (routing.current.gates !== gates) {
    const ids = new Set(gates.map((gate) => gate.to.node.id));
    for (const id of layoutCache.keys()) if (!ids.has(id)) layoutCache.delete(id);
    routing.current.gates = gates;
  }

  // What a gate's place is worked out from: its cards, its hand placement and
  // the bends of its wires (the bends do not move the box, but the gate draws
  // them, so they are part of what it is redrawn for). In graph mode all of it
  // follows from the cards, the stored layout and the board's offset, so it
  // carries over from render to render — a pan reads it, never rebuilds it.
  const inputsFrom = [gates, cols, graph?.ui, graphOffsetX, graphOffsetY];
  if (!sameValues(routing.current.inputsFrom, inputsFrom)) {
    routing.current.inputs = new Map();
    routing.current.inputsFrom = inputsFrom;
  }
  const gateInputs = routing.current.inputs;
  const inputsOf = (gate: Gate): GateInputs => {
    const known = gateInputs.get(gate);
    if (known) return known;
    const ends = [gate.to, ...gate.parents.map((parent) => parent.from)];
    const rects = ends.map((end) => cardRect(end, centerX, rowTop));
    const placement = gatePlacement(gate);
    const ratio = graph?.ui?.gates?.[gate.to.node.id]?.ratio;
    const wires = [
      getWireVertices(gateWireKey(gate.to.node.id)),
      ...gate.parents.map((parent) =>
        getWireVertices(edgePlacementKey(parent.from.node.id, gate.to.node.id)),
      ),
    ];
    const key: number[] = [placement?.x ?? NaN, placement?.y ?? NaN, ratio ?? NaN];
    for (const rect of rects) key.push(rect.left, rect.top, rect.right, rect.bottom);
    for (const wire of wires) {
      key.push(wire.length);
      for (const point of wire) key.push(point.x, point.y);
    }
    const inputs = {
      rects,
      placement,
      ratio,
      bends: [...(placement ? [placement] : []), ...wires.flat()],
      key,
    };
    gateInputs.set(gate, inputs);
    return inputs;
  };
  // A gate's layout, worked out again only when something it was worked out
  // from has moved: a pan that keeps a gate on screen reuses it, and one that
  // brings a gate back finds it here.
  const gateLayoutOf = (gate: Gate): GateLayout => {
    const inputs = inputsOf(gate);
    const cached = layoutCache.get(gate.to.node.id);
    if (
      cached &&
      cached.gate === gate &&
      (cached.key === inputs.key || sameNumbers(cached.key, inputs.key))
    ) {
      return cached.layout;
    }
    const watched = watchedIndex(boardIndex);
    const layout = gateLayout(gate, centerX, rowTop, inputs.placement, inputs.ratio, watched);
    layoutCache.set(gate.to.node.id, {
      gate,
      key: inputs.key,
      layout,
      reach: watched.reach(),
    });
    return layout;
  };

  // A culled board draws the cards near the view and the ones somebody is
  // holding: selected, open, focused (with its tab neighbours), dragged,
  // resized, or an end of the wire being drawn. Every card keeps its place
  // in draw order, so tab order and stacking do not change.
  let drawnCols = cols;
  let drawnGates = gates;
  if (viewWindow) {
    const pinned = new Set(selectedGraphNodeIDs);
    if (activeTab) pinned.add(activeTab);
    for (const id of drag?.ids ?? []) pinned.add(id);
    if (cardResize) pinned.add(cardResize.nodeId);
    if (connectionDrag?.sourceId) pinned.add(connectionDrag.sourceId);
    if (connectionDrag?.targetId) pinned.add(connectionDrag.targetId);
    if (connectionHover) pinned.add(connectionHover.nodeId);
    if (focusedId) {
      const at = cols.findIndex((column) => column.node.id === focusedId);
      for (const column of [cols[at - 1], cols[at], cols[at + 1]]) {
        if (at >= 0 && column) pinned.add(column.node.id);
      }
    }
    const view = viewWindow === "measuring" ? null : viewWindow;
    drawnCols = cols.filter(
      (column) =>
        pinned.has(column.node.id) ||
        (view !== null && rectsIntersect(cardRect(column, centerX, rowTop), view)),
    );
    // A wire is drawn when the box around its ends, its gate and its bends
    // meets the view — so one crossing the view from two cards out of it is —
    // or when a card it joins is being dragged.
    const dragged = new Set(drag?.ids);
    drawnGates = view
      ? gates.filter((gate) => {
          if (dragged.has(gate.to.node.id)) return true;
          if (gate.parents.some((parent) => dragged.has(parent.from.node.id))) return true;
          const { rects, bends } = inputsOf(gate);
          const bounds = boundsOf(rects, bends);
          return bounds !== null && rectsIntersect(bounds, view);
        })
      : [];
  }

  const agentSessions = useAgents((s) => s.sessions);
  const agentByNodeId = new Map<string, { name: string; agent: string }>();
  for (const s of agentSessions) {
    if (s.online && s.nodeId && !agentByNodeId.has(s.nodeId)) {
      agentByNodeId.set(s.nodeId, { name: s.name, agent: s.agent });
    }
  }

  // A card carries one marker at most, the one that changes what to do next:
  // waiting beats blocking-others beats being a starting point. Everything else
  // the card used to print — the status word, kind, priority, tags, the error
  // dot — is in its tooltip (cardDetails) and in the drawer.
  const cardMarker = (id: string) =>
    analysis.blocked.has(id)
      ? { kind: "waiting", label: t("board.card.waiting") }
      : analysis.blocking.has(id)
        ? { kind: "blocking", label: t("board.card.blocking") }
        : analysis.entryNodeIds.has(id)
          ? { kind: "start", label: t("canvas.entryLabel") }
          : null;
  // The notes a card is written against, by title: the first five, then a count.
  const titleOf = new Map((graph?.nodes ?? []).map((node) => [node.id, node.title || node.id]));
  const referenceTitles = (refs: string[]) => {
    const titles = refs.map((ref) => titleOf.get(ref) ?? ref);
    return titles.length > 5
      ? `${formatList(titles.slice(0, 5))} +${titles.length - 5}`
      : formatList(titles);
  };
  // The cards whose references are drawn lit: what is selected and what is
  // open. The card under the pointer joins them inside ReferenceLines.
  const referenceActiveIds = new Set(selectedGraphNodeIDs);
  if (activeTab) referenceActiveIds.add(activeTab);
  const cardDetails = (
    node: GraphNode,
    status: Status,
    assigned: string,
    typeLabel: string,
    hasStatus: boolean,
  ) => [
    node.title || node.id,
    t("board.card.type", { type: typeLabel }),
    hasStatus ? `${localizedStatusLabel(status, customStatuses)} · ${assigned}` : assigned,
    ...(analysis.blocked.has(node.id) ? [t("board.card.waitingDetail")] : []),
    ...(analysis.blocking.has(node.id) ? [t("board.card.blockingDetail")] : []),
    ...(analysis.entryNodeIds.has(node.id)
      ? [
          typeof graph?.ui?.entryOverrides?.[node.id] === "boolean"
            ? t("canvas.entry.manual")
            : t("canvas.entry.auto"),
        ]
      : []),
    ...(errorNodes.has(node.id) ? [t("board.card.errorDetail")] : []),
    ...(node.priority
      ? [t("board.card.priority", { priority: t(`graphTools.${node.priority}`) })]
      : []),
    ...(node.tags?.length ? [t("board.card.tags", { tags: formatList(node.tags) })] : []),
    ...(node.refs?.length ? [t("board.card.refs", { titles: referenceTitles(node.refs) })] : []),
  ];

  // A marquee grabs cards, but a wire between two grabbed cards belongs to the
  // same selection: it is drawn selected so the picked set reads as one shape.
  // One set per selection, so a gate can tell that nothing changed.
  const graphEdges = graph?.edges;
  const selectedEdgeKeys = useMemo(() => {
    const keys = new Set<string>();
    if (graphSelection?.kind === "edge") {
      keys.add(edgePlacementKey(graphSelection.from, graphSelection.to));
    } else if (graphSelection?.kind === "edges") {
      for (const edge of graphSelection.edges) {
        keys.add(edgePlacementKey(edge.from, edge.to));
      }
    } else if (
      graphSelection?.kind === "vertex" &&
      !graphSelection.wireKey.startsWith("gate:")
    ) {
      keys.add(graphSelection.wireKey);
    }
    if (selectedGraphNodeIDs.size > 1) {
      for (const edge of graphEdges ?? []) {
        if (selectedGraphNodeIDs.has(edge.from) && selectedGraphNodeIDs.has(edge.to)) {
          keys.add(edgePlacementKey(edge.from, edge.to));
        }
      }
    }
    return keys;
  }, [graphSelection, selectedGraphNodeIDs, graphEdges]);
  const selectedVertex = graphSelection?.kind === "vertex" ? graphSelection : null;

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
        {/* Under everything else: a reference is context, never a wire to
            click, and every dependency paints over it. */}
        <ReferenceLines
          graph={graph}
          colOf={colOf}
          centerX={centerX}
          rowTop={rowTop}
          activeIds={referenceActiveIds}
        />
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
        {drawnGates.map((gate) => (
          <GateWiring
            key={gate.to.node.id}
            gate={gate}
            centerX={gateEvents.centerX}
            rowTop={gateEvents.rowTop}
            layout={gateLayoutOf(gate)}
            onPositionChange={gateEvents.saveGatePlacement}
            selectedEdges={selectedEdgeKeys}
            onSelectEdge={gateEvents.selectEdge}
            onOpenEdgeMenu={gateEvents.openEdgeMenu}
            getWireVertices={gateEvents.getWireVertices}
            onWireVerticesChange={gateEvents.saveWireVertices}
            selectedVertex={selectedVertex}
            onSelectVertex={gateEvents.selectVertex}
            obstacles={boardObstacles}
            onEditGate={gateEvents.editGate}
            onDeleteGate={gateEvents.deleteGate}
            badgeLayer={gateBadgeLayer}
          />
        ))}
      </svg>
      {drawnCols.map((c) => {
        const n = c.node;
        const st: Status = statuses[n.id] ?? "ready";
        const assigned = (n.assignee && userNames.get(n.assignee)) || t("canvas.unassigned");
        const isDragging = lifted?.has(n.id) === true;
        const isConnectionTarget = connectionDrag?.targetId === n.id;
        const nodeStyle = graph?.ui?.nodeStyles?.[n.id];
        // The type decides the default look; a per-node style still wins.
        const nodeType = nodeTypeOf(graph, n);
        const typeLabel = nodeTypeLabel(nodeType, t);
        const shape = effectiveShape(nodeStyle, nodeType);
        const hasStatus = nodeType.completion !== "none";
        const filteredOut = !matchesViewFilter(n);
        const resizing = cardResize?.nodeId === n.id ? cardResize : null;
        const liveWidth = resizing ? resizing.width : c.width;
        const liveHeight = resizing ? resizing.height : c.height;
        const connectionTargetError =
          isConnectionTarget && connectionDrag ? connectionPreviewError : null;
        const selected = selectedGraphNodeIDs.has(n.id);
        const marker = cardMarker(n.id);
        const agent = agentByNodeId.get(n.id);
        return (
          <GraphCard
            key={n.id}
            node={n}
            detail={cardDetail}
            left={centerX(c) - liveWidth / 2}
            top={rowTop(c.row) + ROW_H / 2 - liveHeight / 2}
            width={liveWidth}
            height={liveHeight}
            className={`col-card ${hasStatus ? `status-${st}` : "no-status"}${errorNodes.has(n.id) ? " has-error" : ""}${
              activeTab === n.id ? " active" : ""
            }${selected ? " selected" : ""}${
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
            }${shape !== "rect" ? ` shape-${shape}` : ""}${
              nodeStyle?.align ? ` align-${nodeStyle.align}` : ""
            }${nodeStyle?.valign ? ` valign-${nodeStyle.valign}` : ""}`}
            cardBg={safeCssColor(nodeStyle?.color)}
            borderColor={safeCssColor(nodeStyle?.borderColor || nodeType.color)}
            typeColor={safeCssColor(nodeType.color)}
            textColor={safeCssColor(nodeStyle?.textColor)}
            dragging={isDragging}
            hasStatus={hasStatus}
            status={st}
            customStatuses={statusDefinitions}
            nodeType={nodeType}
            assigned={assigned}
            markerKind={marker?.kind}
            markerLabel={marker?.label}
            agentName={agent?.name}
            agentKind={agent?.agent}
            ariaLabel={[
              n.title || n.id,
              typeLabel,
              ...(hasStatus ? [localizedStatusLabel(st, customStatuses)] : []),
            ].join(" · ")}
            tooltip={cardDetails(n, st, assigned, typeLabel, hasStatus).join("\n")}
            selected={selected}
            events={cardEvents}
          />
        );
      })}
      {/* Over the cards: a gate box between two neighbouring columns has no
          room clear of them, and under them it could not be read. */}
      <svg className="board-edges board-gate-badges" width={boardW} height={svgH} aria-hidden>
        <g ref={setGateBadgeLayer} />
      </svg>
      <SnapGuides
        guides={snapGuides}
        offsetX={graphOffsetX}
        offsetY={graphOffsetY}
        zoom={zoom}
      />
      {/* Other people: their own subscription, so a remote pointer redraws
          this layer and nothing else. */}
      <PeerOverlay
        colOf={colOf}
        centerX={centerX}
        rowTop={rowTop}
        offsetX={graphOffsetX}
        offsetY={graphOffsetY}
      />
    </>
  );
}

const NO_STATUS_DEFINITIONS: StatusDefinition[] = [];

/** What a gate's layout was last worked out from, and how far it looked. */
interface CachedGateLayout {
  gate: Gate;
  key: number[];
  layout: GateLayout;
  /** The box around every obstacle it looked up; null when it looked up none. */
  reach: Rect | null;
}

interface GateInputs {
  /** The gate's node first, then each input's card. */
  rects: Rect[];
  placement: Point | undefined;
  ratio: number | undefined;
  /** The hand placement and every bend of its wires. */
  bends: Point[];
  /** All of the above as numbers, to tell whether any of it moved. */
  key: number[];
}

function sameValues(a: readonly unknown[], b: readonly unknown[]): boolean {
  return a.length === b.length && a.every((value, i) => Object.is(value, b[i]));
}

function sameNumbers(a: readonly number[], b: readonly number[]): boolean {
  if (a.length !== b.length) return false;
  for (let i = 0; i < a.length; i++) if (!Object.is(a[i], b[i])) return false;
  return true;
}

function sameRect(a: Rect, b: Rect): boolean {
  return a.left === b.left && a.top === b.top && a.right === b.right && a.bottom === b.bottom;
}

/**
 * The obstacles that moved between two lists of the board's obstacles, both
 * where they were and where they are; null when the lists do not line up (a
 * card came or went) and anything may have moved.
 */
function movedRects(before: readonly Rect[], after: readonly Rect[]): Rect[] | null {
  if (before.length !== after.length) return null;
  const moved: Rect[] = [];
  for (let i = 0; i < after.length; i++) {
    if (!sameRect(before[i], after[i])) moved.push(before[i], after[i]);
  }
  return moved;
}
