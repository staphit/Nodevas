// "Waiting on you", first in Station's main column: everything the hub knows
// is stopped on a person (station/waiting.ts), each with the one action that
// unblocks it and, where it helps, a second. The answers themselves happen
// where they always did: the war room's ask and held-mail dialogs, a
// goal's review, a node's Runs tab.

import { useMemo } from "react";

import { canManageSession, excerpt, useAgents } from "../agents/agentStore";
import type { Run } from "../api";
import { useAutopilot } from "../autopilot/autopilotStore";
import { openAutopilot } from "../autopilot/openAutopilot";
import { useGameActions } from "../game/GameActions";
import { agentWho } from "../game/labels";
import { useI18n } from "../i18n";
import { useBudget } from "../runs/budgetStore";
import { confirmCancelRun } from "../runs/confirmCancelRun";
import { openNodeRuns } from "../runs/openRuns";
import { useRuns } from "../runs/runStore";
import { openSettings } from "../settings/openSettings";
import { useStation } from "./stationStore";
import { useTitleOf } from "./StationControl";
import { waitingKey, waitingOnYou, type WaitingItem } from "./waiting";

interface Action {
  label: string;
  run: () => void;
  disabled?: boolean;
}

interface Row {
  text: string;
  sub?: string;
  primary: Action;
  secondary?: Action;
}

export function StationWaiting() {
  const { t } = useI18n();
  const runs = useStation((s) => s.runs);
  const active = useRuns((s) => s.activeRuns);
  const asks = useAgents((s) => s.asks);
  const held = useAgents((s) => s.held);
  const viewer = useAgents((s) => s.viewer);
  const reviews = useAutopilot((s) => s.waiting);
  const exceeded = useBudget((s) => s.status?.exceeded);
  const cancelling = useRuns((s) => s.cancelling);
  const { dispatch } = useGameActions();
  const titleOf = useTitleOf();

  const items = useMemo(
    () =>
      waitingOnYou({
        runs,
        active,
        asks,
        held,
        reviews,
        budgetExceeded: exceeded,
        canAnswer: (ask) => canManageSession(ask, viewer),
        now: Date.now(),
      }),
    [runs, active, asks, held, reviews, exceeded, viewer],
  );

  const openRun = (run: Run): Action => ({
    label: t("station.waiting.openRun"),
    run: () => openNodeRuns(run.nodeId, run.id),
  });

  const row = (item: WaitingItem): Row => {
    switch (item.kind) {
      case "budget":
        return {
          text: t("budget.exceeded", { limit: t(`budget.limit.${item.limit}`) }),
          primary: { label: t("budget.adjust"), run: () => openSettings("budget") },
        };
      case "ask":
        return {
          text: t("station.waiting.ask", {
            agent: item.ask.sessionName || agentWho(item.ask.agent ?? "", ""),
            question: excerpt(item.ask.question),
          }),
          sub: item.ask.nodeId ? titleOf(item.ask.nodeId) : undefined,
          primary: {
            label: t("station.waiting.answer"),
            run: () => dispatch({ kind: "ask", askId: item.ask.id, sessionId: item.ask.sessionId }),
          },
        };
      case "held":
        return {
          text: t("station.waiting.held", { from: item.message.from, to: item.message.to }),
          sub: excerpt(item.message.body),
          primary: {
            label: t("station.waiting.decide"),
            run: () => dispatch({ kind: "held-message", messageId: item.message.id }),
          },
        };
      case "review":
        return {
          text: t("station.waiting.reviewTask", { name: item.task.task.title || titleOf(item.task.task.nodeId) }),
          sub: t("station.goal.title", { name: titleOf(item.task.goalNodeId) }),
          primary: { label: t("station.waiting.reviewOpen"), run: () => openAutopilot(item.task.goalNodeId) },
        };
      case "merge":
        return {
          text: t("station.waiting.merge", { number: item.run.prNumber ?? "", name: titleOf(item.run.nodeId) }),
          primary: { label: t("station.waiting.review"), run: () => openNodeRuns(item.run.nodeId, item.run.id) },
        };
      case "ci":
        return {
          text: t("station.waiting.ci", { number: item.run.prNumber ?? "", name: titleOf(item.run.nodeId) }),
          sub: agentWho(item.run.agent, item.run.model),
          primary: openRun(item.run),
          secondary: item.run.prUrl
            ? {
                label: t("station.waiting.openPr"),
                run: () => dispatch({ kind: "pr", runId: item.run.id, url: item.run.prUrl ?? "" }),
              }
            : undefined,
        };
      case "offer":
        return {
          text: t("station.waiting.offer", { runner: item.run.runnerName || "—", name: titleOf(item.run.nodeId) }),
          sub: `${agentWho(item.run.agent, item.run.model)} · ${t("station.waiting.offerHint")}`,
          primary: openRun(item.run),
          secondary: {
            label: cancelling[item.run.id] ? t("runs.cancelling") : t("station.waiting.withdraw"),
            disabled: Boolean(cancelling[item.run.id]),
            run: () =>
              void confirmCancelRun(t, item.run).then((ok) => {
                if (ok) void useRuns.getState().cancelRun(item.run.id);
              }),
          },
        };
      case "limit":
        return {
          text: t("station.waiting.limit", { agent: agentWho(item.run.agent, item.run.model), name: titleOf(item.run.nodeId) }),
          sub: item.tookOver
            ? t("station.waiting.tookOver", { agent: agentWho(item.tookOver.agent, item.tookOver.model) })
            : undefined,
          primary: openRun(item.run),
        };
    }
  };

  return (
    <section
      className={`station-panel station-waiting${items.length > 0 ? " is-attention" : ""}`}
      aria-labelledby="station-waiting-title"
    >
      <div className="station-panel-head">
        <h2 id="station-waiting-title">{t("station.waiting.title")}</h2>
        {items.length > 0 && <span className="station-count">{items.length}</span>}
      </div>
      {items.length === 0 ? (
        <p className="station-empty">{t("station.waiting.empty")}</p>
      ) : (
        <ul className="station-waiting-list">
          {items.map((item) => {
            const { text, sub, primary, secondary } = row(item);
            return (
              <li key={waitingKey(item)} className={`station-waiting-item is-${item.kind}`}>
                <span className="station-waiting-text">
                  {text}
                  {sub && <span className="station-waiting-sub">{sub}</span>}
                </span>
                <span className="station-waiting-actions">
                  <button type="button" className="ap-btn is-primary" disabled={primary.disabled} onClick={primary.run}>
                    {primary.label}
                  </button>
                  {secondary && (
                    <button type="button" className="ap-btn" disabled={secondary.disabled} onClick={secondary.run}>
                      {secondary.label}
                    </button>
                  )}
                </span>
              </li>
            );
          })}
        </ul>
      )}
    </section>
  );
}
