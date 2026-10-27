/**
 * Board toolbar [B-06].
 *
 * The controls above a pane: collapse, primary create action, zoom, graph
 * filters and analysis, timeline orientation and cell size, and the pane's
 * notice line. Presentational — every action arrives as a callback.
 *
 * Two tiers. The header itself keeps what a first board needs — create, fit,
 * filter, and for the timeline its direction — and everything else sits in
 * the `⋯` menu at the end of the row: toggles such as snapping and the
 * minimap, the axis sides, and the sliders for card and cell size.
 */

import { useId, useState } from "react";

import { IconAlignGuides, IconPlus, IconResizeHorizontal } from "../icons";
import type { GraphAnalysis } from "../analysis";
import { InlineNotice } from "./InteractionPrimitives";
import { OverflowMenu, type OverflowMenuItem } from "./menus/OverflowMenu";
import { GRAPH_GESTURE_MODES, type GraphGestureMode } from "./canvas/gestureMode";
import { useCoarsePointer, useTouchCapable } from "./touch";
import type { LaneContextMenu } from "./LaneView";
import { useI18n } from "../i18n";
import { useStageSkinItems } from "../game/stageSkin";

/**
 * How far the graph will stretch its cards and their text away from the sizes
 * the project stores. Both are display-only, so the range is wide enough to be
 * worth having and narrow enough that a board is still recognisable at either
 * end. The step is fine because the control is a slider, not a keyboard nudge.
 */
const GRAPH_SCALE_MIN = 0.5;
const GRAPH_SCALE_MAX = 2;
const GRAPH_SCALE_STEP = 0.05;

const TIMELINE_CELL_MIN_W = 160;
const TIMELINE_CELL_MAX_W = 360;
const TIMELINE_CELL_MIN_H = 72;
const TIMELINE_CELL_MAX_H = 180;

export interface BoardToolbarProps {
  isGraph: boolean;
  collapsed?: boolean;
  onToggle?: () => void;

  zoom: number;
  zoomAt: (zoom: number) => void;

  /** Graph display scale: how large cards and their text are drawn. */
  nodeScale: number;
  setNodeScale: (scale: number) => void;
  cardFontScale: number;
  setCardFontScale: (scale: number) => void;
  resetCardScale: () => void;

  /** Graph tools */
  graphToolsOpen: boolean;
  setGraphToolsOpen: (update: (open: boolean) => boolean) => void;
  analysisOpen: boolean;
  setAnalysisOpen: (update: (open: boolean) => boolean) => void;
  analysis: GraphAnalysis;
  fitGraph: (selectionOnly: boolean) => void;
  snapToGrid: boolean;
  setSnapToGrid: (value: boolean) => void;
  minimapVisible: boolean;
  setMinimapVisible: (value: boolean) => void;
  alignAllToGrid: () => void;
  canAlignToGrid: boolean;
  selectedIDs: string[];
  selectedNodeId?: string | null;
  nodeTitle: (id: string | undefined) => string;
  gestureMode: GraphGestureMode;
  setGestureMode: (mode: GraphGestureMode) => void;
  snapGuides: boolean;
  setSnapGuides: (enabled: boolean) => void;

  /** Node creation */
  openNodeCreationMenu: (menu: Extract<LaneContextMenu, { kind: "graph" }>) => void;
  viewportCreationAnchor: () => Extract<LaneContextMenu, { kind: "graph" }>;

  /** Timeline layout */
  timelineOrientation: "vertical" | "horizontal";
  changeTimelineOrientation: (orientation: "vertical" | "horizontal") => void;
  timelineDateAxis: "top" | "bottom";
  changeTimelineDateAxis: (axis: "top" | "bottom") => void;
  timelineNodeAxis: "left" | "right";
  changeTimelineNodeAxis: (axis: "left" | "right") => void;
  timelineVNodeAxis: "top" | "bottom";
  changeTimelineVNodeAxis: (axis: "top" | "bottom") => void;
  timelineVDateAxis: "right" | "left";
  changeTimelineVDateAxis: (axis: "right" | "left") => void;
  timelineCellAuto: boolean;
  timelineCellWidth: number;
  timelineCellHeight: number;
  setTimelineCellSize: (dimension: "width" | "height", value: number) => void;
  useAutoTimelineCellSize: () => void;
  collapseEmpty: boolean;
  setCollapse: (collapse: boolean) => void;

