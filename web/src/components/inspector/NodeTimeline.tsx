/**
 * Per-node schedule panel [B-06].
 *
 * Expected milestones (graph.yaml) on top, actual journal entries below, and an
 * assessment line that compares the two — the one place where plan and record
 * are deliberately shown together.
 */

import { BUILTIN_PLAN_STATUSES } from "../../plan";
import { operationScope, reportError, useApp, useOperation } from "../../store";
import {
  formatLocalizedDateTime,
  localizedPlanStatusLabel,
  localizedStatusLabel,
  useI18n,
} from "../../i18n";
import type { PlanStatus, Status } from "../../types";
import { OperationStatus } from "../InteractionPrimitives";

function localDateKey(value: string | Date): string {
  const date = value instanceof Date ? value : new Date(value);
  if (Number.isNaN(date.getTime())) return "";
  return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, "0")}-${String(
    date.getDate(),
  ).padStart(2, "0")}`;
}

function calendarDayDifference(left: string, right: string): number {
  const parse = (value: string) => {
    const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(value);
    return match
      ? Date.UTC(Number(match[1]), Number(match[2]) - 1, Number(match[3]))
      : Number.NaN;
  };
  return Math.round((parse(left) - parse(right)) / 86_400_000);
}

export function NodeTimeline({ id }: { id: string }) {
  const { t, language } = useI18n();
  const graph = useApp((state) => state.graph);
  const runState = useApp((state) => state.runState);
  const upsertPlanMilestone = useApp((state) => state.upsertPlanMilestone);
  const removePlanMilestone = useApp((state) => state.removePlanMilestone);
  const planOperation = useOperation(operationScope.plan(id));
  const plans = graph?.ui?.plans?.[id] ?? [];
  const planStatusDefinitions = graph?.ui?.planStatuses ?? [];
  const customStatuses = graph?.ui?.customStatuses ?? [];

  // Expected milestones go through the plan commands: clearing the date removes
  // the milestone, and the note survives a reschedule [A-04].
  const commitPlan = (status: PlanStatus, patch: { date?: string; time?: string }) => {
    const existing = plans.find((plan) => plan.status === status);
    const date = patch.date ?? existing?.date ?? "";
    const time = patch.time ?? existing?.time;
    const run = date
      ? upsertPlanMilestone(id, { type: status, date, time, note: existing?.note })
      : removePlanMilestone(id, status);
    void run.then((result) => {
      if (!result.ok) reportError(new Error(result.message));
    });
  };
  const commitPlanDate = (status: PlanStatus, date: string) =>
    commitPlan(status, { date });
  const history = runState?.history ?? [];
  const statusEvents = history.filter(
    (event) => event.node === id && event.event === "status",
  );
  const statusByID = new Map(
    statusEvents
      .filter((event) => event.id)
      .map((event) => [event.id!, event]),
  );
  const auditEvents = history
    .map((event, historyIndex) => ({ event, historyIndex }))
    .filter(({ event }) => event.node === id && (event.event === "status" || event.event === "move"));

  const assessment = (status: "started" | "done") => {
    const plan = plans.find((item) => item.status === status);
    const actual = statusEvents.find((event) => event.to === status);
    const action = status === "started" ? t("timeline.startAction") : t("timeline.doneAction");
    if (!plan) {
      return {
        kind: "neutral",
        result: t("timeline.notPlanned", { action }),
        detail: actual
          ? t("timeline.actualDate", { date: localDateKey(actual.t) })
          : t("timeline.noActual"),
      };
    }
    if (actual) {
      const actualDate = localDateKey(actual.t);
      const difference = calendarDayDifference(actualDate, plan.date);
      return {
        kind: difference <= 0 ? "on-time" : "late",
        result:
          difference === 0
            ? t("timeline.onTime", { action })
            : difference < 0
              ? t("timeline.early", { days: String(Math.abs(difference)), action })
              : t("timeline.late", { days: String(difference), action }),
        detail: t("timeline.plannedActual", { planned: plan.date, actual: actualDate }),
      };
    }
    const today = localDateKey(new Date());
    const difference = calendarDayDifference(today, plan.date);
    return {
      kind: difference > 0 ? "late" : "pending",
      result:
        difference > 0
          ? t("timeline.overdue", { days: String(difference), action })
          : difference === 0
            ? t("timeline.today", { action })
            : t("timeline.daysUntil", { days: String(Math.abs(difference)), action }),
      detail: t("timeline.plannedNoActual", { date: plan.date }),
    };
  };

  const startAssessment = assessment("started");
  const doneAssessment = assessment("done");

  return (
    <div className="node-timeline" role="tabpanel" aria-label={t("timeline.aria")}>
      <div className="node-timeline-assessments">
        {[
          [t("timeline.assessment.start"), startAssessment],
          [t("timeline.assessment.done"), doneAssessment],
        ].map(([label, item]) => {
          const result = item as ReturnType<typeof assessment>;
          return (
            <div className={`timeline-assessment ${result.kind}`} key={label as string}>
              <span>{label as string}</span>
              <b>{result.result}</b>
              <small>{result.detail}</small>
            </div>
          );
        })}
      </div>

      <section className="node-timeline-plans">
        <div className="section-head">
          <h3>{t("timeline.plansTitle")}</h3>
          <span className="section-hint">{t("timeline.plansHint")}</span>
          <OperationStatus
            status={planOperation.status}
            message={
              planOperation.status === "error" || planOperation.status === "conflict"
                ? planOperation.message
                : undefined
            }
          />
        </div>
        <div className="timeline-plan-editor">
          {[
            ...BUILTIN_PLAN_STATUSES.map(({ status }) => ({
              status,
              label: localizedPlanStatusLabel(status, planStatusDefinitions, language),
            })),
            ...planStatusDefinitions.map((definition) => ({
              status: definition.id as PlanStatus,
              label: definition.label,
            })),
          ].map(({ status, label }) => {
            const plan = plans.find((item) => item.status === status);
            return (
              <label className="timeline-plan-edit-row with-time" key={status}>
                <span>{label}</span>
                <input
                  type="date"
                  value={plan?.date ?? ""}
                  onChange={(event) => commitPlanDate(status, event.target.value)}
                />
                <input
                  type="time"
                  value={plan?.time ?? ""}
                  disabled={!plan?.date}
                  title={t("timeline.timeTitle")}
                  onChange={(event) => commitPlan(status, { time: event.target.value })}
                />
                {plan?.note && <small>「{plan.note}」</small>}
              </label>
            );
          })}
        </div>
        {plans
          .filter(
            (plan) =>
              !BUILTIN_PLAN_STATUSES.some((item) => item.status === plan.status) &&
              !planStatusDefinitions.some((definition) => definition.id === plan.status),
          )
          .map((plan) => (
            <div className="timeline-plan-edit-row in-progress" key={plan.status}>
              <span>{localizedPlanStatusLabel(plan.status, planStatusDefinitions, language)}</span>
              <time>{plan.date}</time>
              <button type="button" onClick={() => commitPlanDate(plan.status, "")}>
                {t("timeline.remove")}
              </button>
            </div>
          ))}
      </section>

      <section className="node-timeline-audit">
        <h3>{t("timeline.auditTitle")}</h3>
        {auditEvents.length === 0 ? (
          <div className="node-timeline-empty">{t("timeline.noAudit")}</div>
        ) : (
          <ol>
            {auditEvents.map(({ event, historyIndex }, sequence) => {
              const referenced = event.ref ? statusByID.get(event.ref) : undefined;
              const status = (event.event === "move"
                ? referenced?.to
                : event.to) as Status | undefined;
              const statusLabel = status
                ? localizedStatusLabel(status, customStatuses)
                : t("timeline.event");
              const fromLabel = event.from
                ? localizedStatusLabel(event.from, customStatuses)
                : t("timeline.notStarted");
              return (
                <li key={`${event.id || event.ref || historyIndex}-${sequence}`}>
                  <span className={`timeline-audit-kind ${event.event}`}>
                    {event.event === "move" ? t("timeline.adjustment") : t("timeline.actual")}
                  </span>
                  <div>
                    <b>
                      {event.event === "move"
                        ? t("timeline.adjustDate", { status: statusLabel })
                        : `${fromLabel} → ${
                            status ? statusLabel : event.to
                          }`}
                    </b>
                    <time>
                      {event.event === "move" ? t("timeline.adjustTo") : ""}
                      {formatLocalizedDateTime(event.t, language)}
                    </time>
                    {event.event === "move" && event.recordedAt && (
                      <small>{t("timeline.recordedAt", { date: formatLocalizedDateTime(event.recordedAt, language) })}</small>
                    )}
                    {event.note && <small>「{event.note}」</small>}
                  </div>
                </li>
              );
            })}
          </ol>
        )}
      </section>

      <div className="node-timeline-help">
        {t("timeline.help")}
      </div>
    </div>
  );
}
