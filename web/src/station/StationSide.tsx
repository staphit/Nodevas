// Station's side column: today's spend and who is working, the runners, and
// Agent chat folded into one card. Each keeps its full
// view one step away (Settings → Usage, Settings → Runners, the docked agent
// panel) instead of spreading it open.

import { useCallback, useEffect, useState } from "react";

import { AgentPanel } from "../agents/AgentPanel";
import { unseenCount, useAgents } from "../agents/agentStore";
import { agentColorKey, agentWho, areaName, poseLabel } from "../game/labels";
import type { Actor, World } from "../game/world";
import { useI18n } from "../i18n";
import { useRunners } from "../runners/runnerStore";
import { useBudget } from "../runs/budgetStore";
import { openNodeRuns } from "../runs/openRuns";
import { openSettings } from "../settings/openSettings";
import { reportError, useApp } from "../store";
import { useUsage } from "../usage/usageStore";

const POSE_TONE: Record<Actor["pose"], string> = {
  offline: "muted",
  "asking-user": "ask",
  failed: "bad",
  limit: "wait",
  "awaiting-accept": "wait",
  "waiting-ci": "wait",
  reviewing: "review",
  working: "work",
  finished: "done",
  idle: "muted",
};

/** A letter each that tells the CLIs apart (claude and codex share a C). */
const AGENT_INITIAL: Record<string, string> = {
  claude: "C",
  codex: "X",
  gemini: "G",
  opencode: "O",
  antigravity: "A",
};

function initial(agent: string): string {
  return AGENT_INITIAL[agent.toLowerCase()] ?? (agent || "?").slice(0, 1).toUpperCase();
}

function openActor(actor: Actor) {
  if (actor.nodeId) {
    if (actor.runId) openNodeRuns(actor.nodeId, actor.runId);
    else void useApp.getState().openTab(actor.nodeId).catch(reportError);
  }
}

function compact(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
  if (n >= 1_000) return `${Math.round(n / 1_000)}k`;
  return String(n);
}

function dollars(value: number): string {
  return `$${value.toFixed(2)}`;
}

/** A remembered open/closed state for this browser; closed when unknown. */
function useRememberedOpen(key: string): [boolean, (open: boolean) => void] {
  const [open, setOpen] = useState(() => {
    try {
      return localStorage.getItem(key) === "1";
    } catch {
      return false;
    }
  });
  const set = useCallback(
    (next: boolean) => {
      setOpen(next);
      try {
        localStorage.setItem(key, next ? "1" : "0");
      } catch {
        /* this visit only */
      }
    },
    [key],
  );
  return [open, set];
}

export function StationSide({ world }: { world: World }) {
  return (
    <div className="station-side">
      <StationToday world={world} />
      <StationRunners />
      <StationNetwork />
    </div>
  );
}

/**
 * Today: what the agents cost so far (the usage report's last day, runs and
 * sessions both), the daily budget as a bar when one is set, and who is
 * working, unfolding to the roster. "Details" is Settings → Usage.
 */
function StationToday({ world }: { world: World }) {
  const { t } = useI18n();
  const report = useUsage((s) => s.report);
  const usageError = useUsage((s) => s.error);
  const budget = useBudget((s) => s.status);
  const [rosterOpen, setRosterOpen] = useState(false);
  const { actors } = world.building;
  const working = actors.filter((actor) => actor.pose === "working" || actor.pose === "reviewing").length;
  const placeName = (actor: Actor) =>
    `${areaName(t, actor.area)} · ${actor.runnerName || t("station.roster.terminal")}`;

  useEffect(() => {
    if (!useUsage.getState().loaded) void useUsage.getState().refresh();
  }, []);

  const days = report?.byDay ?? [];
  const today = days.length > 0 ? days[days.length - 1].costUsd : null;
  const limit = budget?.budget.dailyCostUsd ?? 0;
  const used = budget?.used.costUsd ?? 0;

  return (
    <section className="station-panel station-today" aria-labelledby="station-today-title">
      <div className="station-panel-head">
        <h2 id="station-today-title">{t("station.today.title")}</h2>
        <button type="button" className="run-link-button station-head-end" onClick={() => openSettings("usage")}>
          {t("station.today.details")}
        </button>
      </div>
      <p className="station-today-spend">
        {today !== null ? dollars(today) : usageError ? "—" : "…"}
        <span className="sr-only"> {t("station.today.spent")}</span>
      </p>
      {limit > 0 ? (
        <>
          <progress
            className="station-budget-bar"
            max={limit}
            value={Math.min(used, limit)}
            aria-label={t("station.today.budget", { used: dollars(used), limit: dollars(limit) })}
          />
          <p className="station-panel-meta">{t("station.today.budget", { used: dollars(used), limit: dollars(limit) })}</p>
        </>
      ) : (
        <p className="station-panel-meta">{t("station.today.noBudget")}</p>
      )}
      <button
        type="button"
        className="station-fold"
        aria-expanded={rosterOpen}
        aria-controls={rosterOpen ? "station-roster-list" : undefined}
        onClick={() => setRosterOpen((open) => !open)}
      >
        <span aria-hidden="true">{rosterOpen ? "▾" : "▸"}</span>{" "}
        {t("station.roster.count", { working, total: actors.length })}
      </button>
      {rosterOpen &&
        (actors.length === 0 ? (
          <p className="station-empty">{t("station.roster.empty")}</p>
        ) : (
          <ul id="station-roster-list" className="station-roster-list" aria-label={t("station.roster.title")}>
            {actors.map((actor) => (
              <li key={actor.id}>
                <button
                  type="button"
                  className={`station-actor tone-${POSE_TONE[actor.pose]}`}
                  onClick={() => openActor(actor)}
                  disabled={!actor.nodeId}
                >
                  <span className={`station-avatar agent-${agentColorKey(actor.agent)}`} aria-hidden="true">
                    {initial(actor.agent)}
                  </span>
                  <span className="station-actor-text">
                    <span className="station-actor-name">{agentWho(actor.agent, actor.model)}</span>
                    <span className="station-actor-line">
                      <span className="station-pose">{poseLabel(t, actor.pose)}</span>
                      {actor.nodeTitle ? ` · ${actor.nodeTitle}` : ""}
                      {actor.energy ? ` · ${t("station.roster.tokens", { tokens: compact(actor.energy.used) })}` : ""}
                    </span>
                    <span className="station-actor-room">{placeName(actor)}</span>
                  </span>
                </button>
              </li>
            ))}
          </ul>
        ))}
    </section>
  );
}

