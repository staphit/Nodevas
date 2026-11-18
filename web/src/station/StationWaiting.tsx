// "Waiting on you", first in Station's main column: everything the hub knows
// is stopped on a person (station/waiting.ts), each a tile (StationTile, the
// shape of a node card) with the one action that unblocks it and, where it
// helps, a second. The answers themselves happen
// where they always did: the war room's ask and held-mail dialogs, a
// goal's review, a node's Runs tab. An offer the app's own runner asks about
// (single-user edition) is answered right in its tile, and so is a finished
// result there: looked at and applied with its plain buttons. Once applied
// it no longer waits, but its tile stays where the person pressed Apply,
// saying so with "Open my folder", until they dismiss it or leave Station.

import { useEffect, useMemo, type ReactNode } from "react";

import { excerpt } from "../agents/agentStore";
import type { Run } from "../api";
import { openAutopilot } from "../autopilot/openAutopilot";
import { useGameActions } from "../game/GameActions";
import { agentWho } from "../game/labels";
import { useI18n } from "../i18n";
import { runnerDisplayName } from "../runners/runnerName";
import { confirmCancelRun } from "../runs/confirmCancelRun";
import { OfferAskCard, runAsksInApp } from "../runs/OfferAskCard";
import { askMachine } from "../runs/offerReasons";
import { openNodeRuns } from "../runs/openRuns";
import { ResultActions } from "../runs/ResultActions";
import { useRuns } from "../runs/runStore";
import { openSettings } from "../settings/openSettings";
import { useSimpleMode } from "../simple/simpleMode";
import { useGlobalAgents } from "./globalAgentsStore";
import { dismissResult, placeResults, useKeptResults } from "./keptResults";
import { useTitleOf } from "./StationControl";
import { StationTile, StationTiles } from "./StationTile";
import { useStation } from "./stationStore";
import { useWaitingItems } from "./useWaitingItems";
import { waitingKey, type WaitingItem } from "./waiting";

interface Action {
  label: string;
  run: () => void;
  disabled?: boolean;
}

interface Row {
  text: string;
  sub?: string;
  /** The node it is about: its tile shows the node's type. */
  nodeId?: string;
  primary?: Action;
  secondary?: Action;
  /** In place of the buttons: the ask card. */
  body?: ReactNode;
}

/** A tile in the list: something that waits, or a result applied during
 * this visit, kept in place. */
type Shown = { item: WaitingItem } | { applied: Run };

/** The copy of a run, from any list that holds it, that says it was applied
 * to the person's folder; undefined while none does. Applied never goes
 * back, so any copy saying so is the truth. */
function useAppliedCopy(): (runId: string) => Run | undefined {
  const globalById = useRuns((s) => s.globalById);
  const runsByNode = useRuns((s) => s.runsByNode);
  const globalRuns = useGlobalAgents((s) => s.runs);
  const stationRuns = useStation((s) => s.runs);
  return useMemo(() => {
    const applied = new Map<string, Run>();
    const add = (run: Run | undefined) => {
      if (run?.id && run.workspace === "merged_local" && !applied.has(run.id)) applied.set(run.id, run);
    };
    Object.values(globalById).forEach(add);
    for (const list of Object.values(runsByNode)) list?.forEach(add);
    globalRuns.forEach(add);
    stationRuns.forEach(add);
    return (runId: string) => applied.get(runId);
  }, [globalById, runsByNode, globalRuns, stationRuns]);
}

