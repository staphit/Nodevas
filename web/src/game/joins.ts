// Which agent session is which run's agent. A hub and runner that know the
// run say so (AgentSession.runId, set from the runner's proxy). Older ones do
// not, and then the session name is the only link: a runner names a run's
// session `<repo>-<agent>-<model>-<first 6 of the run id>`
// (internal/runner/jobs.go mcpArgs), which the hub lowercases and may suffix
// with "-2", "-3"… when the name is taken (internal/agents/registry.go
// uniqueNameLocked).

import type { AgentSession } from "../api/agents";
import type { Run } from "../api/runs";

/** internal/runner/worktree.go slug: lowercase letters and digits, runs of
 * anything else as one dash, no dash at either end. */
export function slug(value: string): string {
  let out = "";
  let dash = false;
  for (const ch of value.toLowerCase()) {
    if ((ch >= "a" && ch <= "z") || (ch >= "0" && ch <= "9")) {
      out += ch;
      dash = false;
    } else if (!dash && out.length > 0) {
      out += "-";
      dash = true;
    }
  }
  return out.replace(/^-+|-+$/g, "");
}

/** The run's tag in its session's name. */
export function runNameTag(runId: string): string {
  return slug(runId).slice(0, 6);
}

/** The session name a runner gives a run's agent before the hub makes it
 * unique: `<repo>-<agent>-<model>-<tag>`, model "default" when none. */
export function runSessionName(run: Pick<Run, "id" | "repoName" | "agent" | "model">): string {
  const repo = slug(run.repoName) || "repo";
  return `${repo}-${run.agent}-${slug(run.model || "default")}-${runNameTag(run.id)}`;
}

/** Whether a session name carries the run's tag where a runner puts it: the
 * last dash-separated part, or the one before a uniqueness suffix. */
export function nameHasRunTag(name: string, runId: string): boolean {
  const tag = runNameTag(runId);
  if (tag.length < 6) return false;
  const parts = name.toLowerCase().split("-");
  const last = parts[parts.length - 1];
  if (last === tag) return true;
  return parts.length >= 2 && /^\d+$/.test(last) && parts[parts.length - 2] === tag;
}

/** Whether the session is the run's agent. With a runId on the session it is
 * decided by that alone; without, by the name tag. The agent kind is not
 * compared: what a session reports as its agent depends on the MCP client's
 * detection, not on the run. */
export function sessionIsRuns(session: AgentSession, run: Pick<Run, "id">): boolean {
  if (session.runId) return session.runId === run.id;
  if (session.run === false) return false;
  return nameHasRunTag(session.name, run.id);
}

export interface SessionJoin {
  /** Run id → its agent's session. */
  byRun: Map<string, AgentSession>;
  /** Session id → the run it is the agent of. */
  runOf: Map<string, string>;
  /** Sessions no run in view claims, in the order given. */
  unjoined: AgentSession[];
}

/** Pairs runs with their agents' sessions. A run claims at most one session
 * (the most recently seen, if several match) and a session at most one run;
 * an exact runId match always wins over a name match. */
export function joinSessions(runs: Run[], sessions: AgentSession[]): SessionJoin {
  const byRun = new Map<string, AgentSession>();
  const runOf = new Map<string, string>();
  const runIds = new Set(runs.map((run) => run.id));

  // Exact first.
  for (const session of sessions) {
    if (!session.runId || !runIds.has(session.runId)) continue;
    const held = byRun.get(session.runId);
    if (!held || (session.lastSeen ?? 0) > (held.lastSeen ?? 0)) {
      if (held) runOf.delete(held.id);
      byRun.set(session.runId, session);
      runOf.set(session.id, session.runId);
    }
  }
  // Then by name, for runs and sessions still free.
  for (const run of runs) {
    if (byRun.has(run.id)) continue;
    let best: AgentSession | undefined;
    for (const session of sessions) {
      if (runOf.has(session.id) || session.runId) continue;
      if (!sessionIsRuns(session, run)) continue;
      if (!best || (session.lastSeen ?? 0) > (best.lastSeen ?? 0)) best = session;
    }
    if (best) {
      byRun.set(run.id, best);
      runOf.set(best.id, run.id);
    }
  }
  return {
    byRun,
    runOf,
    unjoined: sessions.filter((session) => !runOf.has(session.id)),
  };
}
