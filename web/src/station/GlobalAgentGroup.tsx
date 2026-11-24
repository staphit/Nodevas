// Multi-repo global agent group view (§10.2, §11 item 8): shows group counts,
// per-repo status pips, waiting count, usage-limit pause banner with "Resume
// now", budget-stop notice, per-repo folder cards with pose stills, sequential
// "Apply all finished" with per-repo conflict reporting, group follow-up ("Tell
// all" / "Tell failed"), and an inline RunDialog for the selected member.

import { useEffect, useMemo, useState } from "react";

import { runFinal, type Run } from "../api";
import { confirmAction } from "../components/ConfirmDialog";
import { agentWho } from "../game/labels";
import { CastPortrait, useFolderArt } from "../game/warroom/castArt";
import { formatLocalizedTime, useI18n } from "../i18n";
import { IconFolder } from "../icons";
import { useLocalRunner } from "../runners/edition";
import { RunDialog } from "../runs/dialog/RunDialog";
import { followUpErrorKey } from "../runs/dialog/followUpErrors";
import { RunStatusBadge } from "../runs/NodeRuns";
import { ResultActions } from "../runs/ResultActions";
import { resultState } from "../runs/resultState";
import { selectRunById, useRuns } from "../runs/runStore";
import { useWorkspace } from "../runs/workspaceStore";
import { collapseGlobalChains, useGlobalAgents } from "./globalAgentsStore";

export interface GlobalAgentGroupProps {
  groupId: string;
  /** Member runs already known from the global list (fallback before loadGroup). */
  runs: Run[];
  expanded: boolean;
  onToggle?: () => void;
  own: boolean;
  single?: boolean;
  simple?: boolean;
  focused?: boolean;
}

interface ApplyReportItem {
  runId: string;
  repo: string;
  ok: boolean;
  detail: string;
}

function pipTone(run: Run): "done" | "failed" | "cancelled" | "running" | "queued" {
  if (run.status === "succeeded") return "done";
  if (run.status === "failed" || run.status === "declined") return "failed";
  if (run.status === "cancelled") return "cancelled";
  if (run.status === "running") return "running";
  return "queued";
}

function poseScene(run: Run): string {
  if (!runFinal(run.status)) return "write";
  return "idle";
}

function formatHoldTime(iso: string | undefined): string {
  if (!iso) return "";
  const ms = Date.parse(iso);
  if (!Number.isFinite(ms) || ms <= Date.now()) return "";
  return formatLocalizedTime(iso);
}

function canApplyMember(run: Run): boolean {
  if (run.status !== "succeeded" || !run.commit || run.workspace) return false;
  if (run.prUrl && run.prState !== "closed") return false;
  if (run.workspaceAction?.status === "pending") return false;
  return true;
}

function GroupMemberCard({
  run,
  selected,
  onSelect,
  plain,
}: {
  run: Run;
  selected: boolean;
  onSelect: () => void;
  plain: boolean;
}) {
  const { t } = useI18n();
  const repo = run.repoName || run.repoId;
  const folderArt = useFolderArt(run.repoId);
  const noChange = run.status === "succeeded" && !run.commit;
  const budgetStopped = run.failureCode === "budget";

  return (
    <div
      className={`global-group-member${selected ? " is-selected" : ""}${budgetStopped ? " is-budget" : ""}`}
      data-group-member={run.id}
    >
      <button
        type="button"
        className="global-group-member-card"
        aria-pressed={selected}
        onClick={onSelect}
      >
        <span className="global-group-member-art" aria-hidden="true">
          {folderArt ? (
            <img
              className="global-group-folder-img"
              src={folderArt.url}
              alt=""
              loading="lazy"
              draggable={false}
              onError={folderArt.onError}
            />
          ) : (
            <IconFolder size={18} />
          )}
          <CastPortrait
            agent={run.agent}
            model={run.model}
            scene={poseScene(run)}
            fallback={null}
            className="global-group-member-pose"
          />
        </span>
        <span className="global-group-member-body">
          <span className="global-group-member-repo">{repo}</span>
          <span className="global-group-member-status">
            <RunStatusBadge status={run.status} run={run} />
            {noChange && <span className="global-group-member-note">{t("globalAgents.group.noChange")}</span>}
          </span>
        </span>
      </button>
      {plain && <ResultActions run={run} compact />}
    </div>
  );
}

