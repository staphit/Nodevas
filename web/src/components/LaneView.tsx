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
import { useI18n } from "../i18n";
import { localizeIssue } from "../issueMessages";
import { IconPlus } from "../icons";
import { EmptyState } from "./InteractionPrimitives";
import { analyzeGraph } from "../analysis";
import { BoardToolbar } from "./BoardToolbar";
import { GraphBatchToolbar, NodeClipboardNotice } from "./GraphBatchToolbar";
import { GraphToolsPanel, activeFilterCount } from "./GraphToolsPanel";
import { TimelineGrid } from "./timeline/TimelineGrid";
import { TimelineCards } from "./timeline/TimelineCards";
import { GAP_H, HORIZONTAL_GAP_W, useTimelineLayout } from "./timeline/useTimelineLayout";
import { useTimelineDays } from "./timeline/useTimelineDays";
import { TIMELINE_CULL_MIN_LANES } from "./timeline/timelineCulling";
import { useTimelineCellSize } from "./timeline/useTimelineCellSize";
import { useMilestoneDrag } from "./timeline/useMilestoneDrag";
import { usePlanMenu } from "./timeline/usePlanMenu";
import { useNarrowViewport } from "./TopbarOverflow";
import { GraphCanvas } from "./canvas/GraphCanvas";
import { paintLiftedCards } from "./canvas/GraphCard";
import { GraphMinimap } from "./canvas/GraphMinimap";
import { ProblemsPanel, problemCount } from "./canvas/ProblemsPanel";
import { useCardResize } from "./canvas/useCardResize";
import { useCardDrag, type CardDragState } from "./canvas/useCardDrag";
import {
  rectOfColumn,
  snapBoxesOfColumns,
  type SnapBox,
  type SnapGuide,
} from "./canvas/snapping";
import { useCanvasPan } from "./canvas/useCanvasPan";
import { useConnectionDrag } from "./canvas/useConnectionDrag";
import { useMarquee } from "./canvas/useMarquee";
import { useViewWindow } from "./canvas/useViewWindow";
import {
  boardViewKey,
  recallBoardView,
  recallTimelineView,
  rememberTimelineView,
  timelineViewKey,
} from "./canvas/boardViewMemory";
import { CULL_MIN_CARDS, cardRect, rectsIntersect } from "./canvas/viewportCulling";
import {
  GraphGestureModeProvider,
  type GraphGestureMode,
} from "./canvas/gestureMode";
import { useGraphWiring } from "./canvas/useGraphWiring";
import { useBoardShortcuts } from "./canvas/useBoardShortcuts";
import { useNodeCreation } from "./canvas/useNodeCreation";
import { useRevealOpenedCard } from "./canvas/useRevealOpenedCard";
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
  type Rect,
} from "./canvas/geometry";
import {
  boardMetrics,
  fitViewToCards,
  graphCellAt,
  nearestFreeGraphCell,
  graphObstacleRects,
  GRAPH_PAN_BUFFER_X,
  GRAPH_PAN_BUFFER_Y,
  openingScroll,
  RULER_W,
  timelineOpeningScroll,
} from "./lane/boardGeometry";
import { connectionPreview } from "./lane/connectionPreview";
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
import { useMinuteClock } from "./lane/useMinuteClock";
import { visibleBoardArea } from "./lane/boardVisibleArea";
import { VIEWPORT_MARGIN } from "./floatingPanel";

/** The new-node form's widest (12-timeline.css `.node-create-menu`). */
const NODE_CREATE_FORM_W = 380;

/** A horizontal timeline's sticky date header (TimelineGrid's HORIZONTAL_HEADER_H). */
const TIMELINE_DATE_HEADER_H = 48;

export type LaneContextMenu =
  | { kind: "plan"; x: number; y: number; nodeId: string; date: string }
  | {
      kind: "graph";
      x: number;
      y: number;
      col: number;
      row: number;
      /** The type a new node starts as; Task when absent. */
      nodeKind?: string;
    }
  | {
      kind: "node-create";
      x: number;
      y: number;
      col: number;
      row: number;
      nodeKind?: string;
    }
  | {
      kind: "edge";
      x: number;
      y: number;
      edges: { from: string; to: string }[];
    }
  | { kind: "logic-gate"; x: number; y: number; gateId: string }
  /** The node's short menu: open, agents, add dependency, duplicate, trash. */
  | { kind: "node-actions"; x: number; y: number; nodeId: string }
  /** The node's dependency editor, from "Add dependency…" or a wire's gate. */
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

