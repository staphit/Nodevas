import {
  useEffect,
  useMemo,
  useRef,
  useState,
  type CSSProperties,
} from "react";
import {
  reportError,
  useApp,
  usePreference,
  type CanvasCommand,
} from "../store";
import { statusOptions } from "../statusTheme";
import { IconPlus } from "../icons";
import { EmptyState } from "./InteractionPrimitives";
import { analyzeGraph } from "../analysis";
import { BoardToolbar } from "./BoardToolbar";
import { GraphBatchToolbar } from "./GraphBatchToolbar";
import { GraphToolsPanel } from "./GraphToolsPanel";
import { TimelineGrid } from "./timeline/TimelineGrid";
import { TimelineCards } from "./timeline/TimelineCards";
import { useTimelineLayout } from "./timeline/useTimelineLayout";
import { useTimelineDays } from "./timeline/useTimelineDays";
import { useTimelineCellSize } from "./timeline/useTimelineCellSize";
import { useMilestoneDrag } from "./timeline/useMilestoneDrag";
import { usePlanMenu } from "./timeline/usePlanMenu";
import { useCanEdit } from "./SignIn";
import { useNarrowViewport } from "./TopbarOverflow";
import { GraphCanvas } from "./canvas/GraphCanvas";
import { GraphMinimap } from "./canvas/GraphMinimap";
import { useCardResize } from "./canvas/useCardResize";
import { useCardDrag } from "./canvas/useCardDrag";
import {
  rectOfColumn,
  snapBoxesOfColumns,
  type SnapBox,
  type SnapGuide,
} from "./canvas/snapping";
import { useCanvasPan } from "./canvas/useCanvasPan";
import { useConnectionDrag } from "./canvas/useConnectionDrag";
import { useMarquee } from "./canvas/useMarquee";
import {
  GraphGestureModeProvider,
  type GraphGestureMode,
} from "./canvas/gestureMode";
import { useGraphWiring } from "./canvas/useGraphWiring";
import { useBoardShortcuts } from "./canvas/useBoardShortcuts";
import { useNodeCreation } from "./canvas/useNodeCreation";
import { useNodeConditions } from "./canvas/useNodeConditions";
import { useCanvasDecorations } from "./canvas/useCanvasDecorations";
import { useGraphViews } from "./canvas/useGraphViews";
import { useBoardColumns } from "./canvas/useBoardColumns";
import {
  COL_W,
  cardCenter,
  centerX as getCenterX,
  clampZoom,
  dayKey,
  edgeKeyEndpoints,
  rectBoundary,
  rowTop as getRowTop,
  ROW_H,
  type Col,
  type Point,
} from "./canvas/geometry";
import {
  boardMetrics,
  fitViewToCards,
  graphCellAt,
  graphObstacleRects,
} from "./lane/boardGeometry";
import { connectionPreview } from "./lane/connectionPreview";
import { GraphAnalysisPanel } from "./lane/GraphAnalysisPanel";
import {
  LaneContextMenuLayer,
  useLaneContextMenu,
} from "./lane/LaneContextMenuLayer";
import { useBoardReveal } from "./lane/useBoardReveal";
import { useBoardViewport } from "./lane/useBoardViewport";
import { useEdgeCommands } from "./lane/useEdgeCommands";
import { useLaneNotices } from "./lane/useLaneNotices";
import { useDependencyGates } from "./lane/useDependencyGates";
import { useTimelineOrderDrag } from "./lane/useTimelineOrderDrag";

export type LaneContextMenu =
  | { kind: "plan"; x: number; y: number; nodeId: string; date: string }
  | { kind: "graph"; x: number; y: number; col: number; row: number }
  | { kind: "node-create"; x: number; y: number; col: number; row: number }
  | {
      kind: "edge";
      x: number;
      y: number;
      edges: { from: string; to: string }[];
    }
  | { kind: "logic-gate"; x: number; y: number; gateId: string }
  | { kind: "node"; x: number; y: number; nodeId: string };

export type GraphSelection =
  | { kind: "node"; id: string }
  | { kind: "nodes"; ids: string[] }
  | { kind: "logic-gate"; id: string }
  | { kind: "edge"; from: string; to: string }
  /** Several wires at once, as a marquee collects them. */
  | { kind: "edges"; edges: { from: string; to: string }[] }
  | { kind: "vertex"; wireKey: string; index: number }
  | null;

/** Props both panes take. `variant` is supplied by GraphPane / TimelinePane. */
export interface PaneProps {
  collapsed?: boolean;
  onToggle?: () => void;
  paneStyle?: CSSProperties;
  selectedNodeId?: string | null;
  onSelectedNodeChange?: (nodeId: string | null) => void;
  keyboardActive?: boolean;
  onActivate?: () => void;
}

