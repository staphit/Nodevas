/**
 * Board toolbar [B-06].
 *
 * The controls above a pane: collapse, primary create action, zoom, graph
 * filters and analysis, timeline orientation and cell size, and the pane's
 * notice line. Presentational — every action arrives as a callback.
 */

import { useId, useState } from "react";

import { IconAlignGuides, IconPlus, IconResizeHorizontal } from "../icons";
import type { GraphAnalysis } from "../analysis";
import { InlineNotice } from "./InteractionPrimitives";
import { GRAPH_GESTURE_MODES, type GraphGestureMode } from "./canvas/gestureMode";
import { useCoarsePointer, useTouchCapable } from "./touch";
import { useCanEdit } from "./SignIn";
import type { LaneContextMenu } from "./LaneView";

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
  // Two different questions, so two different answers. The gesture control has
  // to exist wherever a finger can land — an iPad with a keyboard attached
  // reports a fine primary pointer, and hiding the control there would take
  // the connect and marquee drags away from the hand still touching the
  // screen. The hint, by contrast, is one sentence and has to pick a side, so
  // it follows whichever input the device is mainly driven with.
  const touchCapable = useTouchCapable();
  const coarsePointer = useCoarsePointer();
  const canEdit = useCanEdit();
  // Both panes render this toolbar, so the hint needs an id that stays unique.
  const hintId = useId();
  const [hintOpen, setHintOpen] = useState(false);
  const hint = isGraph
    ? coarsePointer
      ? "長按開啟選單 · 雙指縮放 · 切換上方手勢後拖曳即可連線或框選"
      : "節點可自由拖放 · Ctrl/⌘＋拖曳框選（框到線就選線）· 空白處右鍵新增 · Alt＋點線新增轉折點、雙擊或右鍵刪除 · Del 刪除"
    : "拖曳節點標題調整順序 · 實際／預期紀錄可拖曳改日期 · 儲存格右鍵新增規劃";
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
          title={collapsed ? "展開此視窗" : "折疊此視窗"}
          aria-expanded={!collapsed}
        >
          {collapsed ? "▸" : "▾"}
        </button>
        <span className="board-title-main">
          {isGraph ? "關係圖：節點依賴結構" : "時間軸：預期規劃與實際紀錄"}
        </span>
        {!collapsed && canEdit && (
          <button
            type="button"
            className="board-primary-action"
            onClick={() => openNodeCreationMenu(viewportCreationAnchor())}
            title="新增節點（右鍵與命令面板仍可使用）"
          >
            <IconPlus size={13} />
            新增節點
          </button>
        )}
        {isGraph && !collapsed && touchCapable && (
          <div className="gesture-mode" role="group" aria-label="拖曳手勢">
            {GRAPH_GESTURE_MODES.map(({ mode, label, title }) => (
              <button
                key={mode}
                type="button"
                className={gestureMode === mode ? "active" : ""}
                aria-pressed={gestureMode === mode}
                onClick={() => setGestureMode(mode)}
                title={title}
              >
                {label}
              </button>
            ))}
          </div>
        )}
        {!collapsed && (
          <>
            {/* The hint costs a whole row of an already crowded phone header, so
                at that width it hides behind this toggle instead. It is a real
                button rather than a tooltip because the touch wording is the only
                place these gestures are taught, and a finger cannot hover to read
                a title. Above 720px the toggle is hidden and the sentence shows
                as before. */}
            <button
              type="button"
              className="board-hint-toggle"
              onClick={() => setHintOpen((open) => !open)}
              aria-expanded={hintOpen}
              aria-controls={hintId}
              title="操作提示"
            >
              ⓘ
            </button>
            <span id={hintId} className={`board-title-hint${hintOpen ? " open" : ""}`}>
              {hint}
            </span>
          </>
        )}
        {!collapsed && (
          <button
            type="button"
            className="board-zoom-reset"
            onClick={() => zoomAt(1)}
            disabled={zoom === 1}
            title="重設為 100%"
            aria-label={`目前縮放 ${Math.round(zoom * 100)}%，重設為 100%`}
          >
            {Math.round(zoom * 100)}%
          </button>
        )}
        {isGraph && !collapsed && (
          <>
            <button
              type="button"
              className={`board-tool-button${graphToolsOpen ? " active" : ""}`}
              onClick={() => setGraphToolsOpen((open) => !open)}
              aria-expanded={graphToolsOpen}
            >
              篩選與檢視
            </button>
            <button
              type="button"
              className={`board-tool-button analysis${analysisOpen ? " active" : ""}${
                analysis.overdue.size || analysis.violations.length ? " alert" : ""
              }`}
              onClick={() => setAnalysisOpen((open) => !open)}
              aria-expanded={analysisOpen}
            >
              分析 {analysis.overdue.size + analysis.violations.length || ""}
            </button>
            <button
              type="button"
              className={`board-tool-button snap${snapGuides ? " active" : ""}`}
              aria-pressed={snapGuides}
              onClick={() => setSnapGuides(!snapGuides)}
              title="拖曳或縮放時對齊其他節點的邊緣與中心線；按住 Shift 可暫時關閉"
            >
              <IconAlignGuides size={13} />
              對齊輔助
            </button>
            {/* Two questions the zoom cannot answer, because zoom moves the box
                and the words in it together: how big the cards are next to the
                board, and how big the words are inside them. Both sit behind
                one summary so a header that already scrolls on a phone gains
                one control rather than two. */}
            <details className="graph-scale-control">
              <summary title="調整節點與文字的顯示比例，不影響縮放">
                節點 {Math.round(nodeScale * 100)}% · 文字{" "}
                {Math.round(cardFontScale * 100)}%
              </summary>
              <div className="timeline-size-popover">
                <div className="timeline-size-heading">
                  <b>顯示比例</b>
                  <button
                    type="button"
                    disabled={nodeScale === 1 && cardFontScale === 1}
                    onClick={resetCardScale}
                  >
                    重設為 100%
                  </button>
                </div>
                <label>
                  <span>節點比例</span>
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
                  <span>文字大小</span>
                  <input
                    type="range"
                    min={GRAPH_SCALE_MIN}
                    max={GRAPH_SCALE_MAX}
                    step={GRAPH_SCALE_STEP}
                    value={cardFontScale}
                    onChange={(event) =>
                      setCardFontScale(Number(event.target.value))
                    }
                  />
                  <output>{Math.round(cardFontScale * 100)}%</output>
                </label>
                <small>節點連同間距一起放大，文字維持原本的級距比例。</small>
              </div>
            </details>
            <button type="button" className="board-tool-button" onClick={() => fitGraph(false)}>
              全部置中
            </button>
            <button
              type="button"
              className="board-tool-button"
              disabled={!selectedIDs.length}
              onClick={() => fitGraph(true)}
            >
              選取置中
            </button>
            <button
              type="button"
              className={`board-tool-button${snapToGrid ? " active" : ""}`}
              aria-pressed={snapToGrid}
              onClick={() => setSnapToGrid(!snapToGrid)}
              title="拖曳節點時對齊格線。按住 Alt 可暫時不貼齊"
            >
              貼齊格線
            </button>
            <button
              type="button"
              className="board-tool-button"
              disabled={!canAlignToGrid}
              onClick={alignAllToGrid}
              title="把盤面上每個節點移到最近的格子。貼齊格線只管之後的拖曳，這個管現在"
            >
              全部對齊
            </button>
            <button
              type="button"
              className={`board-tool-button${minimapVisible ? " active" : ""}`}
              aria-pressed={minimapVisible}
              onClick={() => setMinimapVisible(!minimapVisible)}
              title="右下角的全圖縮圖，點縮圖可移動視角。手機預設隱藏"
            >
              縮圖
            </button>
          </>
        )}
        {!isGraph && selectedNodeId && (
          <span className="board-title-message selection">
            對應節點：{nodeTitle(selectedNodeId)}
          </span>
        )}
        {/* Groups the timeline-only layout controls so the JSX reads as one
            block. It is `display: contents` at every width — the strip around it
            already does the scrolling that these six controls need on a phone,
            and a nested scroller would only trap the swipe. */}
        {!isGraph && !collapsed && (
        <div className="timeline-layout-controls">
          <div className="timeline-orientation" role="group" aria-label="時間軸方向">
            <button
              type="button"
              className={timelineOrientation === "vertical" ? "active" : ""}
              aria-pressed={timelineOrientation === "vertical"}
              onClick={() => changeTimelineOrientation("vertical")}
              title="日期由上往下"
            >
              直式
            </button>
            <button
              type="button"
              className={timelineOrientation === "horizontal" ? "active" : ""}
              aria-pressed={timelineOrientation === "horizontal"}
              onClick={() => changeTimelineOrientation("horizontal")}
              title="日期由左往右"
            >
              橫式
            </button>
          </div>
          <details className="timeline-size-control">
            <summary title="調整時間軸格子的寬度與高度">
              <IconResizeHorizontal size={13} />
              格子 {timelineCellWidth}×{timelineCellHeight}
              {timelineCellAuto ? " · 自動" : ""}
            </summary>
            <div className="timeline-size-popover">
              <div className="timeline-size-heading">
                <b>格子大小</b>
                <button
                  type="button"
                  className={timelineCellAuto ? "active" : ""}
                  onClick={useAutoTimelineCellSize}
                >
                  依內容自動
                </button>
              </div>
              <label>
                <span>寬度</span>
                <input
                  type="range"
                  min={TIMELINE_CELL_MIN_W}
                  max={TIMELINE_CELL_MAX_W}
                  step={8}
                  value={timelineCellWidth}
                  onChange={(event) =>
                    setTimelineCellSize("width", Number(event.target.value))
                  }
                />
                <output>{timelineCellWidth}px</output>
              </label>
              <label>
                <span>高度</span>
                <input
                  type="range"
                  min={TIMELINE_CELL_MIN_H}
                  max={TIMELINE_CELL_MAX_H}
                  step={4}
                  value={timelineCellHeight}
                  onChange={(event) =>
                    setTimelineCellSize("height", Number(event.target.value))
                  }
                />
                <output>{timelineCellHeight}px</output>
              </label>
              <small>拖曳滑桿會切換為手動，設定會保留在這台裝置。</small>
            </div>
          </details>
          {timelineOrientation === "horizontal" && (
          <div className="timeline-width-stepper" role="group" aria-label="時間軸欄寬">
            <IconResizeHorizontal size={13} />
            <button
              type="button"
              aria-label="縮小時間軸欄寬"
              disabled={timelineCellWidth <= TIMELINE_CELL_MIN_W}
              onClick={() => setTimelineCellSize("width", timelineCellWidth - 16)}
            >
              −
            </button>
            <output aria-live="polite">{timelineCellWidth}px</output>
            <button
              type="button"
              aria-label="放大時間軸欄寬"
              disabled={timelineCellWidth >= TIMELINE_CELL_MAX_W}
              onClick={() => setTimelineCellSize("width", timelineCellWidth + 16)}
            >
              ＋
            </button>
          </div>
          )}
          {timelineOrientation === "vertical" && (
          <div className="timeline-orientation" role="group" aria-label="座標軸位置">
            <button
              type="button"
              onClick={() =>
                changeTimelineVNodeAxis(timelineVNodeAxis === "top" ? "bottom" : "top")
              }
              title="切換節點列顯示在上方或下方"
            >
              節點列：{timelineVNodeAxis === "top" ? "上" : "下"}
            </button>
            <button
              type="button"
              onClick={() =>
                changeTimelineVDateAxis(timelineVDateAxis === "right" ? "left" : "right")
              }
              title="切換日期欄顯示在右側或左側"
            >
              日期欄：{timelineVDateAxis === "right" ? "右" : "左"}
            </button>
          </div>
          )}
          {timelineOrientation === "horizontal" && (
          <div className="timeline-orientation" role="group" aria-label="座標軸位置">
            <button
              type="button"
              onClick={() =>
                changeTimelineDateAxis(timelineDateAxis === "top" ? "bottom" : "top")
              }
              title="切換日期列顯示在上方或下方"
            >
              日期列：{timelineDateAxis === "top" ? "上" : "下"}
            </button>
            <button
              type="button"
              onClick={() =>
                changeTimelineNodeAxis(timelineNodeAxis === "left" ? "right" : "left")
              }
              title="切換節點欄顯示在左側或右側"
            >
              節點欄：{timelineNodeAxis === "left" ? "左" : "右"}
            </button>
          </div>
          )}
          <label className="board-title-opt">
            <input
              type="checkbox"
              checked={collapseEmpty}
              onChange={(e) => setCollapse(e.target.checked)}
            />
            折疊空白日期
          </label>
      </div>
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
