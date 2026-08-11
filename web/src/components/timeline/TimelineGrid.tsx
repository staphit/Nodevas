/**
 * Timeline grid [B-06].
 *
 * The day grid in both orientations: vertical (days down the page) and
 * horizontal (days across it). Rendering only — every drag, menu and card
 * arrives as a callback, so scheduling rules stay in the pane and the commands.
 */

import type { MouseEvent, PointerEvent, ReactNode } from "react";
import { StatusShape } from "../../statusTheme";
import type { GraphNode, Status, StatusDefinition } from "../../types";
import { ResizeHandle } from "../InteractionPrimitives";
import type { Col } from "../canvas/geometry";

export type TimelineRowItem =
  | { kind: "day"; date: Date; dayIndex: number }
  | { kind: "gap"; count: number };

export interface TimelineOrderDrag {
  pointerId: number;
  id: string;
  fromIndex: number;
  targetIndex: number;
  originX: number;
  originY: number;
  dx: number;
  dy: number;
  moved: boolean;
}

export interface TimelineColumn {
  node: GraphNode;
  index: number;
  row: number;
  width: number;
  height: number;
}

export interface TimelineGridProps {
  timelineOrientation: "vertical" | "horizontal";
  timelineDateAxis: "top" | "bottom";
  timelineNodeAxis: "left" | "right";
  timelineCellWidth: number;
  timelineCellHeight: number;
  setTimelineCellSize: (dimension: "width" | "height", value: number) => void;
  useAutoTimelineCellSize: () => void;
  setTimelineWidthResizeEdge: (edge: "left" | "right" | null) => void;
  collapseEmpty: boolean;
  setCollapse: (collapse: boolean) => void;
  zoom: number;
  boardW: number;
  horizontalTimelineW: number;
  horizontalTimelineH: number;
  horizontalNowX: number | null;
  nowOffset: number;

  timelineCols: TimelineColumn[];
  rowItems: TimelineRowItem[];
  statuses: Record<string, Status>;
  customStatuses: StatusDefinition[];
  userNames: Map<string, string>;
  activeTab: string | null;
  todayKey: string;
  todayStart: Date;
  dayKey: (date: Date) => string;

  renderTimelineCards: (column: Col, date: string) => ReactNode;
  openPlanMenu: (
    event: MouseEvent,
    nodeId: string,
    date: string,
    note?: string,
    time?: string,
  ) => void;
  openTab: (id: string) => Promise<void>;
  selectNode: (id: string) => void;
  timelineOrderDrag: TimelineOrderDrag | null;
  startTimelineOrderDrag: (event: PointerEvent<HTMLButtonElement>, id: string) => void;
  moveTimelineOrderDrag: (event: PointerEvent<HTMLButtonElement>) => void;
  finishTimelineOrderDrag: (
    event: PointerEvent<HTMLButtonElement>,
    commit: boolean,
  ) => void;
  planDrag: { nodeId: string; targetDate: string; moved: boolean } | null;
  selectedNodeId?: string | null;
  now: Date;
  timelineOrderDragRef: { current: TimelineOrderDrag | null };
  updateTimelineOrderDrag: (next: TimelineOrderDrag | null) => void;
  actualDrag: { nodeId: string; targetDate: string; moved: boolean } | null;
}

const RULER_W = 56;
const HORIZONTAL_HEADER_H = 48;
const HORIZONTAL_GAP_W = 56;
const TIMELINE_CELL_MIN_W = 160;
const TIMELINE_CELL_MAX_W = 360;