export function StationWaiting() {
  const { t } = useI18n();
  const simple = useSimpleMode();
  const cancelling = useRuns((s) => s.cancelling);
  const { dispatch } = useGameActions();
  const titleOf = useTitleOf();
  const items = useWaitingItems();
  const appliedOf = useAppliedCopy();
  // Results seen waiting during this visit, at their last place in the
  // list; and the applied ones the person dismissed (keptResults).
  const placed = useKeptResults((s) => s.placed);
  const dismissedIds = useKeptResults((s) => s.dismissed);
  const dismissed = useMemo(() => new Set(Object.keys(dismissedIds)), [dismissedIds]);

  useEffect(() => {
    placeResults(items.map((item) => (item.kind === "apply" ? item.run.id : null)));
  }, [items]);

  // What waits (the count, the call for attention) and what is shown: a
  // result applied here stays in its place, applied, until dismissed.
  const waiting: WaitingItem[] = [];
  const shown: Shown[] = [];
  const applyIds = new Set<string>();
  for (const item of items) {
    const applied = item.kind === "apply" ? appliedOf(item.run.id) : undefined;
    if (item.kind === "apply") applyIds.add(item.run.id);
    if (applied) {
      if (!dismissed.has(applied.id)) shown.push({ applied });
      continue;
    }
    waiting.push(item);
    shown.push({ item });
  }
  for (const [id, index] of Object.entries(placed).sort((a, b) => a[1] - b[1])) {
    if (applyIds.has(id) || dismissed.has(id)) continue;
    const applied = appliedOf(id);
    if (applied) shown.splice(Math.min(index, shown.length), 0, { applied });
  }

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
      case "ask": {
        const isPermission = item.ask.kind === "permission";
        const agentName = item.ask.sessionName || agentWho(item.ask.agent ?? "", "");
        const text = isPermission
          ? t("station.waiting.permissionAsk", {
              agent: agentName,
              tool: item.ask.tool || "tool",
            })
          : t("station.waiting.ask", {
              agent: agentName,
              question: excerpt(item.ask.question),
            });
        const sub = isPermission && item.ask.toolInput
          ? excerpt(item.ask.toolInput)
          : item.ask.nodeId
            ? titleOf(item.ask.nodeId)
            : undefined;
        return {
          text,
          sub,
          nodeId: item.ask.nodeId || undefined,
          primary: {
            label: t("station.waiting.answer"),
            run: () => dispatch({ kind: "ask", askId: item.ask.id, sessionId: item.ask.sessionId }),
          },
        };
      }
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
          nodeId: item.task.task.nodeId,
          primary: { label: t("station.waiting.reviewOpen"), run: () => openAutopilot(item.task.goalNodeId) },
        };
      case "merge":
        return {
          text: t("station.waiting.merge", { number: item.run.prNumber ?? "", name: titleOf(item.run.nodeId) }),
          nodeId: item.run.nodeId,
          primary: { label: t("station.waiting.review"), run: () => openNodeRuns(item.run.nodeId, item.run.id) },
        };
      case "ci":
        return {
          text: t("station.waiting.ci", { number: item.run.prNumber ?? "", name: titleOf(item.run.nodeId) }),
          sub: agentWho(item.run.agent, item.run.model),
          nodeId: item.run.nodeId,
          primary: openRun(item.run),
          secondary: item.run.prUrl
            ? {
                label: t("station.waiting.openPr"),
                run: () => dispatch({ kind: "pr", runId: item.run.id, url: item.run.prUrl ?? "" }),
              }
            : undefined,
        };
      case "apply":
        return {
          text: t("result.waiting", {
            name: item.run.global ? item.run.title || item.run.id : titleOf(item.run.nodeId),
          }),
          sub: t("result.waiting.sub", { agent: agentWho(item.run.agent, item.run.model) }),
          nodeId: item.run.nodeId || undefined,
          body: <ResultActions run={item.run} compact />,
        };
      case "offer":
        if (runAsksInApp(item.run)) {
          return {
            text: titleOf(item.run.nodeId),
            sub: agentWho(item.run.agent, item.run.model),
            nodeId: item.run.nodeId,
            body: <OfferAskCard run={item.run} className="station-waiting-ask" />,
          };
        }
        return {
          text: t("station.waiting.offer", { runner: runnerDisplayName(t, item.run.runnerName) || "—", name: titleOf(item.run.nodeId) }),
          sub: `${agentWho(item.run.agent, item.run.model)} · ${
            item.run.ask ? t("offerAsk.waitingOn", { machine: askMachine(t, item.run) }) : t("station.waiting.offerHint")
          }`,
          nodeId: item.run.nodeId,
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
          nodeId: item.run.nodeId,
          primary: openRun(item.run),
        };
    }
  };

  return (
    <section
      className={`station-panel station-waiting${waiting.length > 0 ? " is-attention" : ""}`}
      aria-labelledby="station-waiting-title"
    >
      <div className="station-panel-head">
        <h2 id="station-waiting-title">{t("station.waiting.title")}</h2>
        {waiting.length > 0 && <span className="station-count">{waiting.length}</span>}
      </div>
      {shown.length === 0 ? (
        <p className="station-empty">{t(simple ? "simple.waiting.empty" : "station.waiting.empty")}</p>
      ) : (
        <StationTiles label={t("station.waiting.title")}>
          {shown.map((entry) => {
            if ("applied" in entry) {
              const run = entry.applied;
              // The key it had while it waited: the result buttons stay
              // the same ones, now saying it was applied.
              return (
                <StationTile
                  key={waitingKey({ kind: "apply", run })}
                  dot="succeeded"
                  className="station-waiting-item is-apply is-applied"
                  title={t("result.waiting.applied", {
                    name: run.global ? run.title || run.id : titleOf(run.nodeId),
                  })}
                  nodeId={run.nodeId || undefined}
                >
                  <ResultActions run={run} compact />
                  <span className="station-waiting-actions">
                    <button type="button" className="ap-btn" onClick={() => dismissResult(run.id)}>
                      {t("result.dismiss")}
                    </button>
                  </span>
                </StationTile>
              );
            }
            const { item } = entry;
            const { text, sub, nodeId, primary, secondary, body } = row(item);
            return (
              <StationTile
                key={waitingKey(item)}
                dot="waiting"
                className={`station-waiting-item is-${item.kind}`}
                title={text}
                nodeId={nodeId}
                meta={sub}
              >
                {body}
                {primary && (
                  <span className="station-waiting-actions">
                    <button
                      type="button"
                      className="ap-btn is-primary"
                      disabled={primary.disabled}
                      onClick={primary.run}
                    >
                      {primary.label}
                    </button>
                    {secondary && (
                      <button type="button" className="ap-btn" disabled={secondary.disabled} onClick={secondary.run}>
                        {secondary.label}
                      </button>
                    )}
                  </span>
                )}
              </StationTile>
            );
          })}
        </StationTiles>
      )}
    </section>
  );
}
