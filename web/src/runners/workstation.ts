/**
 * Whether the agent workstation is shown.
 *
 * The workstation — runners, agent runs, autopilot, the agent network and
 * usage — is a large part of the interface and none of it does anything on a
 * board used as a local planner. So it is out of sight until there is a reason
 * for it: the person turned it on (Settings → Agents, or the
 * command palette), or this workspace already has a runner, a run, or an agent
 * connected to the network. The runner a single-user hub starts for itself is
 * not such a reason until it has a repo (runnerInUse): it is part of the app,
 * not something the person added. The second kind of reason is written down as the
 * first (`agentWorkstation` = on) the moment it is seen, so a board never loses
 * the workstation because one project happens to have no runs.
 *
 * Hiding is presentation only: the stores and the live updates keep working
 * either way, and the events that open the Agents parts of Settings land on
 * the workstation's switch while it is hidden.
 */

import { useEffect } from "react";

import { api, type Runner } from "../api";
import { useAgents } from "../agents/agentStore";
import { useRuns } from "../runs/runStore";
import { useApp, usePreference } from "../store";
import { isHubRunner } from "./edition";
import { useRunners } from "./runnerStore";

/**
 * Whether `runner` is a reason to show the workstation. A runner someone
 * added is. The one a single-user hub starts for itself is there on every
 * such hub from its first start, whether or not the person ever means to use
 * agents, so it only counts once they gave it a repo to work in.
 */
export function runnerInUse(runner: Runner): boolean {
  return !isHubRunner(runner) || (runner.info?.repos?.length ?? 0) > 0;
}

/** Whether the workstation is in use here, from what the board already holds. */
export function useWorkstationInUse(): boolean {
  const runners = useRunners((s) => s.runners.some(runnerInUse));
  // RunsRoot asks for the project's recent runs once per project.
  const runs = useRuns((s) => Object.keys(s.latestByNode).length > 0);
  // AgentNetworkRoot keeps the network's sessions, asks and held messages.
  const agents = useAgents(
    (s) => s.sessions.length > 0 || s.asks.length > 0 || s.held.length > 0,
  );
  return runners || runs || agents;
}

/** Whether the workstation's controls are drawn. */
export function useWorkstationVisible(): boolean {
  const enabled = usePreference("agentWorkstation");
  const inUse = useWorkstationInUse();
  return enabled || inUse;
}

/**
 * Mounted once, by the main window: asks the hub once whether this person has
 * runners (the runner store itself only loads when its dialog opens), and turns
 * the workstation on for good when anything says it is in use.
 */
export function useWorkstationAutoEnable(): void {
  const enabled = usePreference("agentWorkstation");
  const inUse = useWorkstationInUse();

  useEffect(() => {
    if (enabled) return;
    let cancelled = false;
    // A probe, not a load: the store stays untouched (but for the hub's own
    // runner still on its way up, below), so the Runners dialog still does
    // its own first fetch and a failure here shows nowhere. An error (an
    // older hub, a refused role) is read as "no runners".
    void api
      .listRunners()
      .then((response) => {
        if (cancelled) return;
        const runners = response.runners ?? [];
        if (runners.some(runnerInUse)) {
          useApp.getState().updateUIPreference("agentWorkstation", true);
          return;
        }
        // The hub's own runner has not connected yet. What it was set up
        // with (serve --repo) reaches the hub only with its hello, after
        // this probe, so load the list: runners-changed keeps a loaded list
        // current, and its repos then count above (useWorkstationInUse).
        if (runners.some((runner) => isHubRunner(runner) && !runner.online)) {
          void useRunners.getState().refresh();
        }
      })
      .catch(() => undefined);
    return () => {
      cancelled = true;
    };
  }, [enabled]);

  useEffect(() => {
    if (inUse && !enabled) useApp.getState().updateUIPreference("agentWorkstation", true);
  }, [inUse, enabled]);
}