export function TimelineGrid({
  timelineOrientation,
  timelineDateAxis,
  timelineNodeAxis,
  timelineCellWidth,
  timelineCellHeight,
  setTimelineCellSize,
  useAutoTimelineCellSize,
  setTimelineWidthResizeEdge,
  collapseEmpty,
  setCollapse,
  zoom,
  boardW,
  horizontalTimelineW,
  horizontalTimelineH,
  horizontalNowX,
  nowOffset,
  timelineCols,
  rowItems,
  statuses,
  customStatuses,
  userNames,
  activeTab,
  todayKey,
  todayStart,
  dayKey,
  renderTimelineCards,
  openPlanMenu,
  openTab,
  selectNode,
  timelineOrderDrag,
  startTimelineOrderDrag,
  moveTimelineOrderDrag,
  finishTimelineOrderDrag,
  planDrag,
  actualDrag,
  selectedNodeId,
  now,
  timelineOrderDragRef,
  updateTimelineOrderDrag,
}: TimelineGridProps) {
  return (
    <>
        {timelineOrientation === "vertical" && (
          <>
            <div className="lane-head-row" style={{ width: boardW }}>
              {timelineCols.map((c, index) => {
                const st: Status = statuses[c.node.id] ?? "ready";
                const assigned =
                  (c.node.assignee && userNames.get(c.node.assignee)) || "尚未指派";
                return (
                  <button
                    key={c.node.id}
                    className={`lane-head-cell timeline-order-handle${
                      activeTab === c.node.id ? " active" : ""
                    }${selectedNodeId === c.node.id ? " node-selected" : ""}${
                      timelineOrderDrag?.id === c.node.id ? " order-dragging" : ""
                    }${
                      timelineOrderDrag?.moved &&
                      timelineOrderDrag.targetIndex === index &&
                      timelineOrderDrag.id !== c.node.id
                        ? " order-drop-target"
                        : ""
                    }`}
                    style={{
                      width: timelineCellWidth,
                      transform:
                        timelineOrderDrag?.id === c.node.id &&
                        timelineOrderDrag.moved
                          ? `translateX(${timelineOrderDrag.dx}px)`
                          : undefined,
                    }}
                    onPointerDown={(event) =>
                      startTimelineOrderDrag(event, c.node.id)
                    }
                    onPointerMove={moveTimelineOrderDrag}
                    onPointerUp={(event) =>
                      finishTimelineOrderDrag(event, true)
                    }
                    onPointerCancel={(event) =>
                      finishTimelineOrderDrag(event, false)
                    }
                    onLostPointerCapture={() => {
                      if (timelineOrderDragRef.current?.id === c.node.id) {
                        updateTimelineOrderDrag(null);
                      }
                    }}
                    onClick={(event) => {
                      if (event.detail !== 0) return;
                      selectNode(c.node.id);
                      void openTab(c.node.id).catch(reportError);
                    }}
                    title={`${c.node.title || c.node.id} · ${assigned}（拖曳調整時間軸順序）`}
                    aria-selected={selectedNodeId === c.node.id}
                  >
                    <StatusShape status={st} definitions={customStatuses} />
                    <span className="lane-head-copy">
                      <span className="lane-head-title">{c.node.title || c.node.id}</span>
                        <span className="lane-head-assignee">{assigned}</span>
                      </span>
                      {selectedNodeId === c.node.id && (
                        <span className="lane-selected-tag">選取</span>
                      )}
                  </button>
                );
              })}
              <div className="lane-head-cell ruler" style={{ width: RULER_W }} />
            </div>
            <div className="board-rows">
              {rowItems.map((item, idx) => {
                if (item.kind === "gap") {
                  return (
                    <div
                      key={`gap-${idx}`}
                      className="board-gap"
                      role="button"
                      title="點擊展開這些日期"
                      onClick={() => setCollapse(false)}
                    >
                      <div className="board-gap-label">{item.count} 天無紀錄，點擊展開</div>
                      <div className="board-date-r mono" style={{ width: RULER_W }}>
                        ⋯
                      </div>
                    </div>
                  );
                }
                const d = item.date;
                const key = dayKey(d);
                const isToday = key === todayKey;
                const isFuture = d.getTime() > todayStart.getTime();
                return (
                  <div
                    key={d.getTime()}
                    data-date={key}
                    className={`board-row${isToday ? " board-today" : ""}${isFuture ? " board-future" : ""}`}
                  >
                    {timelineCols.map((c) => {
                      return (
                        <div
                          key={c.node.id}
                          className={`board-cell${activeTab === c.node.id ? " cell-active" : ""}${
                            selectedNodeId === c.node.id ? " node-selected" : ""
                          }${
                            planDrag?.moved &&
                            planDrag.nodeId === c.node.id &&
                            planDrag.targetDate === key
                              ? " plan-drop-target"
                              : ""
                          }${
                            actualDrag?.moved &&
                            actualDrag.nodeId === c.node.id &&
                            actualDrag.targetDate === key
                              ? " actual-drop-target"
                              : ""
                          }`}
                          style={{
                            width: timelineCellWidth,
                            height: timelineCellHeight,
                          }}
                          onContextMenu={(event) => openPlanMenu(event, c.node.id, key)}
                        >
                          {renderTimelineCards(c, key)}
                        </div>
                      );
                    })}
                    {isToday && (
                      <div
                        className="now-line"
                        style={{ top: nowOffset }}
                        aria-label={`目前時間 ${now.toLocaleTimeString([], {
                          hour: "2-digit",
                          minute: "2-digit",
                        })}`}
                      />
                    )}
                    <div
                      className={`board-date-r mono${isToday ? " today" : ""}${isFuture ? " future" : ""}`}
                      style={{ width: RULER_W }}
                    >
                      {d.getMonth() + 1}/{d.getDate()}
                      {isToday && (
                        <span className="now-time">
                          {now.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })}
                        </span>
                      )}
                    </div>
                  </div>
                );
              })}
              <div className="board-fill" />
            </div>
          </>
        )}

        {timelineOrientation === "horizontal" && (
          <div
            className={`horizontal-timeline${
              timelineDateAxis === "bottom" ? " date-axis-bottom" : ""
            }${timelineNodeAxis === "right" ? " node-axis-right" : ""}`}
            style={{
              width: horizontalTimelineW,
              minHeight: horizontalTimelineH,
              ["--timeline-resize-span" as string]: `${horizontalTimelineH}px`,
            }}
          >
            <div
              className="horizontal-date-head"
              style={{ height: HORIZONTAL_HEADER_H }}
            >
              <div
                className="horizontal-corner"
                style={{
                  width: timelineCellWidth,
                  height: HORIZONTAL_HEADER_H,
                }}
              >
                節點＼日期
              </div>
              {rowItems.map((item, index) => {
                if (item.kind === "gap") {
                  return (
                    <button
                      type="button"
                      className="horizontal-gap-head"
                      key={`head-gap-${index}`}
                      style={{ width: HORIZONTAL_GAP_W }}
                      onClick={() => setCollapse(false)}
                      title={`${item.count} 天無紀錄；點擊展開`}
                    >
                      ⋯
                    </button>
                  );
                }
                const key = dayKey(item.date);
                const isToday = key === todayKey;
                const isFuture = item.date.getTime() > todayStart.getTime();
                const previousItem = rowItems[index - 1];
                const nextItem = rowItems[index + 1];
                const leftResizable =
                  index === 0 ||
                  previousItem?.kind === "gap";
                const rightResizable =
                  !collapseEmpty ||
                  index === rowItems.length - 1 ||
                  nextItem?.kind === "gap";
                return (
                  <div
                    className={`horizontal-date-label mono${
                      isToday ? " today" : ""
                    }${isFuture ? " future" : ""}`}
                    key={`head-${key}`}
                    style={{ width: timelineCellWidth }}
                  >
                    <b>
                      {item.date.getMonth() + 1}/{item.date.getDate()}
                    </b>
                    <span>
                      {item.date.toLocaleDateString([], { weekday: "short" })}
                    </span>
                    {isToday && (
                      <small>
                        {now.toLocaleTimeString([], {
                          hour: "2-digit",
                          minute: "2-digit",
                        })}
                      </small>
                    )}
                    {leftResizable && (
                      <ResizeHandle
                        className="timeline-column-resize left"
                        orientation="vertical"
                        value={timelineCellWidth}
                        min={TIMELINE_CELL_MIN_W}
                        max={TIMELINE_CELL_MAX_W}
                        direction={-1}
                        scale={zoom}
                        label="從左側調整全部日期格寬度"
                        title="拖曳調整全部日期格寬度；方向鍵微調；雙擊恢復自動"
                        valueText={(value) => `${Math.round(value)} 像素，套用至全部日期格`}
                        onChange={(value) => setTimelineCellSize("width", value)}
                        onReset={useAutoTimelineCellSize}
                        onResizeStateChange={(resizing) =>
                          setTimelineWidthResizeEdge(resizing ? "left" : null)
                        }
                      />
                    )}
                    {rightResizable && (
                      <ResizeHandle
                        className="timeline-column-resize right"
                        orientation="vertical"
                        value={timelineCellWidth}
                        min={TIMELINE_CELL_MIN_W}
                        max={TIMELINE_CELL_MAX_W}
                        scale={zoom}
                        label="從右側調整全部日期格寬度"
                        title="拖曳調整全部日期格寬度；方向鍵微調；雙擊恢復自動"
                        valueText={(value) => `${Math.round(value)} 像素，套用至全部日期格`}
                        onChange={(value) => setTimelineCellSize("width", value)}
                        onReset={useAutoTimelineCellSize}
                        onResizeStateChange={(resizing) =>
                          setTimelineWidthResizeEdge(resizing ? "right" : null)
                        }
                      />
                    )}
                  </div>
                );
              })}
            </div>

            <div className="horizontal-node-rows">
              {timelineCols.map((column, columnIndex) => {
                const status: Status = statuses[column.node.id] ?? "ready";
                const assigned =
                  (column.node.assignee &&
                    userNames.get(column.node.assignee)) ||
                  "尚未指派";
                return (
                  <div
                    className={`horizontal-node-row${
                      timelineOrderDrag?.id === column.node.id
                        ? " order-dragging"
                        : ""
                    }${
                      timelineOrderDrag?.moved &&
                      timelineOrderDrag.targetIndex === columnIndex &&
                      timelineOrderDrag.id !== column.node.id
                        ? " order-drop-target"
                        : ""
                    }`}
                    key={column.node.id}
                    style={{
                      height: timelineCellHeight,
                      transform:
                        timelineOrderDrag?.id === column.node.id &&
                        timelineOrderDrag.moved
                          ? `translateY(${timelineOrderDrag.dy}px)`
                          : undefined,
                    }}
                  >
                    <button
                      type="button"
                      className={`horizontal-node-head${
                        activeTab === column.node.id ? " active" : ""
                      }${
                        selectedNodeId === column.node.id
                          ? " node-selected"
                          : ""
                      }`}
                      style={{
                        width: timelineCellWidth,
                        height: timelineCellHeight,
                      }}
                      onPointerDown={(event) =>
                        startTimelineOrderDrag(event, column.node.id)
                      }
                      onPointerMove={moveTimelineOrderDrag}
                      onPointerUp={(event) =>
                        finishTimelineOrderDrag(event, true)
                      }
                      onPointerCancel={(event) =>
                        finishTimelineOrderDrag(event, false)
                      }
                      onLostPointerCapture={() => {
                        if (
                          timelineOrderDragRef.current?.id === column.node.id
                        ) {
                          updateTimelineOrderDrag(null);
                        }
                      }}
                      onClick={(event) => {
                        if (event.detail !== 0) return;
                        selectNode(column.node.id);
                        void openTab(column.node.id).catch(reportError);
                      }}
                      title={`${column.node.title || column.node.id} · ${assigned}（拖曳調整時間軸順序）`}
                      aria-selected={selectedNodeId === column.node.id}
                    >
                      <StatusShape
                        status={status}
                        definitions={customStatuses}
                      />
                      <span className="lane-head-copy">
                        <span className="lane-head-title">
                          {column.node.title || column.node.id}
                        </span>
                        <span className="lane-head-assignee">{assigned}</span>
                      </span>
                      {selectedNodeId === column.node.id && (
                        <span className="lane-selected-tag">選取</span>
                      )}
                    </button>

                    {rowItems.map((item, index) => {
                      if (item.kind === "gap") {
                        return (
                          <button
                            type="button"
                            className="horizontal-gap-cell"
                            key={`${column.node.id}-gap-${index}`}
                            style={{
                              width: HORIZONTAL_GAP_W,
                              height: timelineCellHeight,
                            }}
                            onClick={() => setCollapse(false)}
                            title={`${item.count} 天無紀錄；點擊展開`}
                          >
                            ⋯
                          </button>
                        );
                      }
                      const key = dayKey(item.date);
                      const isToday = key === todayKey;
                      const isFuture =
                        item.date.getTime() > todayStart.getTime();
                      return (
                        <div
                          key={`${column.node.id}-${key}`}
                          data-date={key}
                          className={`board-cell horizontal-date-cell${
                            isToday ? " board-today" : ""
                          }${isFuture ? " board-future" : ""}${
                            activeTab === column.node.id ? " cell-active" : ""
                          }${
                            selectedNodeId === column.node.id
                              ? " node-selected"
                              : ""
                          }${
                            planDrag?.moved &&
                            planDrag.nodeId === column.node.id &&
                            planDrag.targetDate === key
                              ? " plan-drop-target"
                              : ""
                          }${
                            actualDrag?.moved &&
                            actualDrag.nodeId === column.node.id &&
                            actualDrag.targetDate === key
                              ? " actual-drop-target"
                              : ""
                          }`}
                          style={{
                            width: timelineCellWidth,
                            height: timelineCellHeight,
                          }}
                          onContextMenu={(event) =>
                            openPlanMenu(event, column.node.id, key)
                          }
                        >
                          {renderTimelineCards(column, key)}
                        </div>
                      );
                    })}
                  </div>
                );
              })}
            </div>

            {horizontalNowX !== null && (
              <div
                className="horizontal-now-line"
                style={{
                  left:
                    horizontalNowX -
                    (timelineNodeAxis === "right" ? timelineCellWidth : 0),
                  top: timelineDateAxis === "bottom" ? 0 : HORIZONTAL_HEADER_H,
                  height:
                    Math.max(timelineCols.length, 1) *
                    timelineCellHeight,
                }}
                aria-label={`目前時間 ${now.toLocaleTimeString([], {
                  hour: "2-digit",
                  minute: "2-digit",
                })}`}
              />
            )}
          </div>
        )}

        {/* ---------- graph: cards + logic-gate wiring on a plain grid ---------- */}
    </>
  );
}
