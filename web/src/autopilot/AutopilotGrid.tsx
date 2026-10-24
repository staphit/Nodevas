import { useMemo } from "react";
import {
  STAGE_STATUSES,
  TASK_STAGES,
  TASK_STATES,
  type AutopilotPolicy,
  type StageRecord,
  type TaskInstance,
} from "../api";
import { formatLocalizedDateTime, useI18n } from "../i18n";
import { CIBadge } from "../runs/RunChanges";
import { safeExternalUrl } from "../runs/runFormat";
import { quorumNeeded } from "./policy";

/** The glyph beside a stage status: identity never rests on color alone. */
export const STAGE_GLYPH: Record<string, string> = {
  pending: "◷",
  running: "●",
  success: "✓",
  failed: "✕",
  skipped: "↷",
  waiting: "⏸",
};

/** The glyph beside a task state. */
export const STATE_GLYPH: Record<string, string> = {
  none: "·",
  scheduled: "◔",
  queued: "◷",
  running: "●",
  waiting_review: "⧗",
  waiting_human: "?",
  success: "✓",
  failed: "✕",
  upstream_failed: "↑✕",
  skipped: "↷",
};

const KNOWN_STATE = new Set<string>(TASK_STATES);
const KNOWN_STATUS = new Set<string>(STAGE_STATUSES);

export function stateClass(state: string): string {
  return KNOWN_STATE.has(state) ? state : "none";
}

function statusClass(status: string): string {
  return KNOWN_STATUS.has(status) ? status : "pending";
}

/** A task state as a chip: glyph and words, colored by state. */
export function TaskStateChip({ state }: { state: string }) {
  const { t } = useI18n();
  const known = stateClass(state);
  const label = KNOWN_STATE.has(state) ? t(`autopilot.state.${state}`) : state;
  return (
    <span className={`ap-state ap-state-${known}`}>
      <span className="ap-glyph" aria-hidden="true">
        {STATE_GLYPH[known]}
      </span>
      {label}
    </span>
  );
}

/** Depth of each task in the DAG: 0 without upstream tasks in the list, else
 * one more than its deepest upstream task. */
export function taskDepths(tasks: TaskInstance[]): Record<string, number> {
  const byId = new Map(tasks.map((task) => [task.nodeId, task]));
  const depth: Record<string, number> = {};
  const visiting = new Set<string>();
  const walk = (id: string): number => {
    if (id in depth) return depth[id];
    if (visiting.has(id)) return 0; // a cycle the hub should not send
    visiting.add(id);
    const task = byId.get(id);
    let d = 0;
    for (const up of task?.upstream ?? []) {
      if (byId.has(up)) d = Math.max(d, walk(up) + 1);
    }
    visiting.delete(id);
    depth[id] = d;
    return d;
  };
  for (const task of tasks) walk(task.nodeId);
  return depth;
}

/** The records a task holds for one stage, oldest first. */
function recordsFor(task: TaskInstance, stage: string): StageRecord[] {
  return (task.stages ?? []).filter((record) => record.stage === stage);
}

function StageCell({ task, stage }: { task: TaskInstance; stage: string }) {
  const { t } = useI18n();
  const records = recordsFor(task, stage);
  const stageLabel = t(`autopilot.stage.${stage}`);
  if (records.length === 0) {
    return (
      <td className="ap-cell-td">
        <span
          className="ap-cell ap-cell-empty"
          aria-label={t("autopilot.cellNone", { stage: stageLabel })}
        />
      </td>
    );
  }
  const latest = records[records.length - 1];
  const status = statusClass(latest.status);
  const lines = records.map((record) => {
    const parts = [t(`autopilot.stageStatus.${statusClass(record.status)}`)];
    if (record.round) parts.push(t("autopilot.roundN", { n: record.round }));
    if (record.at) parts.push(formatLocalizedDateTime(record.at));
    if (record.detail) parts.push(record.detail);
    return parts.join(" · ");
  });
  const title = `${stageLabel}\n${lines.join("\n")}`;
  return (
    <td className="ap-cell-td">
      <span
        className={`ap-cell ap-cell-${status}`}
        title={title}
        aria-label={`${stageLabel}: ${lines.join("; ")}`}
        role="img"
      >
        <span aria-hidden="true">{STAGE_GLYPH[status]}</span>
        {records.length > 1 && (
          <span className="ap-cell-rounds" aria-hidden="true">
            {records.length}
          </span>
        )}
      </span>
    </td>
  );
}