/** The runners, one line each (online or not); "Manage" is Settings →
 * Runners. Nothing while there is none: the "In progress" card then asks
 * for one instead. */
function StationRunners() {
  const { t } = useI18n();
  const runners = useRunners((s) => s.runners);
  const loaded = useRunners((s) => s.loaded);
  const loading = useRunners((s) => s.loading);
  const error = useRunners((s) => s.error);
  if (!loaded || (loading && runners.length === 0)) return null;
  if (!error && runners.length === 0) return null;
  const shown = runners.slice(0, 4);
  return (
    <section className="station-panel station-runners" aria-labelledby="station-runners-title">
      <div className="station-panel-head">
        <h2 id="station-runners-title">{t("station.runner.title")}</h2>
        <button type="button" className="run-link-button station-head-end" onClick={() => openSettings("runners")}>
          {t("station.runner.manage")}
        </button>
      </div>
      <ul className="station-runner-list">
        {shown.map((runner) => (
          <li key={runner.id}>
            <span className={`station-dot ${runner.online ? "status-running" : "is-off"}`} aria-hidden="true" />
            <span className="station-runner-name">{runner.name}</span>
            <span className="station-panel-meta">
              {runner.online ? t("game.room.online") : t("game.room.offline")}
            </span>
          </li>
        ))}
      </ul>
      {runners.length > shown.length && (
        <p className="station-panel-meta">{t("station.runner.more", { count: runners.length - shown.length })}</p>
      )}
    </section>
  );
}

/** Agent chat, folded to what is unread until opened; open, it is the
 * docked agent panel with every control it has. On the board, this card is
 * its way in whenever the floating button has nothing to show. */
function StationNetwork() {
  const { t } = useI18n();
  const [open, setOpen] = useRememberedOpen("nodevas-station-network-open");
  const count = useAgents((s) => unseenCount(s.messages, s.seenAt, s.viewer));
  const online = useAgents((s) => s.sessions.filter((session) => session.online).length);
  // What opens the floating panel on the board (Ctrl+Shift+A, a toast, a
  // loose session in the war room) unfolds this card in Station. The request
  // is consumed here: left set, the next Ctrl+Shift+A would only clear it,
  // and the floating panel would pop up on the way back to the board.
  const asked = useAgents((s) => s.open);
  useEffect(() => {
    if (!asked) return;
    setOpen(true);
    useAgents.getState().setOpen(false);
  }, [asked, setOpen]);
  return (
    <section className={`station-panel station-network${open ? " is-open" : ""}`}>
      <button
        type="button"
        className="station-network-toggle"
        aria-expanded={open}
        aria-controls={open ? "agent-network-docked" : undefined}
        onClick={() => setOpen(!open)}
      >
        <span className="station-network-name">{t("station.network.title")}</span>
        <span className="station-panel-meta">
          {t("station.network.summary", { count, online })} · {open ? t("station.progress.collapse") : t("station.progress.expand")}
          <span aria-hidden="true"> {open ? "▾" : "›"}</span>
        </span>
      </button>
      {open && <AgentPanel docked />}
    </section>
  );
}
