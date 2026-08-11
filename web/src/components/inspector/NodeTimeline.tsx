/**
 * Per-node schedule panel [B-06].
 *
 * Expected milestones (graph.yaml) on top, actual journal entries below, and an
 * assessment line that compares the two — the one place where plan and record
 * are deliberately shown together.
 */

import { BUILTIN_PLAN_STATUSES, planStatusShortLabel } from "../../plan";
import { operationScope, reportError, useApp, useOperation } from "../../store";
import { statusTheme } from "../../statusTheme";
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
    const action = status === "started" ? "開工" : "完成";
    if (!plan) {
      return {
        kind: "neutral",
        result: `未設定預計${action}日`,
        detail: actual ? `實際：${localDateKey(actual.t)}` : "尚無實際紀錄",
      };
    }
    if (actual) {
      const actualDate = localDateKey(actual.t);
      const difference = calendarDayDifference(actualDate, plan.date);
      return {
        kind: difference <= 0 ? "on-time" : "late",
        result:
          difference === 0
            ? `如期${action}`
            : difference < 0
              ? `提前 ${Math.abs(difference)} 天${action}`
              : `延遲 ${difference} 天${action}`,
        detail: `預期 ${plan.date} · 實際 ${actualDate}`,
      };
    }
    const today = localDateKey(new Date());
    const difference = calendarDayDifference(today, plan.date);
    return {
      kind: difference > 0 ? "late" : "pending",
      result:
        difference > 0
          ? `逾期 ${difference} 天未${action}`
          : difference === 0
            ? `預計今日${action}`
            : `距預計${action} ${Math.abs(difference)} 天`,
      detail: `預期 ${plan.date} · 尚無實際紀錄`,
    };
  };

  const startAssessment = assessment("started");
  const doneAssessment = assessment("done");

  return (
    <div className="node-timeline" role="tabpanel" aria-label="時間軸">
      <div className="node-timeline-assessments">
        {[
          ["開工", startAssessment],
          ["完成", doneAssessment],
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
          <h3>預期里程碑</h3>
          <span className="section-hint">寫入 graph.yaml，選定日期即儲存</span>
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
            ...BUILTIN_PLAN_STATUSES,
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
                  title="時間（選填，精確到分鐘）"
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
              <span>{planStatusShortLabel(plan.status, planStatusDefinitions)}</span>
              <time>{plan.date}</time>
              <button type="button" onClick={() => commitPlanDate(plan.status, "")}>
                移除
              </button>
            </div>
          ))}
      </section>

      <section className="node-timeline-audit">
        <h3>實際異動順序</h3>
        {auditEvents.length === 0 ? (
          <div className="node-timeline-empty">尚無實際紀錄</div>
        ) : (
          <ol>
            {auditEvents.map(({ event, historyIndex }, sequence) => {
              const referenced = event.ref ? statusByID.get(event.ref) : undefined;
              const status = (event.event === "move"
                ? referenced?.to
                : event.to) as Status | undefined;
              const statusLabel = status
                ? statusTheme(status, customStatuses).label
                : "事件";
              const fromLabel = event.from
                ? statusTheme(event.from, customStatuses).label
                : "未開始";
              return (
                <li key={`${event.id || event.ref || historyIndex}-${sequence}`}>
                  <span className={`timeline-audit-kind ${event.event}`}>
                    {event.event === "move" ? "調整" : "實際"}
                  </span>
                  <div>
                    <b>
                      {event.event === "move"
                        ? `調整${statusLabel}日期`
                        : `${fromLabel} → ${
                            status ? statusLabel : event.to
                          }`}
                    </b>
                    <time>
                      {event.event === "move" ? "調整為 " : ""}
                      {new Date(event.t).toLocaleString()}
                    </time>
                    {event.event === "move" && event.recordedAt && (
                      <small>異動於 {new Date(event.recordedAt).toLocaleString()}</small>
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
        拖曳時間軸上的「實際」卡片可修正日期；每次修正都會新增調整紀錄。
      </div>
    </div>
  );
}
