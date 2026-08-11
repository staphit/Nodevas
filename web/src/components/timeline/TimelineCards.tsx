/**
 * One timeline cell's records: the milestones expected on this day and the
 * lifecycle stamps that actually happened on it.
 *
 * Both are draggable to another day, so this stays presentational — the drag
 * itself lives in useMilestoneDrag and arrives here as handlers.
 */

import { reportError } from "../../store";
import { StatusShape, statusTheme } from "../../statusTheme";
import { isBuiltinPlanStatus, planStatusLabel } from "../../plan";
import type {
  BuiltinPlanStatus,
  HistoryEvent,
  PlanMilestone,
  PlanStatusDefinition,
  Status,
  StatusDefinition,
} from "../../types";
import type { Col } from "../canvas/geometry";
import type { ActualDragState, PlanDragState } from "./useMilestoneDrag";

export interface TimelineCardsProps {
  column: Col;
  date: string;
  /** Milestones and stamps already narrowed to this node and this day. */
  plans: PlanMilestone[];
  events: HistoryEvent[];
  planStatusDefinitions: PlanStatusDefinition[];
  customStatuses: StatusDefinition[];
  planDrag: PlanDragState | null;
  actualDrag: ActualDragState | null;
  startPlanDrag: (
    event: React.PointerEvent<HTMLDivElement>,
    nodeId: string,
    plan: PlanMilestone,
  ) => void;
  movePlanDrag: (event: React.PointerEvent<HTMLDivElement>) => void;
  endPlanDrag: (event: React.PointerEvent<HTMLDivElement>) => void;
  startActualDrag: (
    event: React.PointerEvent<HTMLDivElement>,
    historyEvent: HistoryEvent,
  ) => void;
  moveActualDrag: (event: React.PointerEvent<HTMLDivElement>) => void;
  endActualDrag: (event: React.PointerEvent<HTMLDivElement>) => void;
  selectNode: (nodeId: string) => void;
  openTab: (id: string) => Promise<unknown>;
  openPlanMenu: (
    event: React.MouseEvent,
    nodeId: string,
    date: string,
    note?: string,
    time?: string,
  ) => void;
  nodeTitle: (id: string | undefined) => string;
}

export function TimelineCards({
  column,
  date,
  plans,
  events,
  planStatusDefinitions,
  customStatuses,
  planDrag,
  actualDrag,
  startPlanDrag,
  movePlanDrag,
  endPlanDrag,
  startActualDrag,
  moveActualDrag,
  endActualDrag,
  selectNode,
  openTab,
  openPlanMenu,
  nodeTitle,
}: TimelineCardsProps) {
  return (
    <>
      {plans.map((plan) => {
        const builtinStatus = isBuiltinPlanStatus(plan.status);
        const label = planStatusLabel(plan.status, planStatusDefinitions);
        const lifting =
          planDrag?.nodeId === column.node.id &&
          planDrag.status === plan.status &&
          planDrag.fromDate === date;
        return (
          <div
            key={`plan-${plan.status}`}
            className={`plan-card status-${
              builtinStatus ? plan.status : "custom"
            }${lifting ? " lifting" : ""}`}
            style={
              lifting
                ? {
                    transform: `translate3d(${planDrag.dx}px, ${planDrag.dy}px, 0)`,
                  }
                : undefined
            }
            aria-grabbed={lifting}
            role="button"
            tabIndex={0}
            onPointerDown={(event) =>
              startPlanDrag(event, column.node.id, plan)
            }
            onPointerMove={movePlanDrag}
            onPointerUp={endPlanDrag}
            onPointerCancel={endPlanDrag}
            onKeyDown={(event) => {
              if (event.key !== "Enter") return;
              selectNode(column.node.id);
              void openTab(column.node.id).catch(reportError);
            }}
            onContextMenu={(event) =>
              openPlanMenu(
                event,
                column.node.id,
                date,
                plan.note ?? "",
                plan.time ?? "",
              )
            }
            title={`${label} ${plan.date}${
              plan.time ? ` ${plan.time}` : ""
            }（拖曳修改日期；右鍵修改註解與時間）`}
          >
            <span className="snap-card-head">
              <span className="record-kind expected">預期</span>
              {builtinStatus ? (
                <StatusShape status={plan.status as BuiltinPlanStatus} size={11} />
              ) : (
                <span className="custom-plan-shape" aria-hidden>＋</span>
              )}
              <b>{label}</b>
              {plan.time && <span className="plan-time mono">{plan.time}</span>}
            </span>
            <span className="snap-card-title">
              {column.node.title || column.node.id}
            </span>
            <span className="timeline-card-note">{plan.note || "無註解"}</span>
          </div>
        );
      })}
      {events.map((historyEvent, index) => {
        const to = (historyEvent.to ?? "ready") as Status;
        const lifting =
          actualDrag?.eventId === historyEvent.id &&
          actualDrag?.fromDate === date;
        const draggable =
			Boolean(historyEvent.id);
        const liftingStyle =
          lifting && actualDrag
            ? {
                transform: `translate3d(${actualDrag.dx}px, ${actualDrag.dy}px, 0)`,
              }
            : undefined;
        return (
          <div
            key={historyEvent.id ?? index}
            className={`snap-card status-${to}${
              draggable ? " draggable" : ""
            }${lifting ? " lifting" : ""}`}
            style={liftingStyle}
            aria-grabbed={lifting}
            role="button"
            tabIndex={0}
            onPointerDown={(event) => startActualDrag(event, historyEvent)}
            onPointerMove={moveActualDrag}
            onPointerUp={endActualDrag}
            onPointerCancel={endActualDrag}
            onClick={() => {
              if (draggable) return;
              if (historyEvent.node) selectNode(historyEvent.node);
              if (historyEvent.node) {
                void openTab(historyEvent.node).catch(reportError);
              }
            }}
            onKeyDown={(event) => {
              if (event.key !== "Enter" || !historyEvent.node) return;
              selectNode(historyEvent.node);
              void openTab(historyEvent.node).catch(reportError);
            }}
            title={`${new Date(historyEvent.t).toLocaleString()} ${
              historyEvent.from
                ? statusTheme(historyEvent.from, customStatuses).label
                : "(初始)"
            } → ${statusTheme(to, customStatuses).label}${
              historyEvent.note ? `\n備註: ${historyEvent.note}` : ""
            }\n${
              draggable
                ? "實際紀錄（拖曳修改日期；點擊開啟資訊）"
                : "舊版實際紀錄（無事件 ID，無法拖曳）"
            }`}
          >
            <span className="snap-card-head">
              <span className="record-kind actual">實際</span>
              <StatusShape status={to} size={11} definitions={customStatuses} />
              <b style={{ color: statusTheme(to, customStatuses).color }}>
                {statusTheme(to, customStatuses).label}
              </b>
              <span className="snap-time mono">
                {new Date(historyEvent.t).toLocaleTimeString([], {
                  hour: "2-digit",
                  minute: "2-digit",
                })}
              </span>
            </span>
            <span className="snap-card-title">
              {nodeTitle(historyEvent.node)}
            </span>
            <span className="timeline-card-note">
              {historyEvent.note || "無註解"}
            </span>
          </div>
        );
      })}
    </>
  );
}