export function LaneView({
  variant,
  collapsed,
  onToggle,
  paneStyle,
  selectedNodeId,
  onSelectedNodeChange,
  keyboardActive,
  onActivate,
}: PaneProps & { variant: "graph" | "timeline" }) {
  const isGraph = variant === "graph";
  const graph = useApp((s) => s.graph);
  const statuses = useApp((s) => s.statuses);
  const runState = useApp((s) => s.runState);
  const issues = useApp((s) => s.issues);
  const openTab = useApp((s) => s.openTab);
  const updateCanvasLayout = useApp((s) => s.updateCanvasLayout);
  const updatePlan = useApp((s) => s.updatePlan);
  const updateWorkflowDefinition = useApp((s) => s.updateWorkflowDefinition);
  const updateNode = useApp((s) => s.updateNode);
  const createNode = useApp((s) => s.createNode);
  const duplicateNode = useApp((s) => s.duplicateNode);
  const deleteNode = useApp((s) => s.deleteNode);
  const deleteNodes = useApp((s) => s.deleteNodes);
  const setStatus = useApp((s) => s.setStatus);
  const setLifecycleStatus = useApp((s) => s.setLifecycleStatus);
  const moveStamp = useApp((s) => s.moveStamp);
  const activeTab = useApp((s) => s.activeTab);
  const activeProject = useApp((s) => s.activeProject);
  const planStatusDefinitions = graph?.ui?.planStatuses ?? [];
  const customStatuses = graph?.ui?.customStatuses ?? [];
  const selectableStatuses = statusOptions(customStatuses);
  // Timeline layout is a personal preference: it lives in the versioned local
  // adapter, never in the project file [A-03].
  const updateUIPreference = useApp((state) => state.updateUIPreference);
  const timelineOrientation = usePreference("timelineOrientation");
  const timelineDateAxis = usePreference("timelineDateAxis");
  const timelineNodeAxis = usePreference("timelineNodeAxis");
  const changeTimelineDateAxis = (axis: "top" | "bottom") =>
    updateUIPreference("timelineDateAxis", axis);
  const changeTimelineNodeAxis = (axis: "left" | "right") =>
    updateUIPreference("timelineNodeAxis", axis);
  const timelineVNodeAxis = usePreference("timelineVerticalNodeAxis");
  const timelineVDateAxis = usePreference("timelineVerticalDateAxis");
  const {
    timelineCellAuto,
    timelineCellWidth,
    timelineCellHeight,
    timelineWidthResizeEdge,
    setTimelineWidthResizeEdge,
    setTimelineCellSize,
    useAutoTimelineCellSize,
  } = useTimelineCellSize({ graph, runState, updateUIPreference });

  const snapToGrid = usePreference("graphSnapToGrid");
  const setSnapToGrid = (value: boolean) =>
    updateUIPreference("graphSnapToGrid", value);
  const minimapVisible = usePreference("graphMinimap");
  const setMinimapVisible = (value: boolean) =>
    updateUIPreference("graphMinimap", value);

  // How large the graph draws its cards and their text, on top of the zoom.
  // Both are a way of looking at the board rather than a fact about it, so like
  // the zoom they live here and start over at 100% — nothing is written to the
  // project file or remembered between sessions.
  const [nodeScale, setNodeScale] = useState(1);
  const [cardFontScale, setCardFontScale] = useState(1);
  const graphNodeScale = isGraph ? nodeScale : 1;
  const graphFontScale = isGraph ? cardFontScale : 1;
  const resetCardScale = () => {
    setNodeScale(1);
    setCardFontScale(1);
  };

  const changeTimelineVNodeAxis = (axis: "top" | "bottom") =>
    updateUIPreference("timelineVerticalNodeAxis", axis);
  const changeTimelineVDateAxis = (axis: "right" | "left") =>
    updateUIPreference("timelineVerticalDateAxis", axis);
  const [now, setNow] = useState(() => new Date());
  const [graphSelection, setGraphSelection] = useState<GraphSelection>(null);
  const { graphNotice, setGraphNotice, planNotice, setPlanNotice } =
    useLaneNotices();
  const [graphToolsOpen, setGraphToolsOpen] = useState(false);
  const [analysisOpen, setAnalysisOpen] = useState(false);
  // What a plain drag means, for a pointer with no modifier keys to hold. The
  // modifiers keep working in every mode, so this only ever adds a way in.
  const [gestureMode, setGestureMode] = useState<GraphGestureMode>("select");

  const selectedGraphNodeIDs = useMemo(
    () =>
      new Set(
        graphSelection?.kind === "node"
          ? [graphSelection.id]
          : graphSelection?.kind === "nodes"
            ? graphSelection.ids
            : [],
      ),
    [graphSelection],
  );
  const selectedIDs = [...selectedGraphNodeIDs];
  /** One destructive action at a time: shared by the shortcuts and the batch toolbar. */
  const [shortcutBusy, setShortcutBusy] = useState(false);

  const runCanvasCommand = (command: CanvasCommand) => {
    void updateCanvasLayout(command).then((result) => {
      if (!result.ok) reportError(new Error(result.message));
    });
  };

  const {
    savedViewName,
    setSavedViewName,
    viewFilter,
    setViewFilter,
    matchesViewFilter,
    applyBatchStatus,
    applyBatchAssignee,
    saveCurrentView,
    applySavedView,
    removeSavedView,
  } = useGraphViews({
    statuses,
    selectedIDs,
    setShortcutBusy,
    setStatus,
    setGraphNotice,
    updateNode,
    updateCanvasLayout,
    runCanvasCommand,
  });
  const boardRef = useRef<HTMLDivElement>(null);
  const canvasPan = useCanvasPan({
    boardRef,
    collapsed,
    displayScale: graphNodeScale,
  });
  // Two different numbers on purpose. `zoom` is the user's own, and the only
  // one the toolbar shows or the wheel changes. `viewScale` is what the board
  // is actually drawn at — zoom times the node scale — so it is what every
  // conversion between client pixels and board units has to divide by.
  const { zoom, panning, viewScale } = canvasPan.state;
  const panHandlers = canvasPan.handlers;
  const zoomAt = canvasPan.zoomAt;
  const setZoomDirect = canvasPan.setZoom;
  const graphPointFromClient = canvasPan.pointFromClient;
  const { boardViewport, boardScroll, setBoardScroll } = useBoardViewport({
    boardRef,
    collapsed,
    isGraph,
    activeProject,
    zoom: viewScale,
  });
  // A read-only session keeps every gesture that only looks — select, open,
  // pan, zoom — and loses the ones that would write. The server refuses those
  // regardless; this is what stops the card from moving under the pointer
  // first and springing back afterwards.
  const canEdit = useCanEdit();

  const userNames = useMemo(
    () => new Map((graph?.users ?? []).map((user) => [user.id, user.name])),
    [graph?.users],
  );
  const analysis = useMemo(
    () => analyzeGraph(graph, statuses, now, runState?.flags),
    [graph, statuses, now, runState?.flags],
  );
  const violationNodeIDs = useMemo(
    () => new Set(analysis.violations.map((item) => item.nodeId)),
    [analysis],
  );
  const selectNode = (nodeId: string) => {
    if (isGraph) setGraphSelection({ kind: "node", id: nodeId });
    onSelectedNodeChange?.(nodeId);
  };

  const changeTimelineOrientation = (orientation: "vertical" | "horizontal") =>
    updateUIPreference("timelineOrientation", orientation);

  // Calendar resolution is one day; a 30-minute refresh keeps the current-time
  // line useful without waking the app every second.
  useEffect(() => {
    const refreshNow = () => setNow(new Date());
    const timer = window.setInterval(refreshNow, 30 * 60 * 1000);
    window.addEventListener("focus", refreshNow);
    document.addEventListener("visibilitychange", refreshNow);
    return () => {
      window.clearInterval(timer);
      window.removeEventListener("focus", refreshNow);
      document.removeEventListener("visibilitychange", refreshNow);
    };
  }, []);

  const errorNodes = useMemo(() => {
    const set = new Set<string>();
    for (const i of issues) if (i.severity === "error" && i.nodeId) set.add(i.nodeId);
    return set;
  }, [issues]);

  // ----- graph layout: stored positions use continuous grid units so
  // integer coordinates remain compatible while dragging is fully free-form.
  const { cols, colOf, maxCol, maxRow, timelineCols, timelineColOf } =
    useBoardColumns({
      graph,
      statuses,
      timelineSort: viewFilter.sort,
      selectedNodeId,
      timelineCellWidth,
      timelineCellHeight,
    });

  const { boardW, graphOffsetX, graphCanvasH, graphOffsetY } = boardMetrics({
    isGraph,
    maxCol,
    maxRow,
    timelineColumnCount: timelineCols.length,
    timelineCellWidth,
    boardViewport,
    zoom: viewScale,
  });

  const logicGates = graph?.ui?.logicGates ?? [];

  // ----- alignment. Cards, group boxes and sticky notes all offer their edges
  // and centre lines; whatever is moving is excluded when the gesture starts.
  const snapGuidesEnabled = usePreference("graphSnapGuides");
  const setSnapGuidesEnabled = (enabled: boolean) =>
    updateUIPreference("graphSnapGuides", enabled);
  // Gestures that live inside their own component report their lines up here,
  // because the overlay belongs to the board.
  const [decorationGuides, setDecorationGuides] = useState<SnapGuide[]>([]);
  const snapBoxes = useMemo<SnapBox[]>(() => {
    if (!isGraph || !snapGuidesEnabled) return [];
    const decorations = [...(graph?.ui?.groups ?? []), ...(graph?.ui?.annotations ?? [])];
    return [
      ...snapBoxesOfColumns(cols),
      ...decorations.map((item) => ({
        id: item.id,
        rect: {
          left: item.x,
          right: item.x + item.width,
          top: item.y,
          bottom: item.y + item.height,
        },
      })),
    ];
  }, [isGraph, snapGuidesEnabled, cols, graph?.ui?.groups, graph?.ui?.annotations]);
  const snapRectOf = (nodeId: string) => {
    const column = colOf.get(nodeId);
    return column ? rectOfColumn(column) : null;
  };

  const { state: cardResize, handlers: cardResizeHandlers } = useCardResize({
    zoom: viewScale,
    canResize: canEdit,
    snapEnabled: snapGuidesEnabled,
    snapBoxes,
    rectOf: snapRectOf,
    onCommit: (nodeId, size) => {
      void updateNode({
        type: "node.setStyle",
        nodeId,
        patch: size,
      }).then((result) => {
        if (!result.ok) reportError(new Error(result.message));
      });
    },
  });

  // ----- dependency gates (incoming edges per target)
  const gates = useDependencyGates({ graph, colOf, statuses, analysis });

  /**
   * Fire-and-forget canvas command. Layout edits happen constantly while
   * dragging, so failures report through the shared error channel rather than
   * interrupting the gesture [A-04].
   */

  const { state: drag, handlers: cardDragHandlers } = useCardDrag({
    zoom: viewScale,
    canMove: canEdit,
    snapToGrid,
    columns: cols,
    snapEnabled: snapGuidesEnabled,
    snapBoxes,
    selectedIds: selectedGraphNodeIDs,
    onSelect: selectNode,
    onOpen: (nodeId) => void openTab(nodeId).catch(reportError),
    onMove: (positions, count) => {
      // Cards may be dragged past the top-left corner. Cells cannot be
      // negative, so the whole board slides by the overshoot instead and the
      // dragged card ends up where it was dropped.
      const values = Object.values(positions);
      const overshootCols = Math.max(0, -Math.min(0, ...values.map((p) => p.x)));
      const overshootRows = Math.max(0, -Math.min(0, ...values.map((p) => p.y)));
      runCanvasCommand({
        type: "canvas.moveNodes",
        // The positions below already carry the overshoot; the shift is what
        // moves the decorations with them, in one write.
        shift:
          overshootCols > 0 || overshootRows > 0
            ? {
                columns: 0,
                rows: 0,
                x: overshootCols * COL_W,
                y: overshootRows * ROW_H,
              }
            : undefined,
        positions: Object.fromEntries(
          Object.entries(positions).map(([id, point]) => [
            id,
            {
              x: Math.round((point.x + overshootCols) * 1000) / 1000,
              y: Math.round((point.y + overshootRows) * 1000) / 1000,
            },
          ]),
        ),
      });
      if (count > 1) {
        setGraphNotice({ text: `已移動 ${count} 個節點`, kind: "ok" });
      }
    },
    onReject: (message) => setGraphNotice({ text: message, kind: "error" }),
  });

  /**
   * Round every card onto the grid in one command.
   *
   * The snap toggle only governs the card under the pointer, so a board laid
   * out before it was switched on stays crooked however long it is left on.
   * Two cards can round into the same cell; the nearer one takes it and the
   * other keeps its place, because stacking two cards is worse than leaving
   * one card off the grid.
   */
  const alignAllToGrid = () => {
    const claims = new Map<string, { id: string; distance: number; x: number; y: number }>();
    for (const column of cols) {
      const x = Math.round(column.index);
      const y = Math.round(column.row);
      const key = `${x},${y}`;
      const distance = Math.hypot(column.index - x, column.row - y);
      const held = claims.get(key);
      if (!held || distance < held.distance) {
        claims.set(key, { id: column.node.id, distance, x, y });
      }
    }
    const aligned = new Map(
      Array.from(claims.values(), (claim) => [claim.id, { x: claim.x, y: claim.y }]),
    );
    const positions = Object.fromEntries(
      cols.map((column) => [
        column.node.id,
        aligned.get(column.node.id) ?? { x: column.index, y: column.row },
      ]),
    );
    const moved = cols.filter((column) => {
      const next = positions[column.node.id];
      return next.x !== column.index || next.y !== column.row;
    }).length;
    if (!moved) {
      setGraphNotice({ text: "所有節點都已經在格線上", kind: "ok" });
      return;
    }
    runCanvasCommand({ type: "canvas.moveNodes", positions });
    setGraphNotice({ text: `已對齊 ${moved} 個節點`, kind: "ok" });
  };
  // ----- live drag. What a peer sees while a card is still under a pointer;
  // where it lands arrives afterwards as a graph op.
  const reportDrag = useApp((state) => state.reportDrag);
  const ghosts = useApp((state) => state.ghosts);
  const peers = useApp((state) => state.peers);
  const lastDragSentAt = useRef(0);
  const dragReported = useRef(false);
  useEffect(() => {
    if (!isGraph) return;
    if (drag?.moved) {
      // About thirty a second: enough to read as motion, far under the socket's
      // stream budget, and the gesture is redrawn locally either way.
      const now = Date.now();
      if (now - lastDragSentAt.current < 33) return;
      lastDragSentAt.current = now;
      dragReported.current = true;
      reportDrag(drag.ids, drag.dx, drag.dy, true);
      return;
    }
    // One last message so the ghost goes away even if the drop changed nothing.
    if (dragReported.current) {
      dragReported.current = false;
      reportDrag([], 0, 0, false);
    }
  }, [isGraph, drag, reportDrag]);

  // A pointer on the board, in the same offset-free space the ghosts use, so a
  // window scrolled somewhere else still draws it over the right card.
  const presences = useApp((state) => state.presences);
  const reportPresenceState = useApp((state) => state.reportPresenceState);
  const reportPointer = (event: React.PointerEvent<HTMLElement>) => {
    if (!isGraph) return;
    // The board's own conversion: it measures `.board-inner`, which is what
    // scrolls and what CSS `zoom` is applied to. Measuring the scroll container
    // instead lands the pointer somewhere off the top-left corner.
    const point = graphPointFromClient(event.clientX, event.clientY);
    if (!point) return;
    reportPresenceState({
      pointer: { x: point.x - graphOffsetX, y: point.y - graphOffsetY },
    });
  };
  useEffect(() => {
    // The board is not on screen any more, so neither is this pointer.
    return () => reportPresenceState({ pointer: undefined });
  }, [reportPresenceState, isGraph]);

  const peerPointers = useMemo(
    () =>
      peers
        .filter((peer) => presences[peer.id]?.pointer)
        .map((peer) => ({
          peerId: peer.id,
          name: peer.actor?.name || "?",
          color: peer.color || "#888",
          point: presences[peer.id].pointer!,
        })),
    [peers, presences],
  );

  const peerGhosts = useMemo(
    () =>
      Object.entries(ghosts).map(([peerId, ghost]) => ({
        peerId,
        color: peers.find((peer) => peer.id === peerId)?.color || "#888",
        name: peers.find((peer) => peer.id === peerId)?.actor?.name ?? "",
        ...ghost,
      })),
    [ghosts, peers],
  );


  const saveGatePlacement = (targetId: string, point: Point) => {
    runCanvasCommand({
      type: "canvas.setGatePlacement",
      targetId,
      point: { x: point.x - graphOffsetX, y: point.y - graphOffsetY },
    });
  };

  const saveLogicGatePosition = (gateId: string, point: Point) => {
    runCanvasCommand({
      type: "canvas.moveLogicGate",
      gateId,
      point: { x: point.x - graphOffsetX, y: point.y - graphOffsetY },
    });
  };

  const getWireVertices = (wireKey: string): Point[] =>
    (graph?.ui?.wireVertices?.[wireKey] ?? []).map((vertex) => ({
      x: vertex.x + graphOffsetX,
      y: vertex.y + graphOffsetY,
    }));

  const saveWireVertices = (wireKey: string, vertices: Point[]) => {
    runCanvasCommand({
      type: "canvas.setWireVertices",
      wireKey,
      vertices: vertices.map((vertex) => ({
        x: vertex.x - graphOffsetX,
        y: vertex.y - graphOffsetY,
      })),
    });
  };

  const { selectedEdgeEndpoints, setEdgeStyles, toggleEdgeStyle } =
    useEdgeCommands({
      graph,
      graphSelection,
      updateCanvasLayout,
      setGraphNotice,
    });

  // ----- days: past events .. today .. future (extends as you scroll toward
  // either end; the range is unbounded in both directions)
  // ----- collapse every empty day (opt-in)
  const collapseEmpty = usePreference("timelineCollapseEmptyDays");
  const setCollapse = (v: boolean) =>
    updateUIPreference("timelineCollapseEmptyDays", v);

  const { days, onBoardScroll } = useTimelineDays({
    now,
    runState,
    graph,
    isGraph,
    timelineOrientation,
    collapseEmpty,
    timelineCellWidth,
    timelineCellHeight,
    zoom: viewScale,
  });


  const handleBoardScroll = (event: React.UIEvent<HTMLDivElement>) => {
    if (isGraph) {
      const element = event.currentTarget;
      setBoardScroll({ left: element.scrollLeft, top: element.scrollTop });
      return;
    }
    onBoardScroll(event);
  };

  const { state: marquee, handlers: marqueeHandlers } = useMarquee({
    enabled: isGraph,
    withoutModifier: gestureMode === "marquee",
    pointFromClient: graphPointFromClient,
    onSingleSelect: (nodeId) => selectNode(nodeId),
    onMultiSelect: (nodeIds) => {
      setGraphSelection(nodeIds.length ? { kind: "nodes", ids: nodeIds } : null);
      onSelectedNodeChange?.(null);
    },
    onSelectEdges: (wireKeys) => {
      const edges = wireKeys.flatMap((wireKey) => {
        const endpoints = edgeKeyEndpoints(wireKey);
        return endpoints ? [endpoints] : [];
      });
      if (!edges.length) return;
      setGraphSelection(
        edges.length === 1
          ? { kind: "edge", ...edges[0] }
          : { kind: "edges", edges },
      );
      onSelectedNodeChange?.(null);
      setGraphNotice({
        text: `已選取 ${edges.length} 條關係線（右鍵可改語意或線條）`,
        kind: "ok",
      });
    },
    onClearSelection: () => {
      setGraphSelection(null);
      onSelectedNodeChange?.(null);
    },
  });

  useBoardShortcuts({
    isGraph,
    collapsed: collapsed ?? false,
    keyboardActive: keyboardActive ?? false,
    graph,
    graphSelection,
    setGraphSelection,
    selectedEdgeEndpoints,
    marqueeHandlers,
    onSelectedNodeChange,
    openTab,
    deleteNode,
    deleteNodes,
    updateCanvasLayout,
    toggleEdgeStyle,
    shortcutBusy,
    setShortcutBusy,
  });

  const onBoardPointerDown = (event: React.PointerEvent<HTMLDivElement>) => {
    if (event.pointerType === "mouse" && event.button !== 0) return;
    const target = event.target as Element;
    if (
      target.closest(
        "button, input, select, textarea, a, .col-card, .plan-card, .snap-card, .gate-handle, .lane-context-menu, .board-gap",
      )
    ) {
      return;
    }
    if (isGraph && marqueeHandlers.onPointerDown(event)) {
      panHandlers.cancel();
      return;
    }
    if (isGraph) {
      setGraphSelection(null);
      onSelectedNodeChange?.(null);
    }
    panHandlers.onPointerDown(event);
  };

  const onBoardPointerMove = (event: React.PointerEvent<HTMLDivElement>) => {
    // Reported first: a marquee or a pan is exactly when somebody else most
    // wants to see where this pointer is.
    reportPointer(event);
    if (marqueeHandlers.onPointerMove(event)) return;
    panHandlers.onPointerMove(event);
  };

  const endBoardPan = (event: React.PointerEvent<HTMLDivElement>) => {
    if (marqueeHandlers.onPointerUp(event)) return;
    panHandlers.onPointerUp(event);
  };

  const todayKey = dayKey(now);
  const todayStart = new Date(now);
  todayStart.setHours(0, 0, 0, 0);
  const nowOffset =
    ((now.getHours() * 60 + now.getMinutes()) / (24 * 60)) *
    timelineCellHeight;
  const centerX = (c: Col) =>
    getCenterX(c, {
      isGraph,
      graphOffsetX,
      graphOffsetY,
      timelineCellWidth,
      dayY: [],
      dayCount: 0,
    });



  const {
    byNodeDay,
    plansByNodeDay,
    rowItems,
    dayY,
    totalH,
    horizontalTimelineW,
    horizontalTimelineH,
    horizontalNowX,
  } = useTimelineLayout({
    days,
    runState,
    graph,
    collapseEmpty,
    timelineCols,
    timelineCellWidth,
    timelineCellHeight,
    boardViewport,
    zoom: viewScale,
    todayKey,
    nowOffset,
  });

  // Graph view: pure uniform grid (rows have no date meaning).
  // Timeline view: rows are dates, mapped through the collapsed layout.
  const rowTop = (row: number) =>
    getRowTop(row, {
      isGraph,
      graphOffsetX,
      graphOffsetY,
      timelineCellWidth,
      dayY,
      dayCount: days.length,
    });

  useBoardReveal({
    isGraph,
    collapsed,
    boardRef,
    graph,
    colOf,
    timelineColOf,
    timelineCellWidth,
    zoom: viewScale,
    centerX,
    rowTop,
    selectedNodeId,
    onSelectedNodeChange,
    graphSelection,
    setGraphSelection,
  });

  const graphObstacles = isGraph
    ? graphObstacleRects(cols, logicGates, centerX, rowTop, graphOffsetX, graphOffsetY)
    : [];

  const {
    timelineOrderDrag,
    timelineOrderDragRef,
    updateTimelineOrderDrag,
    startTimelineOrderDrag,
    moveTimelineOrderDrag,
    finishTimelineOrderDrag,
  } = useTimelineOrderDrag({
    timelineCols,
    timelineOrientation,
    timelineCellWidth,
    timelineCellHeight,
    zoom: viewScale,
    selectNode,
    openTab,
    runCanvasCommand,
  });

  const connectionPointForNode = (nodeId: string, toward: Point): Point | null => {
    const column = colOf.get(nodeId);
    if (!column) return null;
    const center = cardCenter(column, centerX, rowTop);
    const direction =
      Math.abs(toward.x - center.x) + Math.abs(toward.y - center.y) < 0.01
        ? { x: center.x + 1, y: center.y }
        : toward;
    return rectBoundary(center, column.width / 2, column.height / 2, direction);
  };

  /** Card titles and the shared context menu: both wiring and the board use them. */
  const nodeTitle = (id: string | undefined) =>
    cols.find((c) => c.node.id === id)?.node.title || id || "";
  const { contextMenu, setContextMenu } = useLaneContextMenu();

  const wiring = useGraphWiring({
    graph,
    nodeTitle,
    selectNode,
    setGraphNotice,
    setGraphSelection,
    setContextMenu,
    updateNode,
    updateCanvasLayout,
    runCanvasCommand,
  });


  const connection = useConnectionDrag({
    enabled: isGraph && canEdit,
    graphPointFromClient,
    connectionPointForNode,
    onSelectGate: (gateId) => setGraphSelection({ kind: "logic-gate", id: gateId }),
    connectNodes: wiring.connectNodes,
    connectNodeToLogicGate: wiring.connectNodeToLogicGate,
    connectLogicGateToNode: wiring.connectLogicGateToNode,
  });
  const { state: connectionDrag, hover: connectionHover } = connection;
  const connectionHandlers = connection.handlers;
  const updateConnectionDrag = connectionHandlers.onPointerMove;
  const finishConnectionDrag = connectionHandlers.onPointerUp;
  const startLogicGateConnection = connectionHandlers.startGate;

  const onCardPointerDown = (e: React.PointerEvent<HTMLElement>, id: string) => {
    if (e.button !== 0) return;
    if (isGraph && (e.altKey || gestureMode === "connect")) {
      if (!connectionHandlers.startNode(e, id)) return;
      cardDragHandlers.cancel();
      return;
    }
    cardDragHandlers.onPointerDown(e, id);
  };
  const onCardPointerMove = (e: React.PointerEvent<HTMLElement>) => {
    if (updateConnectionDrag(e)) return;
    if (cardDragHandlers.onPointerMove(e)) return;
  };
  const onCardPointerUp = (e: React.PointerEvent<HTMLElement>, id: string) => {
    if (finishConnectionDrag(e, true)) return;
    cardDragHandlers.onPointerUp(e, id);
  };

  // ----- right-click creation: graph nodes and expected timeline milestones
  const nodeCreation = useNodeCreation({
    planStatusDefinitions,
    createNode,
    setLifecycleStatus,
    setGraphNotice,
    setContextMenu,
  });

  const planMenu = usePlanMenu({
    graph,
    planStatusDefinitions,
    updatePlan,
    updateWorkflowDefinition,
    setContextMenu,
  });

  const {
    planDrag,
    actualDrag,
    startPlanDrag,
    movePlanDrag,
    endPlanDrag,
    startActualDrag,
    moveActualDrag,
    endActualDrag,
  } = useMilestoneDrag({
    graph,
    zoom: viewScale,
    boardRef,
    timelineOrientation,
    planStatusDefinitions,
    customStatuses,
    updatePlan,
    moveStamp,
    selectNode,
    openTab,
    setContextMenu,
    setPlanNotice,
    canEdit,
  });

  const conditions = useNodeConditions({
    graph,
    logicGates,
    contextMenu,
    setContextMenu,
    setGraphSelection,
    setGraphNotice,
    updateNode,
    updateCanvasLayout,
    runCanvasCommand,
  });

  // TimelineGrid asks for one cell's records at a time; everything the cards
  // need is narrowed here and drawn there.
  const renderTimelineCards = (column: Col, date: string) => (
    <TimelineCards
      column={column}
      date={date}
      plans={plansByNodeDay.get(column.node.id)?.get(date) ?? []}
      events={byNodeDay.get(column.node.id)?.get(date) ?? []}
      planStatusDefinitions={planStatusDefinitions}
      customStatuses={customStatuses}
      planDrag={planDrag}
      actualDrag={actualDrag}
      startPlanDrag={startPlanDrag}
      movePlanDrag={movePlanDrag}
      endPlanDrag={endPlanDrag}
      startActualDrag={startActualDrag}
      moveActualDrag={moveActualDrag}
      endActualDrag={endActualDrag}
      selectNode={selectNode}
      openTab={openTab}
      openPlanMenu={planMenu.openPlanMenu}
      nodeTitle={nodeTitle}
    />
  );

  const onGraphContextMenu = (event: React.MouseEvent<HTMLDivElement>) => {
    if (!isGraph) return;
    if (
      (event.target as Element).closest(
        ".col-card, .gate-handle, .edge-hit-target, .edge-vertex",
      )
    ) {
      return;
    }
    event.preventDefault();
    const rect = event.currentTarget.getBoundingClientRect();
    setContextMenu({
      kind: "graph",
      x: event.clientX,
      y: event.clientY,
      ...graphCellAt(
        (event.clientX - rect.left) / viewScale - graphOffsetX,
        (event.clientY - rect.top) / viewScale - graphOffsetY,
      ),
    });
  };

  /**
   * Anchor for the toolbar's "新增節點" button: the middle of what the user is
   * currently looking at, so the node appears where they are working rather
   * than at the origin [B-03].
   */
  const viewportCreationAnchor = (): Extract<LaneContextMenu, { kind: "graph" }> => {
    const board = boardRef.current;
    const rect = board?.getBoundingClientRect();
    const x = rect ? rect.left + rect.width / 2 : 240;
    const y = rect ? rect.top + Math.min(rect.height / 2, 320) : 200;
    if (!board || !isGraph) return { kind: "graph", x, y, col: 0, row: 0 };
    const contentX =
      (board.scrollLeft + board.clientWidth / 2) / viewScale - graphOffsetX;
    const contentY =
      (board.scrollTop + board.clientHeight / 2) / viewScale - graphOffsetY;
    return {
      kind: "graph",
      x,
      y,
      ...graphCellAt(contentX, contentY),
    };
  };

  const openNodeCreationMenu = (
    menu: Extract<LaneContextMenu, { kind: "graph" }>,
  ) => {
    nodeCreation.setNodeCreateTitle("");
    nodeCreation.setNodePlanDates({});
    nodeCreation.setNodeCustomPlanDrafts([]);
    nodeCreation.setNodeCreateError(null);
    setContextMenu({ ...menu, kind: "node-create" });
  };

  const {
    createCanvasGroup,
    createCanvasAnnotation,
    createGroupFromSelection,
    updateCanvasDecoration,
    deleteCanvasDecoration,
  } = useCanvasDecorations({
    graph,
    cols,
    selectedGraphNodeIDs,
    runCanvasCommand,
    setContextMenu,
    setGraphNotice,
  });


  const fitGraph = (selectionOnly: boolean) => {
    const board = boardRef.current;
    if (!board) return;
    const candidates = selectionOnly
      ? cols.filter((column) => selectedGraphNodeIDs.has(column.node.id))
      : cols;
    const view = fitViewToCards(candidates, centerX, rowTop, {
      width: board.clientWidth,
      height: board.clientHeight,
    });
    if (!view) return;
    // What comes back is the scale the board has to be drawn at, which the node
    // scale already contributes to — so the user's zoom is what is left over,
    // and the scroll has to be written in terms of the two together.
    const nextZoom = clampZoom(view.zoom / graphNodeScale);
    const nextViewScale = nextZoom * graphNodeScale;
    setZoomDirect(nextZoom);
    requestAnimationFrame(() => {
      board.scrollLeft = view.focusX * nextViewScale - board.clientWidth / 2;
      board.scrollTop = view.focusY * nextViewScale - board.clientHeight / 2;
    });
  };

  // A phone at 100% shows about two cards of a 164px-grid board, so the first
  // sight of a project is the whole of it. Once per project, not per mount or
  // per resize: after that first fit the zoom is the user's own. Desktop keeps
  // its opening behaviour — small boards already centre themselves and the
  // toolbar's 全部置中 does this on demand. Waiting for a real measurement
  // matters because the effect runs before useBoardViewport has one; fitGraph
  // writes its scroll in a requestAnimationFrame, so it lands after the
  // remembered opening scroll rather than under it.
  const narrow = useNarrowViewport();
  const autoFitKeyRef = useRef<string | null>(null);
  useEffect(() => {
    if (!isGraph || collapsed || !narrow) return;
    if (boardViewport.width <= 0 || boardViewport.height <= 0) return;
    if (cols.length === 0) return;
    const key = activeProject || "__default__";
    if (autoFitKeyRef.current === key) return;
    autoFitKeyRef.current = key;
    fitGraph(false);
    // eslint-disable-next-line react-hooks/exhaustive-deps -- fitGraph is
    // recreated every render; the guards above decide when a fit happens.
  }, [
    isGraph,
    collapsed,
    narrow,
    boardViewport.width,
    boardViewport.height,
    cols.length,
    activeProject,
  ]);

  // How far out the board is drawn decides how much a card can say. Below these
  // effective scales the secondary lines go before their words shrink past
  // reading size; the CSS keyed on the attribute lives in 04-board.css. One
  // attribute on .board-inner is a single selector invalidation however many
  // cards are up, and both factors of the scale are quantized (zoom to 0.1,
  // node scale to 0.05), so the levels cannot flicker at a boundary.
  const lod =
    !isGraph || viewScale >= 0.8
      ? undefined
      : viewScale >= 0.6
        ? "compact"
        : "minimal";
  const gridVars = {
    // Cards ride the board's CSS zoom, which the node scale is folded into, so
    // their text would grow with the box unless it is divided back out first.
    // What is left is the font scale on its own, applied to every size on the
    // card at once so the steps between title, status and subtitle survive.
    ["--card-font-scale" as string]: `${graphFontScale / graphNodeScale}`,
    // The LOD floor in 04-board.css divides this back out of the CSS zoom to
    // hold the title at a constant on-screen size while the board shrinks.
    ["--view-scale" as string]: `${viewScale}`,
    ["--col-w" as string]: `${isGraph ? COL_W : timelineCellWidth}px`,
    ["--step" as string]: `${isGraph ? ROW_H : timelineCellHeight}px`,
    // The drawn lines have to start where the cells do. The graph centres its
    // content in the viewport, so the lattice sits at a fraction of a cell from
    // the element origin — a grid painted from 0 would be a ruler for a
    // different board, and snapped cards would look untouched.
    ["--grid-origin-x" as string]: `${graphOffsetX}px`,
    ["--grid-origin-y" as string]: `${graphOffsetY}px`,
  };
  const svgH = isGraph
    ? graphCanvasH
    : Math.max(totalH, rowTop(maxRow) + timelineCellHeight) + 40;
  const { previewEnd: connectionPreviewEnd, previewError: connectionPreviewError } =
    connectionPreview({
      connectionDrag,
      logicGates,
      edges: graph?.edges ?? [],
      colOf,
      centerX,
      rowTop,
      graphOffsetX,
      graphOffsetY,
    });

  return (
    <GraphGestureModeProvider value={gestureMode}>
    <section
      className={`lane-wrap ${isGraph ? "graph" : "timeline"}${
        collapsed ? " pane-collapsed" : ""
      }${timelineWidthResizeEdge ? " timeline-cell-resizing" : ""}`}
      aria-label={isGraph ? "關係圖" : "時間軸"}
      style={collapsed ? undefined : paneStyle}
      onPointerDownCapture={onActivate}
      onFocusCapture={onActivate}
    >
      <BoardToolbar
        gestureMode={gestureMode}
        snapGuides={snapGuidesEnabled}
        setSnapGuides={setSnapGuidesEnabled}
        setGestureMode={setGestureMode}
        isGraph={isGraph}
        collapsed={collapsed}
        onToggle={onToggle}
        zoom={zoom}
        zoomAt={zoomAt}
        nodeScale={nodeScale}
        setNodeScale={setNodeScale}
        cardFontScale={cardFontScale}
        setCardFontScale={setCardFontScale}
        resetCardScale={resetCardScale}
        graphToolsOpen={graphToolsOpen}
        setGraphToolsOpen={setGraphToolsOpen}
        analysisOpen={analysisOpen}
        setAnalysisOpen={setAnalysisOpen}
        analysis={analysis}
        fitGraph={fitGraph}
        snapToGrid={snapToGrid}
        setSnapToGrid={setSnapToGrid}
        minimapVisible={minimapVisible}
        setMinimapVisible={setMinimapVisible}
        alignAllToGrid={alignAllToGrid}
        canAlignToGrid={canEdit && cols.length > 0}
        selectedIDs={selectedIDs}
        selectedNodeId={selectedNodeId}
        nodeTitle={nodeTitle}
        openNodeCreationMenu={openNodeCreationMenu}
        viewportCreationAnchor={viewportCreationAnchor}
        timelineOrientation={timelineOrientation}
        changeTimelineOrientation={changeTimelineOrientation}
        timelineDateAxis={timelineDateAxis}
        changeTimelineDateAxis={changeTimelineDateAxis}
        timelineNodeAxis={timelineNodeAxis}
        changeTimelineNodeAxis={changeTimelineNodeAxis}
        timelineVNodeAxis={timelineVNodeAxis}
        changeTimelineVNodeAxis={changeTimelineVNodeAxis}
        timelineVDateAxis={timelineVDateAxis}
        changeTimelineVDateAxis={changeTimelineVDateAxis}
        timelineCellAuto={timelineCellAuto}
        timelineCellWidth={timelineCellWidth}
        timelineCellHeight={timelineCellHeight}
        setTimelineCellSize={setTimelineCellSize}
        useAutoTimelineCellSize={useAutoTimelineCellSize}
        collapseEmpty={collapseEmpty}
        setCollapse={setCollapse}
        graphNotice={graphNotice}
        planNotice={planNotice}
      />
      {/* Every control in it mutates — batch status, assignee, copy, move —
        * so for a read-only session the whole strip goes. */}
      {canEdit && isGraph && !collapsed && selectedIDs.length > 0 && (
        <GraphBatchToolbar
          graph={graph}
          customStatuses={customStatuses}
          selectableStatuses={selectableStatuses}
          selectedIDs={selectedIDs}
          shortcutBusy={shortcutBusy}
          setShortcutBusy={setShortcutBusy}
          applyBatchStatus={applyBatchStatus}
          applyBatchAssignee={applyBatchAssignee}
          createGroupFromSelection={createGroupFromSelection}
          duplicateNode={duplicateNode}
          deleteNodes={deleteNodes}
          setGraphSelection={setGraphSelection}
          onSelectedNodeChange={onSelectedNodeChange}
        />
      )}
      {isGraph && !collapsed && graphToolsOpen && (
        <GraphToolsPanel
          graph={graph}
          customStatuses={customStatuses}
          selectableStatuses={selectableStatuses}
          viewFilter={viewFilter}
          setViewFilter={setViewFilter}
          savedViewName={savedViewName}
          setSavedViewName={setSavedViewName}
          saveCurrentView={saveCurrentView}
          applySavedView={applySavedView}
          removeSavedView={removeSavedView}
        />
      )}
      {isGraph && !collapsed && analysisOpen && (
        <GraphAnalysisPanel
          analysis={analysis}
          selectNode={selectNode}
          nodeTitle={nodeTitle}
        />
      )}
      {collapsed ? null : (
      <div
        ref={boardRef}
        className={`board pan-enabled${panning ? " panning" : ""}`}
        onScroll={handleBoardScroll}
        onPointerDown={onBoardPointerDown}
        onPointerMove={onBoardPointerMove}
        onPointerUp={endBoardPan}
        onPointerCancel={endBoardPan}
        onLostPointerCapture={() => {
          panHandlers.cancel();
          marqueeHandlers.cancel();
        }}
      >
        <div
          className={`board-inner${isGraph ? " graph-bg" : ""}${
            drag?.moved ? " dragging" : ""
          }${
            !isGraph && timelineOrientation === "horizontal"
              ? " timeline-horizontal"
              : ""
          }${
            !isGraph && timelineOrientation === "vertical"
              ? ` timeline-vertical${
                  timelineVNodeAxis === "bottom" ? " vnode-bottom" : ""
                }${timelineVDateAxis === "left" ? " vdate-left" : ""}`
              : ""
          }`}
          style={{
            width:
              !isGraph && timelineOrientation === "horizontal"
                ? horizontalTimelineW
                : boardW,
            minHeight:
              !isGraph && timelineOrientation === "horizontal"
                ? horizontalTimelineH
                : svgH,
            zoom: viewScale,
            ...gridVars,
          }}
          data-lod={lod}
          onContextMenu={onGraphContextMenu}
        >
          {isGraph && marquee && (
            <div
              className="graph-marquee"
              style={{
                left: Math.min(marquee.start.x, marquee.current.x),
                top: Math.min(marquee.start.y, marquee.current.y),
                width: Math.abs(marquee.current.x - marquee.start.x),
                height: Math.abs(marquee.current.y - marquee.start.y),
              }}
              aria-hidden
            />
          )}
          {/* ---------- timeline: lane header + expected/actual lifecycle cards ---------- */}
          {!isGraph && (
            <TimelineGrid
              timelineOrientation={timelineOrientation}
              timelineDateAxis={timelineDateAxis}
              timelineNodeAxis={timelineNodeAxis}
              timelineCellWidth={timelineCellWidth}
              timelineCellHeight={timelineCellHeight}
              setTimelineCellSize={setTimelineCellSize}
              useAutoTimelineCellSize={useAutoTimelineCellSize}
              setTimelineWidthResizeEdge={setTimelineWidthResizeEdge}
              collapseEmpty={collapseEmpty}
              setCollapse={setCollapse}
              zoom={viewScale}
              boardW={boardW}
              horizontalTimelineW={horizontalTimelineW}
              horizontalTimelineH={horizontalTimelineH}
              horizontalNowX={horizontalNowX}
              nowOffset={nowOffset}
              timelineCols={timelineCols}
              rowItems={rowItems}
              statuses={statuses}
              customStatuses={customStatuses}
              userNames={userNames}
              activeTab={activeTab}
              todayKey={todayKey}
              todayStart={todayStart}
              dayKey={dayKey}
              renderTimelineCards={renderTimelineCards}
              openPlanMenu={planMenu.openPlanMenu}
              openTab={openTab}
              selectNode={selectNode}
              timelineOrderDrag={timelineOrderDrag}
              startTimelineOrderDrag={startTimelineOrderDrag}
              moveTimelineOrderDrag={moveTimelineOrderDrag}
              finishTimelineOrderDrag={finishTimelineOrderDrag}
              planDrag={planDrag}
              actualDrag={actualDrag}
              selectedNodeId={selectedNodeId}
              now={now}
              timelineOrderDragRef={timelineOrderDragRef}
              updateTimelineOrderDrag={updateTimelineOrderDrag}
            />
          )}
          {isGraph && (
            <GraphCanvas
              graph={graph}
              cols={cols}
              colOf={colOf}
              gates={gates}
              logicGates={logicGates}
              statuses={statuses}
              customStatuses={customStatuses}
              userNames={userNames}
              activeTab={activeTab}
              errorNodes={errorNodes}
              violationNodeIDs={violationNodeIDs}
              analysis={analysis}
              matchesViewFilter={matchesViewFilter}
              boardW={boardW}
              svgH={svgH}
              zoom={viewScale}
              graphOffsetX={graphOffsetX}
              graphOffsetY={graphOffsetY}
              graphObstacles={graphObstacles}
              centerX={centerX}
              rowTop={rowTop}
              snapEnabled={snapGuidesEnabled}
              snapBoxes={snapBoxes}
              // One gesture runs at a time, so whichever is live owns the lines.
              snapGuides={drag?.guides ?? cardResize?.guides ?? decorationGuides}
              onSnapGuides={setDecorationGuides}
              peerGhosts={peerGhosts}
              peerPointers={peerPointers}
              drag={drag}
              cardDragHandlers={cardDragHandlers}
              cardResize={cardResize}
              cardResizeHandlers={cardResizeHandlers}
              connectionDrag={connectionDrag}
              connectionHover={connectionHover}
              connectionHandlers={connectionHandlers}
              connectionPreviewEnd={connectionPreviewEnd}
              connectionPreviewError={connectionPreviewError}
              updateConnectionDrag={updateConnectionDrag}
              finishConnectionDrag={finishConnectionDrag}
              startLogicGateConnection={startLogicGateConnection}
              onCardPointerDown={onCardPointerDown}
              onCardPointerMove={onCardPointerMove}
              onCardPointerUp={onCardPointerUp}
              graphSelection={graphSelection}
              setGraphSelection={setGraphSelection}
              selectedGraphNodeIDs={selectedGraphNodeIDs}
              selectNode={selectNode}
              onSelectedNodeChange={onSelectedNodeChange}
              setContextMenu={setContextMenu}
              openTab={openTab}
              openConditionMenu={conditions.openConditionMenu}
              updateCanvasDecoration={updateCanvasDecoration}
              deleteCanvasDecoration={deleteCanvasDecoration}
              saveGatePlacement={saveGatePlacement}
              saveLogicGatePosition={saveLogicGatePosition}
              deleteStandaloneLogicGate={wiring.deleteStandaloneLogicGate}
              getWireVertices={getWireVertices}
              saveWireVertices={saveWireVertices}
              updateNode={updateNode}
              setGraphNotice={setGraphNotice}
            />
          )}
          {cols.length === 0 && (
            <EmptyState
              title="尚無節點"
              description="建立第一個節點後，即可安排依賴與時間規劃。"
              action={
                <button
                  type="button"
                  onClick={(event) =>
                    openNodeCreationMenu({
                      kind: "graph",
                      x: event.clientX || 24,
                      y: event.clientY || 88,
                      col: 0,
                      row: 0,
                    })
                  }
                >
                  <IconPlus size={13} />
                  新增節點
                </button>
              }
            />
          )}
        </div>
      </div>
      )}
      {isGraph && !collapsed && minimapVisible && cols.length > 0 && (
        <GraphMinimap
          cols={cols}
          selectedGraphNodeIDs={selectedGraphNodeIDs}
          boardRef={boardRef}
          boardW={boardW}
          svgH={svgH}
          zoom={viewScale}
          boardScroll={boardScroll}
          boardViewport={boardViewport}
          centerX={centerX}
          rowTop={rowTop}
        />
      )}

      {contextMenu && (
        <LaneContextMenuLayer
          contextMenu={contextMenu}
          setContextMenu={setContextMenu}
          graph={graph}
          nodeTitle={nodeTitle}
          logicGates={logicGates}
          planStatusDefinitions={planStatusDefinitions}
          openTab={openTab}
          selectNode={selectNode}
          duplicateNode={duplicateNode}
          deleteNode={deleteNode}
          openNodeCreationMenu={openNodeCreationMenu}
          createCanvasGroup={createCanvasGroup}
          createCanvasAnnotation={createCanvasAnnotation}
          setEdgeStyles={setEdgeStyles}
          wiring={wiring}
          conditions={conditions}
          nodeCreation={nodeCreation}
          planMenu={planMenu}
        />
      )}

    </section>
    </GraphGestureModeProvider>
  );
}
