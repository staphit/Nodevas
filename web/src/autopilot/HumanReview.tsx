import { useId, useState } from "react";
import type { TaskInstance } from "../api";
import { useI18n } from "../i18n";
import { safeExternalUrl } from "../runs/runFormat";
import { voteText } from "./AutopilotGrid";
import { useAutopilot } from "./autopilotStore";
import { openAutopilot } from "./openAutopilot";

/** One task waiting for a person: its PR, the agents' votes, a comment and
 * the two decisions. */
function HumanReviewItem({ autopilotId, task }: { autopilotId: string; task: TaskInstance }) {
  const { t } = useI18n();
  const review = useAutopilot((s) => s.review);
  const [comment, setComment] = useState("");
  const [sending, setSending] = useState<"" | "approve" | "changes">("");
  const commentId = useId();
  const pr = safeExternalUrl(task.prUrl);
  const votes = (task.votes ?? []).filter((vote) => vote.verdict);
  // The hub refuses "changes" without saying what should change: the
  // comment is what the implementer revises against.
  const [missingComment, setMissingComment] = useState(false);

  const decide = async (verdict: "approve" | "changes") => {
    if (!task.pipelineId) return;
    if (verdict === "changes" && !comment.trim()) {
      setMissingComment(true);
      return;
    }
    setMissingComment(false);
    setSending(verdict);
    const ok = await review(autopilotId, task.pipelineId, {
      verdict,
      comment: comment.trim() || undefined,
    });
    setSending("");
    if (ok) setComment("");
  };

  return (
    <li className="ap-review-item">
      <div className="ap-review-head">
        <span className="ap-review-title">{task.title || task.nodeId}</span>
        {pr && (
          <a href={pr} target="_blank" rel="noreferrer noopener">
            {t("autopilot.prLink")}
          </a>
        )}
      </div>
      {votes.length > 0 && (
        <p className="ap-muted">{votes.map((vote) => voteText(t, vote)).join(" · ")}</p>
      )}
      <label className="run-field" htmlFor={commentId}>
        <span>{t("autopilot.reviewComment")}</span>
        <textarea
          id={commentId}
          rows={2}
          value={comment}
          maxLength={4000}
          placeholder={t("autopilot.reviewCommentPlaceholder")}
          aria-invalid={missingComment ? true : undefined}
          onChange={(event) => {
            setComment(event.target.value);
            if (event.target.value.trim()) setMissingComment(false);
          }}
        />
      </label>
      {missingComment && (
        <p className="autopilot-error" role="alert">
          {t("autopilot.changesNeedComment")}
        </p>
      )}
      <div className="ap-actions">
        <button
          type="button"
          className="ap-btn"
          disabled={sending !== "" || !task.pipelineId}
          onClick={() => void decide("changes")}
        >
          {sending === "changes" ? t("autopilot.sending") : t("autopilot.requestChanges")}
        </button>
        <button
          type="button"
          className="run-dispatch-button"
          disabled={sending !== "" || !task.pipelineId}
          onClick={() => void decide("approve")}
        >
          {sending === "approve" ? t("autopilot.sending") : t("autopilot.approve")}
        </button>
      </div>
    </li>
  );
}

/** The tasks of one autopilot run waiting for a person. */
export function HumanReviewList({
  autopilotId,
  tasks,
}: {
  autopilotId: string;
  tasks: TaskInstance[];
}) {
  const { t } = useI18n();
  const titleId = useId();
  if (tasks.length === 0) return null;
  return (
    <section className="ap-section ap-review" aria-labelledby={titleId}>
      <h4 id={titleId} className="ap-section-title">
        {t("autopilot.waitingTitle", { count: tasks.length })}
      </h4>
      <ul className="ap-review-list">
        {tasks.map((task) => (
          <HumanReviewItem key={task.nodeId} autopilotId={autopilotId} task={task} />
        ))}
      </ul>
    </section>
  );
}

/** "2 waiting for you": opens the first waiting task's goal on its Autopilot
 * tab. Nothing while no held run has a task waiting for a person. */
export function WaitingForYouButton() {
  const { t } = useI18n();
  const waiting = useAutopilot((s) => s.waiting);
  if (waiting.length === 0) return null;
  const first = waiting[0];
  return (
    <button
      type="button"
      className="ap-waiting-pill"
      onClick={() => openAutopilot(first.goalNodeId)}
      title={t("autopilot.waitingTitle", { count: waiting.length })}
    >
      {t("autopilot.waitingPill", { count: waiting.length })}
    </button>
  );
}