/**
 * The empty-state hint over a board that has groups but no cards: its card
 * plus the room left between it and the groups' tops, and the margin
 * .ui-empty-state keeps above it, in board pixels.
 */
const EMPTY_HINT_ROOM = 96;
const EMPTY_HINT_MARGIN = 48;

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
  const { t, language } = useI18n();
  const isGraph = variant === "graph";
  const graph = useApp((s) => s.graph);
  const statuses = useApp((s) => s.statuses);
  const runState = useApp((s) => s.runState);
  const issues = useApp((s) => s.issues);
  const openTabInStore = useApp((s) => s.openTab);
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
  const graphProject = useApp((s) => s.graphProject);
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
  // The Problems side panel: the schedule analysis and the validation
  // warnings, beside the board instead of on top of it.
  const [problemsOpen, setProblemsOpen] = useState(false);
  const problemsButtonRef = useRef<HTMLButtonElement>(null);
  const closeDrawer = useApp((s) => s.closeDrawer);
  // The panel and the node drawer answer different questions and both take
  // the right-hand side, so one opening closes the other: Problems closes the
  // drawer here, and picking a problem (which opens the drawer) closes
  // Problems below.
  const toggleProblems = (update: (open: boolean) => boolean) => {
    const next = update(problemsOpen);
    if (next && useApp.getState().activeTab !== null) closeDrawer();
    setProblemsOpen(next);
  };
  // Closed from inside (× or Escape), the keyboard goes back to the button
  // that opened it rather than to the top of the page.
  const closeProblems = () => {
    setProblemsOpen(false);
    problemsButtonRef.current?.focus();
  };
  // And the other way: a node opened any other way (Enter or a double click
  // on a card, the palette) takes the side back from Problems.
  const lastActiveTab = useRef(activeTab);
  useEffect(() => {
    if (activeTab !== null && activeTab !== lastActiveTab.current) setProblemsOpen(false);
    lastActiveTab.current = activeTab;
  }, [activeTab]);
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
  // Every way the board opens a node — a card, Enter, its menu, a row in
  // Problems — keeps the card out from under the drawer that opens with it.
  const { openTab, revealIfHidden } = useRevealOpenedCard({
    enabled: isGraph && !collapsed,
    boardRef,
    openTab: openTabInStore,
  });
  // Station and Settings unmount the board, so the view this project was left
  // at in this session — if any — is read once, on mount.
  const [rememberedView] = useState(() =>
    isGraph ? recallBoardView(boardViewKey(activeProject)) : undefined,
  );
  const canvasPan = useCanvasPan({
    boardRef,
    collapsed,
    displayScale: graphNodeScale,
    initialZoom: rememberedView?.zoom,
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
  // The project menu swaps the project under a board that stays mounted, so
  // the zoom follows it here: the new project's remembered zoom, or 100% for
  // one this session has not shown. Set during render, so the commit that
  // shows the new project is already drawn at its own zoom, and the opening
  // scroll useBoardViewport restores below lands at that scale rather than
  // being re-centred from the old one.
  // Whether the project on show had been shown before it came up, which is
  // also what spares a phone's board a second first-sight fit below.
  // `staleGraph` is the graph the store held when the project changed under
  // a mounted board: the old project's, kept until the new one loads. Not
  // when the board came up before the workspace named any project: what was
  // loaded then is the server's active project, the one now being named.
  // `staleRunState` is the same for the run state, which loads beside the
  // graph and can land after it.
  const [shownProject, setShownProject] = useState(() => ({
    project: activeProject,
    remembered: rememberedView !== undefined,
    staleGraph: null as typeof graph,
    staleRunState: null as typeof runState,
  }));
  if (shownProject.project !== activeProject) {
    const remembered = isGraph ? recallBoardView(boardViewKey(activeProject)) : undefined;
    setShownProject({
      project: activeProject,
      remembered: remembered !== undefined,
      staleGraph: shownProject.project ? graph : null,
      staleRunState: shownProject.project ? runState : null,
    });
    if (isGraph) setZoomDirect(remembered?.zoom ?? 1);
  }
  // Whether the graph on show is this project's, so anything framing its
  // cards can wait for them: a different graph has arrived since the switch,
  // or the load says whose it is. (graphProject alone cannot say: the first
  // load can land before the workspace names the active project.)
  const graphReady =
    graph !== null &&
    shownProject.project === activeProject &&
    (graph !== shownProject.staleGraph || graphProject === activeProject);
  // The timeline's calendar is built from the records too, so its opening
  // also waits for this project's run state: built from the last project's,
  // it opened on the last project's days.
  const runStateReady =
    shownProject.staleRunState === null || runState !== shownProject.staleRunState;
  // Set once the cards are laid out below; the opening scroll reads it.
  const openingCardsRef = useRef<() => Rect[]>(() => []);
  const timelineOpeningRef = useRef(() => ({ left: 0, top: 0 }));
  // The timeline view whose opening has landed; its scroll is recorded below.
  const timelineOpenedRef = useRef("");
  // The minimap floats over the board's bottom-right corner; the opening
  // keeps the cards out from under it when they have room to be.
  const minimapRef = useRef<HTMLDivElement>(null);
  const openingCornerRef = useRef(() => {
    const board = boardRef.current;
    const map = minimapRef.current;
    if (!board || !map) return null;
    const view = board.getBoundingClientRect();
    const rect = map.getBoundingClientRect();
    const right = view.left + board.clientLeft + board.clientWidth;
    const bottom = view.top + board.clientTop + board.clientHeight;
    if (rect.width <= 0 || rect.left >= right || rect.top >= bottom) return null;
    // A little room between a card and the map's edge.
    return { width: right - rect.left + 8, height: bottom - rect.top + 8 };
  });
  const { boardViewport } = useBoardViewport({
    boardRef,
    collapsed,
    isGraph,
    activeProject,
    zoom: viewScale,
    userZoom: zoom,
    openingCards: openingCardsRef,
    openingCorner: openingCornerRef,
    timelineOpening: timelineOpeningRef,
    timelineOrientation,
    contentReady: isGraph ? graphReady : graphReady && runStateReady,
  });

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
  // The current-time line is labelled to the minute, so it keeps its own
  // clock; a day it moves into is a new today for the calendar too.
  const clock = useMinuteClock(!isGraph && !collapsed);
  useEffect(() => {
    if (dayKey(clock) !== dayKey(now)) setNow(clock);
  }, [clock]);

  const errorNodes = useMemo(() => {
    const set = new Set<string>();
    for (const i of issues) if (i.severity === "error" && i.nodeId) set.add(i.nodeId);
    return set;
  }, [issues]);
  // The hub writes its findings in English; the panel reads them in the UI's language.
  const localizedIssues = useMemo(
    () => issues.map((issue) => localizeIssue(issue, language)),
    [issues, language],
  );

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
  // A large graph mounts only the cards near the view. Read after
  // useBoardViewport's opening scroll, which lands in the same commit.
  // A large timeline does the same with its lanes (timelineCulling).
  const viewWindow = useViewWindow({
    boardRef,
    enabled:
      !collapsed &&
      (isGraph
        ? cols.length >= CULL_MIN_CARDS
        : timelineCols.length >= TIMELINE_CULL_MIN_LANES),
    zoom: viewScale,
    viewport: boardViewport,
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
    onFrame: (live) => paintDrag(live),
    zoom: viewScale,
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
      setGraphNotice({ text: t("lane.nodesMoved", { count }), kind: "ok" });
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
      setGraphNotice({ text: t("lane.alreadyAligned"), kind: "ok" });
      return;
    }
    runCanvasCommand({ type: "canvas.moveNodes", positions });
    setGraphNotice({ text: t("lane.nodesAligned", { count: moved }), kind: "ok" });
  };
  // ----- live drag. The offset changes on every pointermove and goes nowhere
  // near React: each animation frame writes it to the board as two custom
  // properties, which the lifted cards' transform reads. The same frame tells
  // the peers, who see it as a ghost; where the cards land arrives afterwards
  // as a graph op.
  const boardInnerRef = useRef<HTMLDivElement>(null);
  const reportDrag = useApp((state) => state.reportDrag);
  const lastDragSentAt = useRef(0);
  const dragReported = useRef(false);
  const paintDrag = (live: CardDragState) => {
    paintLiftedCards(boardInnerRef.current, live.ids, live.dx, live.dy);
    if (!isGraph || !live.moved) return;
    // About thirty a second: enough to read as motion, far under the socket's
    // stream budget, and the gesture is redrawn locally either way.
    const now = Date.now();
    if (now - lastDragSentAt.current < 33) return;
    lastDragSentAt.current = now;
    dragReported.current = true;
    reportDrag(live.ids, live.dx, live.dy, true);
  };
  useEffect(() => {
    if (drag) return;
    // One last message so the ghost goes away even if the drop changed nothing.
    if (dragReported.current) {
      dragReported.current = false;
      reportDrag([], 0, 0, false);
    }
  }, [drag, reportDrag]);

  // A pointer on the board, in the same offset-free space the ghosts use, so a
  // window scrolled somewhere else still draws it over the right card. Other
  // people's pointers and ghosts are drawn by PeerOverlay, which subscribes to
  // them itself: the board does not re-render for somebody else's mouse.
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
    // The graph's scroll is read where it is used — the culling window and the
    // minimap frame each follow it by frame — so a pan renders no board.
    if (isGraph) return;
    onBoardScroll(event);
  };

  const { state: marquee, handlers: marqueeHandlers } = useMarquee({
    enabled: isGraph,
    withoutModifier: gestureMode === "marquee",
    pointFromClient: graphPointFromClient,
    // A culled board has no element for most cards; the layout knows them all.
    nodesInRect: viewWindow
      ? (rect) =>
          cols.flatMap((column) =>
            rectsIntersect(cardRect(column, centerX, rowTop), rect) ? [column.node.id] : [],
          )
      : undefined,
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
        text: t("lane.edgesSelected", { count: edges.length }),
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
    ((clock.getHours() * 60 + clock.getMinutes()) / (24 * 60)) *
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

  // Groups and annotations are content too: a template that is nothing but
  // four quadrant groups opened on a corner of the first one. Over a board
  // with no cards the empty-state hint is part of what the opening frames,
  // set just above the decorations so it does not cover their titles.
  const decorationRects: Rect[] = isGraph
    ? [...(graph?.ui?.groups ?? []), ...(graph?.ui?.annotations ?? [])].map((item) => ({
        left: item.x + graphOffsetX,
        top: item.y + graphOffsetY,
        right: item.x + graphOffsetX + item.width,
        bottom: item.y + graphOffsetY + item.height,
      }))
    : [];
  const emptyHintRect: Rect | null =
    cols.length === 0 && decorationRects.length > 0
      ? {
          left: Math.min(...decorationRects.map((rect) => rect.left)),
          right: Math.max(...decorationRects.map((rect) => rect.right)),
          top: Math.min(...decorationRects.map((rect) => rect.top)) - EMPTY_HINT_ROOM,
          bottom: Math.min(...decorationRects.map((rect) => rect.top)),
        }
      : null;
  const openingContent = (): Rect[] =>
    isGraph
      ? [
          ...cols.map((column) => cardRect(column, centerX, rowTop)),
          ...decorationRects,
          ...(emptyHintRect ? [emptyHintRect] : []),
        ]
      : [];
  openingCardsRef.current = openingContent;
  // The timeline comes back where this project's was left — its scroll is a
  // place in this project's calendar, never another's — and a project it has
  // not shown opens on today (timelineOpeningScroll).
  const timelineKey = timelineViewKey(activeProject, timelineOrientation);
  const timelineDaySize =
    timelineOrientation === "horizontal" ? timelineCellWidth : timelineCellHeight;
  timelineOpeningRef.current = () => {
    timelineOpenedRef.current = timelineKey;
    const horizontal = timelineOrientation === "horizontal";
    const remembered = recallTimelineView(timelineKey);
    // Days collapsed into gaps have no fixed size, and a different day size or
    // zoom is a different layout: those open afresh.
    if (
      remembered &&
      !collapseEmpty &&
      remembered.daySize === timelineDaySize &&
      remembered.scale === viewScale
    ) {
      // The range may have grown into the past since: the same day is further on.
      const shift = days.findIndex((day) => dayKey(day) === remembered.firstDay);
      if (shift >= 0) {
        const delta = shift * timelineDaySize * viewScale;
        return horizontal
          ? { left: remembered.left + delta, top: remembered.top }
          : { left: remembered.left, top: remembered.top + delta };
      }
    }
    const contentDays = new Set<string>();
    for (const byDay of [...byNodeDay.values(), ...plansByNodeDay.values()]) {
      for (const [day, items] of byDay) if (items.length > 0) contentDays.add(day);
    }
    // Each lane's days with a record or a plan, in lane order, so the lane
    // axis opens on the ones with something on the opening day.
    const lanes = timelineCols.map(
      (column) =>
        new Set(
          [byNodeDay.get(column.node.id), plansByNodeDay.get(column.node.id)].flatMap((byDay) =>
            byDay ? [...byDay].flatMap(([day, items]) => (items.length > 0 ? [day] : [])) : [],
          ),
        ),
    );
    return timelineOpeningScroll({
      days,
      rowItems,
      todayKey,
      contentDays,
      orientation: timelineOrientation,
      daySize: horizontal ? timelineCellWidth : timelineCellHeight,
      gapSize: horizontal ? HORIZONTAL_GAP_W : GAP_H,
      viewport: boardViewport,
      zoom: viewScale,
      lanes,
      laneSize: horizontal ? timelineCellHeight : timelineCellWidth,
      // The sticky date ruler beside the lanes (vertical) or the date header
      // above or below them (horizontal: TimelineGrid's 48px).
      laneInset: horizontal ? TIMELINE_DATE_HEADER_H : RULER_W,
    });
  };

  // Recorded as the timeline scrolls, once its opening has landed for this
  // project: before that the scroll is still the last project's.
  const recordTimelineRef = useRef(() => undefined as void);
  recordTimelineRef.current = () => {
    const board = boardRef.current;
    if (isGraph || collapsed || !board || timelineOpenedRef.current !== timelineKey) return;
    rememberTimelineView(timelineKey, {
      left: board.scrollLeft,
      top: board.scrollTop,
      firstDay: days.length > 0 ? dayKey(days[0]) : "",
      daySize: timelineDaySize,
      scale: viewScale,
    });
  };
  useEffect(() => {
    const board = boardRef.current;
    if (!board || isGraph || collapsed) return;
    const onScroll = () => recordTimelineRef.current();
    board.addEventListener("scroll", onScroll, { passive: true });
    return () => board.removeEventListener("scroll", onScroll);
  }, [collapsed, isGraph]);

  useBoardReveal({
    isGraph,
    collapsed,
    boardRef,
    graph,
    colOf,
    timelineColOf,
    timelineCellWidth,
    timelineCellHeight,
    timelineOrientation,
    timelineDateAxis,
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
    enabled: isGraph,
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
    // A card made where the reader is looking can still land out of sight:
    // under the drawer, or left of column 0's place on a narrow board.
    createNode: async (node, options) => {
      const id = await createNode(node, options);
      revealIfHidden(id);
      return id;
    },
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
   * than at the origin [B-03]. What they are looking at is the board less
   * the drawer laid over it: the board's own middle can be under the drawer,
   * and the form and the card with it.
   */
  const viewportCreationAnchor = (): Extract<LaneContextMenu, { kind: "graph" }> => {
    const board = boardRef.current;
    if (!board) return { kind: "graph", x: 240, y: 200, col: 0, row: 0 };
    const rect = board.getBoundingClientRect();
    const visible = visibleBoardArea(board);
    const centreX = (visible.left + visible.right) / 2;
    // The form opens with its corner here: as near the middle as lets all of
    // it fit in what is showing, never past its left edge.
    const x = Math.max(
      visible.left + VIEWPORT_MARGIN,
      Math.min(centreX, visible.right - NODE_CREATE_FORM_W - VIEWPORT_MARGIN),
    );
    const y = visible.top + Math.min((visible.bottom - visible.top) / 2, 320);
    if (!isGraph) return { kind: "graph", x, y, col: 0, row: 0 };
    const contentX = (board.scrollLeft + centreX - rect.left) / viewScale - graphOffsetX;
    const contentY =
      (board.scrollTop + (visible.top + visible.bottom) / 2 - rect.top) / viewScale -
      graphOffsetY;
    return {
      kind: "graph",
      x,
      y,
      // Off any card already there: pressing the button twice without
      // touching the board must not put the second card on the first.
      ...nearestFreeGraphCell(graphCellAt(contentX, contentY), cols),
    };
  };

  const openNodeCreationMenu = (
    menu: Extract<LaneContextMenu, { kind: "graph" }>,
  ) => {
    nodeCreation.setNodeCreateTitle("");
    nodeCreation.setNodePlanDates({});
    nodeCreation.setNodeCustomPlanDrafts([]);
    nodeCreation.setNodeCreateError(null);
    nodeCreation.setNodeCreateKind(menu.nodeKind || "task");
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


  // `opening` is a phone's first sight of the board: a board that does not
  // fit even at the smallest zoom is then framed the way a desktop opens it
  // (openingScroll) — on the card nearest its top-left corner — rather than
  // centred on the middle of its bounds, which on a wide, mostly shallow
  // board is empty grid with the top card row cut off.
  const fitGraph = (selectionOnly: boolean, opening = false) => {
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
    // A graph narrower than the view is drawn centred in it, and how much
    // room that leaves depends on the zoom: the focus moves with the origin,
    // so it is placed against the origin the new zoom draws at.
    const next = boardMetrics({
      isGraph,
      maxCol,
      maxRow,
      timelineColumnCount: timelineCols.length,
      timelineCellWidth,
      boardViewport,
      zoom: nextViewScale,
    });
    const shiftX = next.graphOffsetX - graphOffsetX;
    const shiftY = next.graphOffsetY - graphOffsetY;
    if (opening) {
      const viewport = { width: board.clientWidth, height: board.clientHeight };
      const scroll = openingScroll(
        openingContent().map((rect) => ({
          left: rect.left + shiftX,
          right: rect.right + shiftX,
          top: rect.top + shiftY,
          bottom: rect.bottom + shiftY,
        })),
        nextViewScale,
        viewport,
      );
      canvasPan.zoomOnto(nextZoom, {
        x: (scroll.left + viewport.width / 2) / nextViewScale,
        y: (scroll.top + viewport.height / 2) / nextViewScale,
      });
      return;
    }
    canvasPan.zoomOnto(nextZoom, {
      x: view.focusX + shiftX,
      y: view.focusY + shiftY,
    });
  };

  // A phone at 100% shows about two cards of a 164px-grid board, so the first
  // sight of a project is the whole of it. Once per project, not per mount or
  // per resize: after that first fit the zoom is the user's own. Desktop keeps
  // its opening behaviour — the opening scroll frames the cards and the
  // toolbar's 全部置中 does this on demand. Waiting for a real measurement
  // matters because the effect runs before useBoardViewport has one; fitGraph
  // writes its scroll with the commit that draws its zoom (or at once when
  // the zoom stays), so it lands after the opening scroll rather than under it.
  const narrow = useNarrowViewport();
  // A board this session already showed has been through its first sight:
  // coming back from Station or Settings, or to it from another project in
  // the project menu, keeps the user's zoom, not a refit.
  const autoFitKeyRef = useRef<string | null>(null);
  useEffect(() => {
    if (!isGraph || collapsed || !narrow || !graphReady) return;
    if (boardViewport.width <= 0 || boardViewport.height <= 0) return;
    if (cols.length === 0) return;
    const key = boardViewKey(activeProject);
    if (autoFitKeyRef.current === key) return;
    autoFitKeyRef.current = key;
    if (shownProject.remembered) return;
    fitGraph(false, true);
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
    graphReady,
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

  const emptyHint = cols.length === 0 && (
    // The graph opens scrolled to its pan buffer (useBoardViewport), so
    // the hint is placed in that opening view rather than at the
    // surface's top-left corner, which is off screen. The timeline
    // opens on today, far down or across its calendar: the hint comes
    // ahead of the calendar, stuck to the pane's top-left and taking
    // no room, so it is in the pane wherever the calendar is scrolled.
    <div
      className="board-empty"
      style={
        isGraph
          ? {
              position: "absolute",
              // Over decorations: across the opening view, just above them.
              left: emptyHintRect
                ? openingScroll(openingContent(), viewScale, boardViewport).left / viewScale
                : GRAPH_PAN_BUFFER_X,
              top: emptyHintRect
                ? emptyHintRect.top - EMPTY_HINT_MARGIN
                : GRAPH_PAN_BUFFER_Y,
              width:
                boardViewport.width > 0
                  ? boardViewport.width / viewScale
                  : undefined,
            }
          : {
              position: "sticky",
              top: 0,
              left: 0,
              height: 0,
              // Its own formatting context: the message's top margin would
              // otherwise collapse through it and push the calendar down.
              display: "flow-root",
              // Over the sticky date header and the lane heads.
              zIndex: 14,
              width:
                boardViewport.width > 0
                  ? boardViewport.width / viewScale
                  : undefined,
            }
      }
    >
      <EmptyState
        title={t("lane.emptyTitle")}
        description={t("lane.emptyDescription")}
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
            {t("nodeCreate.create")}
          </button>
        }
      />
    </div>
  );

  return (
    <GraphGestureModeProvider value={gestureMode}>
    <section
      className={`lane-wrap ${isGraph ? "graph" : "timeline"}${
        collapsed ? " pane-collapsed" : ""
      }${timelineWidthResizeEdge ? " timeline-cell-resizing" : ""}`}
      aria-label={isGraph ? t("lane.graph") : t("lane.timeline")}
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
        filterPanel={
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
        }
        activeFilters={activeFilterCount(viewFilter)}
        problemCount={problemCount(analysis, issues)}
        problemsOpen={problemsOpen}
        setProblemsOpen={toggleProblems}
        problemsButtonRef={problemsButtonRef}
        fitGraph={fitGraph}
        snapToGrid={snapToGrid}
        setSnapToGrid={setSnapToGrid}
        minimapVisible={minimapVisible}
        setMinimapVisible={setMinimapVisible}
        alignAllToGrid={alignAllToGrid}
        canAlignToGrid={cols.length > 0}
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
      {/* Two or more: one node has its own menu and its drawer. */}
      {isGraph && !collapsed && selectedIDs.length > 1 && (
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
      {/* The clipboard outlives the selection that filled it. */}
      {isGraph && !collapsed && selectedIDs.length <= 1 && <NodeClipboardNotice strip />}
      {collapsed ? null : (
      <div className={`lane-body${isGraph && problemsOpen ? " problems-open" : ""}`}>
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
          ref={boardInnerRef}
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
          {!isGraph && emptyHint}
          {/* ---------- timeline: lane header + expected/actual lifecycle cards ---------- */}
          {!isGraph && (
            <TimelineGrid
              timelineOrientation={timelineOrientation}
              timelineDateAxis={timelineDateAxis}
              timelineNodeAxis={timelineNodeAxis}
              timelineVDateAxis={timelineVDateAxis}
              viewWindow={viewWindow}
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
              now={clock}
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
              viewWindow={viewWindow}
              snapEnabled={snapGuidesEnabled}
              snapBoxes={snapBoxes}
              // One gesture runs at a time, so whichever is live owns the lines.
              snapGuides={drag?.guides ?? cardResize?.guides ?? decorationGuides}
              onSnapGuides={setDecorationGuides}
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
          {isGraph && emptyHint}
        </div>
      </div>
      {isGraph && minimapVisible && cols.length > 0 && (
        <GraphMinimap
          cols={cols}
          selectedGraphNodeIDs={selectedGraphNodeIDs}
          boardRef={boardRef}
          boardW={boardW}
          svgH={svgH}
          zoom={viewScale}
          boardViewport={boardViewport}
          graphOffsetX={graphOffsetX}
          graphOffsetY={graphOffsetY}
          centerX={centerX}
          rowTop={rowTop}
          mapRef={minimapRef}
        />
      )}
      {isGraph && problemsOpen && (
        <ProblemsPanel
          analysis={analysis}
          issues={localizedIssues}
          nodeTitle={nodeTitle}
          onPick={(nodeId) => {
            setProblemsOpen(false);
            selectNode(nodeId);
            void openTab(nodeId).catch(reportError);
          }}
          onClose={closeProblems}
        />
      )}
      </div>
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
          createGroupFromSelection={createGroupFromSelection}
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