/** The current round's votes: "✓✓✕ 2/2", agents first; a person's vote
 * (agent "human") is drawn apart and does not count toward the quorum. */
function Votes({ task, policy }: { task: TaskInstance; policy: AutopilotPolicy }) {
  const { t } = useI18n();
  const votes = task.votes ?? [];
  if (votes.length === 0) return <span className="ap-muted">—</span>;
  const round = Math.max(...votes.map((vote) => vote.round || 0));
  const current = votes.filter((vote) => (vote.round || 0) === round);
  const agents = current.filter((vote) => vote.agent !== "human");
  const people = current.filter((vote) => vote.agent === "human");
  const approvals = agents.filter((vote) => vote.verdict === "approve").length;
  const needed = quorumNeeded(policy) || agents.length;
  return (
    <span
      className="ap-votes"
      aria-label={t("autopilot.votesLabel", { approvals, needed, round })}
      title={current.map((vote) => voteText(t, vote)).join("\n")}
    >
      {agents.map((vote, index) => (
        <span
          key={index}
          className={`ap-vote ap-vote-${vote.verdict || "pending"}`}
          aria-hidden="true"
        >
          {VOTE_GLYPH[vote.verdict] ?? "…"}
        </span>
      ))}
      {agents.length > 0 && (
        <span className="ap-votes-count" aria-hidden="true">
          {approvals}/{needed}
        </span>
      )}
      {people.map((vote, index) => (
        <span
          key={`p${index}`}
          className={`ap-vote ap-vote-person ap-vote-${vote.verdict || "pending"}`}
          aria-hidden="true"
        >
          {VOTE_GLYPH[vote.verdict] ?? "…"}
        </span>
      ))}
    </span>
  );
}

/** "codex/gpt-5.5: Approved", "patrick (person): Changes requested". */
export function voteText(
  t: (key: string, values?: Record<string, string | number>) => string,
  vote: { agent: string; model: string; verdict: string },
): string {
  const who =
    vote.agent === "human"
      ? t("autopilot.humanVoter", { name: vote.model || "?" })
      : `${vote.agent}/${vote.model}`;
  const verdict =
    vote.verdict === "approve" || vote.verdict === "changes" || vote.verdict === "abstain"
      ? t(`autopilot.verdict.${vote.verdict}`)
      : t("autopilot.votePending");
  return `${who}: ${verdict}`;
}

const VOTE_GLYPH: Record<string, string> = { approve: "✓", changes: "✕", abstain: "–" };

/** Legend for the grid: every stage status and task state, glyph and words. */
export function AutopilotLegend() {
  const { t } = useI18n();
  return (
    <div className="ap-legend" aria-label={t("autopilot.legend")}>
      <span className="ap-legend-group">
        {STAGE_STATUSES.map((status) => (
          <span key={status} className="ap-legend-item">
            <span className={`ap-cell ap-cell-${status}`} aria-hidden="true">
              {STAGE_GLYPH[status]}
            </span>
            {t(`autopilot.stageStatus.${status}`)}
          </span>
        ))}
      </span>
    </div>
  );
}

/**
 * The autopilot's tasks as Airflow draws a DAG run's task instances: one row
 * per task in topological order (indented by depth), one column per stage,
 * each cell the stage's latest status with every round in its tooltip.
 */
