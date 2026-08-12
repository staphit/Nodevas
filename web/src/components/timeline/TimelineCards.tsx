/**
 * One timeline cell's records: the milestones expected on this day and the
 * lifecycle stamps that actually happened on it.
 *
 * Both are draggable to another day, so this stays presentational — the drag
 * itself lives in useMilestoneDrag and arrives here as handlers.
 */

import { reportError } from "../../store";
import { StatusShape, statusTheme } from "../../statusTheme";
import {
  formatLocalizedDateTime,
  formatLocalizedTime,
  localizedPlanStatusLabel,
  localizedStatusLabel,
  useI18n,
} from "../../i18n";
import { isBuiltinPlanStatus } from "../../plan";
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
  const { t, language } = useI18n();
  return (
    <>
      {plans.map((plan) => {
        const builtinStatus = isBuiltinPlanStatus(plan.status);
        const label = localizedPlanStatusLabel(plan.status, planStatusDefinitions, language);
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
            title={t("timeline.card.planTitle", {
              label,
              date: plan.date,
              time: plan.time ? ` ${plan.time}` : "",
            })}
          >
            <span className="snap-card-head">
              <span className="record-kind expected">{t("timeline.card.expected")}</span>
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
            <span className="timeline-card-note">{plan.note || t("timeline.card.noNote")}</span>
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
            title={`${formatLocalizedDateTime(historyEvent.t, language)} ${
              historyEvent.from
                ? localizedStatusLabel(historyEvent.from, customStatuses)
                : t("timeline.card.initial")
            } → ${localizedStatusLabel(to, customStatuses)}${
              historyEvent.note
                ? `\n${t("timeline.card.note", { note: historyEvent.note })}`
                : ""
            }\n${draggable ? t("timeline.card.actualTitle") : t("timeline.card.legacyTitle")}`}
          >
            <span className="snap-card-head">
              <span className="record-kind actual">{t("timeline.card.actual")}</span>
              <StatusShape status={to} size={11} definitions={customStatuses} />
              <b style={{ color: statusTheme(to, customStatuses).color }}>
                {localizedStatusLabel(to, customStatuses)}
              </b>
              <span className="snap-time mono">
                {formatLocalizedTime(historyEvent.t, language)}
              </span>
            </span>
            <span className="snap-card-title">
              {nodeTitle(historyEvent.node)}
            </span>
            <span className="timeline-card-note">
              {historyEvent.note || t("timeline.card.noNote")}
            </span>
          </div>
        );
      })}
    </>
  );
}