  /** Notices */
  graphNotice: { text: string; kind: "ok" | "error" } | null;
  planNotice: { text: string; kind: "ok" | "error" } | null;
}

export function BoardToolbar({
  isGraph,
  collapsed,
  onToggle,
  zoom,
  zoomAt,
  nodeScale,
  setNodeScale,
  cardFontScale,
  setCardFontScale,
  resetCardScale,
  graphToolsOpen,
  setGraphToolsOpen,
  analysisOpen,
  setAnalysisOpen,
  analysis,
  fitGraph,
  snapToGrid,
  setSnapToGrid,
  minimapVisible,
  setMinimapVisible,
  alignAllToGrid,
  canAlignToGrid,
  selectedIDs,
  selectedNodeId,
  nodeTitle,
  openNodeCreationMenu,
  viewportCreationAnchor,
  timelineOrientation,
  changeTimelineOrientation,
  timelineDateAxis,
  changeTimelineDateAxis,
  timelineNodeAxis,
  changeTimelineNodeAxis,
  timelineVNodeAxis,
  changeTimelineVNodeAxis,
  timelineVDateAxis,
  changeTimelineVDateAxis,
  timelineCellAuto,
  timelineCellWidth,
  timelineCellHeight,
  setTimelineCellSize,
  useAutoTimelineCellSize,
  collapseEmpty,
  setCollapse,
  gestureMode,
  setGestureMode,
  snapGuides,
  setSnapGuides,
  graphNotice,
  planNotice,
}: BoardToolbarProps) {
  const { t } = useI18n();
  // Two different questions, so two different answers. The gesture control has
  // to exist wherever a finger can land — an iPad with a keyboard attached
  // reports a fine primary pointer, and hiding the control there would take
  // the connect and marquee drags away from the hand still touching the
  // screen. The hint, by contrast, is one sentence and has to pick a side, so
  // it follows whichever input the device is mainly driven with.
  const touchCapable = useTouchCapable();
  const coarsePointer = useCoarsePointer();
  // Both panes render this toolbar, so the hint needs an id that stays unique.
  const hintId = useId();
  const [hintOpen, setHintOpen] = useState(false);
  const hint = isGraph
    ? coarsePointer
      ? t("board.hint.graphTouch")
      : t("board.hint.graphMouse")
    : t("board.hint.timeline");
  const alerts = analysis.overdue.size + analysis.violations.length;
  // The pane's other skins (war room, boss map), last in the graph's menu.
  const skinItems = useStageSkinItems();

  // The graph's second tier. Ordered by how often each is reached for: the
  // analysis panel (also a button of its own while it has something to say),
  // then fitting, then the drag aids, then the minimap.
  const graphOverflowItems: OverflowMenuItem[] = [
    {
      id: "analysis",
      label: t("board.analysis", { count: String(alerts || "") }).trim(),
      checked: analysisOpen,
      onSelect: () => setAnalysisOpen((open) => !open),
    },
    {
      id: "fit-selected",
      label: t("board.fitSelected"),
      disabled: !selectedIDs.length,
      onSelect: () => fitGraph(true),
    },
    {
      id: "snap-guides",
      label: t("board.alignGuides"),
      title: t("board.alignGuidesTitle"),
      icon: <IconAlignGuides size={13} />,
      checked: snapGuides,
      separatorBefore: true,
      onSelect: () => setSnapGuides(!snapGuides),
    },
    {
      id: "snap-grid",
      label: t("board.snapGrid"),
      title: t("board.snapGridTitle"),
      checked: snapToGrid,
      onSelect: () => setSnapToGrid(!snapToGrid),
    },
    {
      id: "align-all",
      label: t("board.alignAll"),
      title: t("board.alignAllTitle"),
      disabled: !canAlignToGrid,
      onSelect: alignAllToGrid,
    },
    {
      id: "minimap",
      label: t("board.minimap"),
      title: t("board.minimapTitle"),
      checked: minimapVisible,
      separatorBefore: true,
      onSelect: () => setMinimapVisible(!minimapVisible),
    },
    ...skinItems,
  ];

  // The timeline's: which side each axis sits on, and whether empty days fold.
  // Direction stays in the header — it changes the whole board, and it is what
  // the tour points at.
  const timelineOverflowItems: OverflowMenuItem[] = [
    ...(timelineOrientation === "vertical"
      ? [
          {
            id: "vnode-axis",
            label: t("board.nodeRow", {
              position: timelineVNodeAxis === "top" ? t("board.top") : t("board.bottom"),
            }),
            title: t("board.axisToggleNodeRowTitle"),
            onSelect: () =>
              changeTimelineVNodeAxis(timelineVNodeAxis === "top" ? "bottom" : "top"),
          },
          {
            id: "vdate-axis",
            label: t("board.dateColumn", {
              position: timelineVDateAxis === "right" ? t("board.right") : t("board.left"),
            }),
            title: t("board.axisToggleDateColumnTitle"),
            onSelect: () =>
              changeTimelineVDateAxis(timelineVDateAxis === "right" ? "left" : "right"),
          },
        ]
      : [
          {
            id: "date-axis",
            label: t("board.dateRow", {
              position: timelineDateAxis === "top" ? t("board.top") : t("board.bottom"),
            }),
            title: t("board.axisToggleDateRowTitle"),
            onSelect: () =>
              changeTimelineDateAxis(timelineDateAxis === "top" ? "bottom" : "top"),
          },
          {
            id: "node-axis",
            label: t("board.nodeColumn", {
              position: timelineNodeAxis === "left" ? t("board.left") : t("board.right"),
            }),
            title: t("board.axisToggleNodeColumnTitle"),
            onSelect: () =>
              changeTimelineNodeAxis(timelineNodeAxis === "left" ? "right" : "left"),
          },
        ]),
    {
      id: "collapse-empty",
      label: t("board.collapseEmpty"),
      checked: collapseEmpty,
      separatorBefore: true,
      onSelect: () => setCollapse(!collapseEmpty),
    },
  ];

  return (
    <div className="board-title">
      {/* One wrapper around every control in the header. On a desktop it is
          `display: contents`, so the row is laid out exactly as it was before
          this existed; on a phone it becomes a single scrollable strip, which
          is what stops these controls from wrapping the header to a second row
          the board would otherwise lose. The notices stay outside it: they are
          the one thing here that has to be seen without a swipe. */}
      <div className="board-title-controls">
        <button
          className="pane-toggle"
          onClick={onToggle}
          title={collapsed ? t("board.expandPane") : t("board.collapsePane")}
          aria-expanded={!collapsed}
        >
          {collapsed ? "▸" : "▾"}
        </button>
        <span className="board-title-main">
          {isGraph ? t("board.graphTitle") : t("board.timelineTitle")}
        </span>
        {/* A folded pane is one line of header; the chevron alone is easy to
            miss, so it says in words what opening it does. */}
        {collapsed && (
          <button type="button" className="board-show-pane" onClick={onToggle}>
            {isGraph ? t("board.showGraph") : t("board.showTimeline")}
          </button>
        )}
        {!collapsed && (
          <button
            type="button"
            className="board-primary-action"
            onClick={() => openNodeCreationMenu(viewportCreationAnchor())}
            title={t("board.newNodeTitle")}
          >
            <IconPlus size={13} />
            {t("board.newNode")}
          </button>
        )}
        {isGraph && !collapsed && touchCapable && (
          <div className="gesture-mode" role="group" aria-label={t("board.gestureLabel")}>
            {GRAPH_GESTURE_MODES.map(({ mode }) => (
              <button
                key={mode}
                type="button"
                className={gestureMode === mode ? "active" : ""}
                aria-pressed={gestureMode === mode}
                onClick={() => setGestureMode(mode)}
                title={t(`board.gesture.${mode}Title`)}
              >
                {t(`board.gesture.${mode}`)}
              </button>
            ))}
          </div>
        )}
        {isGraph && !collapsed && (
          <>
            <button type="button" className="board-tool-button" onClick={() => fitGraph(false)}>
              {t("board.fitAll")}
            </button>
            <button
              type="button"
              className={`board-tool-button${graphToolsOpen ? " active" : ""}`}
              onClick={() => setGraphToolsOpen((open) => !open)}
              aria-expanded={graphToolsOpen}
            >
              {t("board.filterView")}
            </button>
            {/* Out of the way until it has something to report: an overdue
                node or a broken dependency puts the count back in the header,
                and the menu always has it. */}
            {(alerts > 0 || analysisOpen) && (
              <button
                type="button"
                className={`board-tool-button analysis${analysisOpen ? " active" : ""}${
                  alerts ? " alert" : ""
                }`}
                onClick={() => setAnalysisOpen((open) => !open)}
                aria-expanded={analysisOpen}
              >
                {t("board.analysis", { count: String(alerts || "") })}
              </button>
            )}
          </>
        )}
        {!isGraph && selectedNodeId && (
          <span className="board-title-message selection">
            {t("board.selectedNode", { title: nodeTitle(selectedNodeId) })}
          </span>
        )}
        {/* Groups the timeline-only layout controls so the JSX reads as one
            block. It is `display: contents` at every width — the strip around it
            already does the scrolling these controls need on a phone, and a
            nested scroller would only trap the swipe. */}
        {!isGraph && !collapsed && (
          <div className="timeline-layout-controls">
            <div className="timeline-orientation" role="group" aria-label={t("board.timelineDirection")}>
              <button
                type="button"
                className={timelineOrientation === "vertical" ? "active" : ""}
                aria-pressed={timelineOrientation === "vertical"}
                onClick={() => changeTimelineOrientation("vertical")}
                title={t("board.dateTopBottom")}
              >
                {t("board.vertical")}
              </button>
              <button
                type="button"
                className={timelineOrientation === "horizontal" ? "active" : ""}
                aria-pressed={timelineOrientation === "horizontal"}
                onClick={() => changeTimelineOrientation("horizontal")}
                title={t("board.dateLeftRight")}
              >
                {t("board.horizontal")}
              </button>
            </div>
            {timelineOrientation === "horizontal" && (
              <div className="timeline-width-stepper" role="group" aria-label={t("board.timelineColumnWidth")}>
                <IconResizeHorizontal size={13} />
                <button
                  type="button"
                  aria-label={t("board.shrinkTimelineColumn")}
                  disabled={timelineCellWidth <= TIMELINE_CELL_MIN_W}
                  onClick={() => setTimelineCellSize("width", timelineCellWidth - 16)}
                >
                  −
                </button>
                <output aria-live="polite">{timelineCellWidth}px</output>
                <button
                  type="button"
                  aria-label={t("board.expandTimelineColumn")}
                  disabled={timelineCellWidth >= TIMELINE_CELL_MAX_W}
                  onClick={() => setTimelineCellSize("width", timelineCellWidth + 16)}
                >
                  ＋
                </button>
              </div>
            )}
          </div>
        )}
        {/* Only worth a place while it says something: at 100% the reset has
            nothing to do. */}
        {!collapsed && zoom !== 1 && (
          <button
            type="button"
            className="board-zoom-reset"
            onClick={() => zoomAt(1)}
            title={t("board.resetZoomTitle")}
            aria-label={t("board.currentZoom", { value: String(Math.round(zoom * 100)) })}
          >
            {Math.round(zoom * 100)}%
          </button>
        )}
        {!collapsed && (
          <>
            {/* The gestures used to be spelled out in a sentence across the
                header, all the time. They are one press away instead, at every
                width: a real button rather than a tooltip, because the touch
                wording is the only place these gestures are taught and a
                finger cannot hover to read a title. */}
            <button
              type="button"
              className="board-hint-toggle"
              onClick={() => setHintOpen((open) => !open)}
              aria-expanded={hintOpen}
              aria-controls={hintId}
              aria-label={t("board.hintToggle")}
              title={t("board.hintToggle")}
            >
              ?
            </button>
            <span
              id={hintId}
              className={`board-title-hint${hintOpen ? " open" : ""}`}
              role="note"
            >
              {hint}
            </span>
          </>
        )}
        {isGraph && !collapsed && (
          <OverflowMenu
            className="board-overflow"
            triggerClassName="board-tool-button"
            placement="fixed"
            label={t("board.moreGraphTools")}
            items={graphOverflowItems}
          >
            {/* Two questions the zoom cannot answer, because zoom moves the
                box and the words in it together: how big the cards are next
                to the board, and how big the words are inside them. */}
            <div className="board-overflow-section" role="group" aria-label={t("board.displayScale")}>
              <div className="timeline-size-heading">
                <b>{t("board.displayScale")}</b>
                <button
                  type="button"
                  disabled={nodeScale === 1 && cardFontScale === 1}
                  onClick={resetCardScale}
                >
                  {t("board.reset100")}
                </button>
              </div>
              <label>
                <span>{t("board.nodeScale")}</span>
                <input
                  type="range"
                  min={GRAPH_SCALE_MIN}
                  max={GRAPH_SCALE_MAX}
                  step={GRAPH_SCALE_STEP}
                  value={nodeScale}
                  onChange={(event) => setNodeScale(Number(event.target.value))}
                />
                <output>{Math.round(nodeScale * 100)}%</output>
              </label>
              <label>
                <span>{t("board.textSize")}</span>
                <input
                  type="range"
                  min={GRAPH_SCALE_MIN}
                  max={GRAPH_SCALE_MAX}
                  step={GRAPH_SCALE_STEP}
                  value={cardFontScale}
                  onChange={(event) => setCardFontScale(Number(event.target.value))}
                />
                <output>{Math.round(cardFontScale * 100)}%</output>
              </label>
              <small>{t("board.scaleHint")}</small>
            </div>
          </OverflowMenu>
        )}
        {!isGraph && !collapsed && (
          <OverflowMenu
            className="board-overflow"
            triggerClassName="board-tool-button"
            placement="fixed"
            label={t("board.moreTimelineTools")}
            items={timelineOverflowItems}
          >
            <div className="board-overflow-section" role="group" aria-label={t("board.cellSize")}>
              <div className="timeline-size-heading">
                <b>
                  {t("board.cellSize")} · {timelineCellWidth}×{timelineCellHeight}
                </b>
                <button
                  type="button"
                  className={timelineCellAuto ? "active" : ""}
                  aria-pressed={timelineCellAuto}
                  onClick={useAutoTimelineCellSize}
                >
                  {t("board.autoCellSize")}
                </button>
              </div>
              <label>
                <span>{t("board.width")}</span>
                <input
                  type="range"
                  min={TIMELINE_CELL_MIN_W}
                  max={TIMELINE_CELL_MAX_W}
                  step={8}
                  value={timelineCellWidth}
                  onChange={(event) => setTimelineCellSize("width", Number(event.target.value))}
                />
                <output>{timelineCellWidth}px</output>
              </label>
              <label>
                <span>{t("board.height")}</span>
                <input
                  type="range"
                  min={TIMELINE_CELL_MIN_H}
                  max={TIMELINE_CELL_MAX_H}
                  step={4}
                  value={timelineCellHeight}
                  onChange={(event) => setTimelineCellSize("height", Number(event.target.value))}
                />
                <output>{timelineCellHeight}px</output>
              </label>
              <small>{t("board.cellHint")}</small>
            </div>
          </OverflowMenu>
        )}
      </div>
      {isGraph && graphNotice && (
        <InlineNotice kind={graphNotice.kind === "ok" ? "success" : "error"}>
          {graphNotice.text}
        </InlineNotice>
      )}
      {!isGraph && planNotice && (
        <InlineNotice kind={planNotice.kind === "ok" ? "success" : "error"}>
          {planNotice.text}
        </InlineNotice>
      )}
    </div>
  );
}