export function AutopilotGrid({
  tasks,
  policy,
  active,
  busy,
  onOpen,
  onTaskAction,
}: {
  tasks: TaskInstance[];
  policy: AutopilotPolicy;
  /** Whether the run still takes retry and skip. */
  active: boolean;
  /** Task node ids with an action in flight. */
  busy: Record<string, boolean>;
  onOpen: (task: TaskInstance) => void;
  onTaskAction: (task: TaskInstance, action: "retry" | "skip") => void;
}) {
  const { t } = useI18n();
  const depths = useMemo(() => taskDepths(tasks), [tasks]);
  const titles = useMemo(
    () => new Map(tasks.map((task) => [task.nodeId, task.title || task.nodeId])),
    [tasks],
  );
  if (tasks.length === 0) {
    return <p className="run-hint">{t("autopilot.noTasks")}</p>;
  }
  return (
    <div
      className="ap-grid-scroll"
      role="region"
      aria-label={t("autopilot.gridLabel")}
      tabIndex={0}
    >
      <table className="ap-grid">
        <thead>
          <tr>
            <th scope="col" className="ap-col-task">
              {t("autopilot.task")}
            </th>
            <th scope="col" className="ap-col-state">
              {t("autopilot.stateCol")}
            </th>
            {TASK_STAGES.map((stage) => (
              <th
                key={stage}
                scope="col"
                className="ap-col-stage"
                title={t(`autopilot.stage.${stage}`)}
              >
                <span className="ap-stage-head">{t(`autopilot.stageShort.${stage}`)}</span>
              </th>
            ))}
            <th scope="col">{t("autopilot.votes")}</th>
            <th scope="col">{t("autopilot.pr")}</th>
            <th scope="col">
              <span className="visually-hidden">{t("autopilot.actions")}</span>
            </th>
          </tr>
        </thead>
        <tbody>
          {tasks.map((task) => {
            const depth = Math.min(depths[task.nodeId] ?? 0, 6);
            const upstream = (task.upstream ?? []).map((id) => titles.get(id) ?? id);
            const pr = safeExternalUrl(task.prUrl);
            const canRetry =
              active &&
              (task.state === "failed" ||
                task.state === "upstream_failed" ||
                task.state === "skipped");
            const canSkip =
              active &&
              (task.state === "none" ||
                task.state === "scheduled" ||
                task.state === "failed" ||
                task.state === "upstream_failed");
            return (
              <tr key={task.nodeId} className={`ap-row ap-row-${stateClass(task.state)}`}>
                <th scope="row" className="ap-col-task">
                  <button
                    type="button"
                    className="ap-task-open"
                    style={{ paddingInlineStart: `${depth * 12}px` }}
                    onClick={() => onOpen(task)}
                    title={task.reason || undefined}
                  >
                    <span className="ap-task-title">{task.title || task.nodeId}</span>
                    {upstream.length > 0 && (
                      <small className="ap-task-after">
                        {t("autopilot.after", { list: upstream.join(", ") })}
                      </small>
                    )}
                    {task.try > 1 && (
                      <small className="ap-task-after">
                        {t("autopilot.tryN", { n: task.try })}
                      </small>
                    )}
                    {task.advisor && (
                      <small className="ap-task-after ap-task-advisor" title={task.advisor}>
                        {t("autopilot.pickedBy", { reason: task.advisor })}
                      </small>
                    )}
                  </button>
                </th>
                <td className="ap-col-state">
                  <TaskStateChip state={task.state} />
                </td>
                {TASK_STAGES.map((stage) => (
                  <StageCell key={stage} task={task} stage={stage} />
                ))}
                <td>
                  <Votes task={task} policy={policy} />
                </td>
                <td className="ap-col-pr">
                  {pr ? (
                    <a href={pr} target="_blank" rel="noreferrer noopener">
                      {t("autopilot.prLink")}
                    </a>
                  ) : (
                    <span className="ap-muted">—</span>
                  )}
                  <CIBadge status={task.ciStatus} />
                </td>
                <td className="ap-col-actions">
                  {canRetry && (
                    <button
                      type="button"
                      className="run-link-button"
                      disabled={busy[task.nodeId]}
                      onClick={() => onTaskAction(task, "retry")}
                    >
                      {t("autopilot.retry")}
                    </button>
                  )}
                  {canSkip && (
                    <button
                      type="button"
                      className="run-link-button"
                      disabled={busy[task.nodeId]}
                      onClick={() => onTaskAction(task, "skip")}
                    >
                      {t("autopilot.skip")}
                    </button>
                  )}
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}