export function GlobalAgentGroup({
  groupId,
  runs: propRuns,
  expanded,
  onToggle,
  own,
  simple = false,
  focused = false,
}: GlobalAgentGroupProps) {
  const { t } = useI18n();
  const group = useGlobalAgents((s) => s.groups[groupId]);
  const storedRuns = useGlobalAgents((s) => s.groupRuns[groupId]);
  const groupError = useGlobalAgents((s) => s.groupErrors[groupId]);
  const globalById = useRuns((s) => s.globalById);
  const localRunnerId = useLocalRunner()?.id;

  const [selectedRunId, setSelectedRunId] = useState<string | null>(null);
  const [resuming, setResuming] = useState(false);
  const [stopping, setStopping] = useState(false);
  const [applyingAll, setApplyingAll] = useState(false);
  const [applyReport, setApplyReport] = useState<ApplyReportItem[] | null>(null);
  const [tellMode, setTellMode] = useState<"all" | "failed" | null>(null);
  const [tellText, setTellText] = useState("");
  const [sendingTell, setSendingTell] = useState(false);
  const [tellReport, setTellReport] = useState<{
    none: boolean;
    skipped: { runId: string; repo: string; reason: string }[];
  } | null>(null);

  // Fetch group metadata (heldUntil, repoCount, full member list) when expanded.
  useEffect(() => {
    if (expanded && !group) {
      void useGlobalAgents.getState().loadGroup(groupId);
    }
  }, [expanded, group, groupId]);

  // Collapse follow-up chains per repo so each folder shows its latest turn,
  // preserving the original member ordering for deterministic display & apply.
  const members = useMemo(() => {
    const raw = (storedRuns ?? propRuns).map((run) => globalById[run.id] ?? run);
    const { latest } = collapseGlobalChains(raw);
    const order = new Map<string, number>();
    raw.forEach((run, index) => {
      const key = run.repoId || run.id;
      if (!order.has(key)) order.set(key, index);
    });
    return [...latest].sort((a, b) => {
      const ia = order.get(a.repoId || a.id) ?? 0;
      const ib = order.get(b.repoId || b.id) ?? 0;
      return ia - ib;
    });
  }, [storedRuns, propRuns, globalById]);

  const total = group?.repoCount || members.length;
  const doneCount = members.filter((run) => runFinal(run.status)).length;
  const waitingCount = members.filter((run) => run.status === "queued" || run.status === "offered").length;
  const activeCount = members.filter((run) => !runFinal(run.status)).length;
  const failedCount = members.filter((run) => run.status === "failed" || run.status === "cancelled").length;
  const eligibleApply = members.filter(canApplyMember);
  const hasBudgetStop = members.some((run) => run.failureCode === "budget");
  const holdTime = formatHoldTime(group?.heldUntil);

  const title = group?.title || members[0]?.title || groupId;
  const who = group ? agentWho(group.agent, group.model) : members[0] ? agentWho(members[0].agent, members[0].model) : "";
  const activeMember = selectedRunId ? members.find((m) => m.id === selectedRunId) ?? null : null;

  const onSelectMember = (run: Run) => {
    if (!selectRunById(useRuns.getState(), run.id)) {
      useRuns.getState().putRun(run);
    }
    setSelectedRunId((prev) => (prev === run.id ? null : run.id));
  };

  const onResume = async () => {
    if (resuming) return;
    setResuming(true);
    try {
      await useGlobalAgents.getState().resumeGroup(groupId);
    } finally {
      setResuming(false);
    }
  };

  const onStopAll = async () => {
    if (stopping) return;
    const ok = await confirmAction({
      title: t("globalAgents.action.stopAllTitle", { count: activeCount }),
      description: t("globalAgents.list.stopAllBody"),
      confirmLabel: t("globalAgents.group.stopAll"),
      cancelLabel: t("runs.keep"),
      tone: "danger",
    });
    if (!ok) return;
    setStopping(true);
    try {
      await useGlobalAgents.getState().cancelGroup(groupId);
    } finally {
      setStopping(false);
    }
  };

  const onApplyAll = async () => {
    if (applyingAll || eligibleApply.length === 0) return;
    setApplyingAll(true);
    const results: ApplyReportItem[] = [];
    try {
      for (const member of eligibleApply) {
        const ok = await useWorkspace.getState().mergeLocal(member);
        const ws = useWorkspace.getState();
        const detail = ok
          ? t("result.apply.done")
          : ws.actionErrors[member.id] || ws.actionCodes[member.id] || t("result.refuse.other", { reason: "" });
        results.push({
          runId: member.id,
          repo: member.repoName || member.repoId,
          ok,
          detail,
        });
      }
      setApplyReport(results);
    } finally {
      setApplyingAll(false);
    }
  };

  // Why the hub skipped a member, in the reader's words.
  const skipReason = (code: string) => {
    const key = followUpErrorKey(code, simple);
    if (key) return t(key);
    if (code === "BUDGET_EXCEEDED") return t("globalAgents.group.skipBudget");
    return t("globalAgents.group.skipOther", { code });
  };

  const onSendTell = async () => {
    const trimmed = tellText.trim();
    if (!tellMode || !trimmed || sendingTell) return;
    setSendingTell(true);
    setTellReport(null);
    try {
      const res = await useGlobalAgents.getState().followUpGroup(groupId, { message: trimmed, only: tellMode });
      if (!res) return;
      const started = res.started ?? [];
      const skipped = (res.skipped ?? []).map((item) => {
        const member = members.find((m) => m.id === item.runId) ?? globalById[item.runId];
        return {
          runId: item.runId,
          repo: member?.repoName || member?.repoId || item.runId,
          reason: skipReason(item.code),
        };
      });
      if (skipped.length > 0 || started.length === 0) {
        setTellReport({ none: started.length === 0, skipped });
      }
      // Nothing started: the text stays, so the person can change it and try again.
      if (started.length > 0) {
        setTellText("");
        setTellMode(null);
      }
    } finally {
      setSendingTell(false);
    }
  };

  return (
    <div className={`global-group${expanded ? " is-expanded" : ""}${focused ? " is-focus" : ""}`}>
      <div className="global-group-head">
        <div className="global-group-title-wrap">
          {onToggle ? (
            <button
              type="button"
              className="global-agent-task-toggle global-group-toggle"
              aria-expanded={expanded}
              onClick={onToggle}
            >
              {title}
            </button>
          ) : (
            <span className="global-group-title">{title}</span>
          )}
          {who && <span className="global-group-who">{who}</span>}
        </div>
        <div className="global-group-summary">
          <span className="global-agent-chip">{t("globalAgents.group.title", { count: total })}</span>
          <span className="global-group-progress">{t("globalAgents.group.progress", { done: doneCount, count: total })}</span>
          {waitingCount > 0 && (
            <span className="global-group-waiting">{t("globalAgents.group.waiting", { count: waitingCount })}</span>
          )}
          <ul className="global-group-pips" aria-label={t("globalAgents.group.title", { count: total })}>
            {members.map((member) => (
              <li
                key={member.id}
                className={`global-group-pip is-${pipTone(member)}`}
                title={`${member.repoName || member.repoId}: ${member.status}`}
              />
            ))}
          </ul>
        </div>
      </div>

      {holdTime && (
        <div className="global-group-banner is-paused" role="status">
          <span>{t("globalAgents.group.paused", { time: holdTime })}</span>
          {own && (
            <button type="button" className="ap-btn" disabled={resuming} onClick={() => void onResume()}>
              {t("globalAgents.group.resume")}
            </button>
          )}
        </div>
      )}

      {hasBudgetStop && (
        <p className="global-group-banner is-budget" role="status">
          {t("globalAgents.group.budget")}
        </p>
      )}

      {groupError && (
        <p className="global-agent-error" role="alert">
          {groupError}
        </p>
      )}

      {expanded && (
        <div className="global-group-body">
          {own && (
            <div className="global-group-actions">
              {activeCount > 0 && (
                <button
                  type="button"
                  className="ap-btn is-danger"
                  disabled={stopping}
                  onClick={() => void onStopAll()}
                >
                  {t("globalAgents.group.stopAll")}
                </button>
              )}
              {eligibleApply.length > 0 && (
                <button
                  type="button"
                  className="ap-btn"
                  disabled={applyingAll}
                  onClick={() => void onApplyAll()}
                >
                  {t("globalAgents.group.applyAll")}
                </button>
              )}
              <button
                type="button"
                className="ap-btn"
                onClick={() => setTellMode((prev) => (prev === "all" ? null : "all"))}
              >
                {t("globalAgents.group.tellAll")}
              </button>
              {failedCount > 0 && (
                <button
                  type="button"
                  className="ap-btn"
                  onClick={() => setTellMode((prev) => (prev === "failed" ? null : "failed"))}
                >
                  {t("globalAgents.group.tellFailed")}
                </button>
              )}
            </div>
          )}

          {tellMode && (
            <div className="global-group-tell">
              <textarea
                className="global-group-tell-input"
                value={tellText}
                placeholder={t("runDialog.ph.followUp")}
                onChange={(event) => setTellText(event.target.value)}
                disabled={sendingTell}
              />
              <div className="global-group-tell-buttons">
                <button
                  type="button"
                  className="ap-btn"
                  disabled={sendingTell}
                  onClick={() => {
                    setTellMode(null);
                    setTellText("");
                    setTellReport(null);
                  }}
                >
                  {t("common.cancel")}
                </button>
                <button
                  type="button"
                  className="ap-btn is-primary"
                  disabled={sendingTell || !tellText.trim()}
                  onClick={() => void onSendTell()}
                >
                  {t("runDialog.send")}
                </button>
              </div>
            </div>
          )}

          {tellReport && (
            <div className="global-group-tell-report" data-testid="group-tell-report" role="status">
              {tellReport.none && <p>{t("globalAgents.group.noneStarted")}</p>}
              {tellReport.skipped.length > 0 && (
                <>
                  <p>{t("globalAgents.group.skipped", { count: tellReport.skipped.length })}</p>
                  <ul>
                    {tellReport.skipped.map((item) => (
                      <li key={item.runId}>
                        {t("globalAgents.group.skipLine", { repo: item.repo, reason: item.reason })}
                      </li>
                    ))}
                  </ul>
                </>
              )}
            </div>
          )}

          {applyReport && (
            <ul className="global-group-apply-report" data-testid="group-apply-report" role="status">
              {applyReport.map((item) => (
                <li key={item.runId} className={item.ok ? "is-ok" : "is-error"}>
                  <strong>{item.repo}</strong>: {item.detail}
                </li>
              ))}
            </ul>
          )}

          <div className="global-group-members">
            {members.map((member) => (
              <GroupMemberCard
                key={member.id}
                run={member}
                selected={selectedRunId === member.id}
                onSelect={() => onSelectMember(member)}
                plain={resultState(member, localRunnerId) !== "none"}
              />
            ))}
          </div>

          {activeMember && (
            <div className="global-group-dialog">
              <RunDialog runId={activeMember.id} variant="plain" simple={simple} />
            </div>
          )}
        </div>
      )}
    </div>
  );
}

export default GlobalAgentGroup;
