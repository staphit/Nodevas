// Station's "who worked on what": one lane per agent over the last hour,
// with its runs as bars and what it said or hit as marks. Plain data from
// plain inputs, like the game world (src/game/world.ts), so it is tested
// without React and a component only decides how it looks.

import type { AgentAsk, AgentMessage, Run } from "../api";
import { askQuestionText } from "../agents/AskCard";
import { isAutoAllowedAsk } from "../agents/askList";
import { agentWho } from "../game/labels";

export const ACTIVITY_WINDOW_MS = 60 * 60 * 1000;
/** Lanes shown at most; the quietest are left out. */
export const MAX_LANES = 8;

export interface ActivityBar {
  runId: string;
  nodeId: string;
  /** The node's title, or its id when the graph does not have it. */
  label: string;
  kind: "run" | "review";
  status: string;
  /** Position in the window, 0 (window start) to 1 (now). */
  start: number;
  end: number;
  /** Still running at the window's end. */
  open: boolean;
}

export interface ActivityMark {
  kind: "ask" | "message" | "limit";
  at: number;
  /** One line: the question, the message or the reason. */
  text: string;
  nodeId?: string;
  runId?: string;
}

export interface ActivityLane {
  /** The agent (and model, for a run) the lane is about. */
  key: string;
  label: string;
  bars: ActivityBar[];
  marks: ActivityMark[];
  /** The newest moment anything happened in it, for ordering. */
  latest: number;
}

export interface ActivityInput {
  runs: Run[];
  asks: AgentAsk[];
  messages: AgentMessage[];
  now: number;
  titleOf: (nodeId: string) => string;
  /** Translates, for an ask's text as the viewer reads it. */
  t: (key: string, values?: Record<string, string | number>) => string;
  windowMs?: number;
}

function millis(value: number | string | undefined): number {
  if (value === undefined || value === "") return NaN;
  if (typeof value === "number") return value < 1e12 ? value * 1000 : value;
  return Date.parse(value);
}

const LIMIT_KINDS = new Set(["rate_limit", "quota"]);

/** The lanes for the window ending at now, busiest-most-recently first. */
export function buildActivity({
  runs,
  asks,
  messages,
  now,
  titleOf,
  t,
  windowMs = ACTIVITY_WINDOW_MS,
}: ActivityInput): ActivityLane[] {
  const from = now - windowMs;
  const at = (ms: number) => Math.min(1, Math.max(0, (ms - from) / windowMs));
  const lanes = new Map<string, ActivityLane>();
  const lane = (key: string, label: string): ActivityLane => {
    let found = lanes.get(key);
    if (!found) {
      found = { key, label, bars: [], marks: [], latest: 0 };
      lanes.set(key, found);
    }
    return found;
  };

  for (const run of runs) {
    const started = millis(run.startedAt) || millis(run.createdAt);
    if (!Number.isFinite(started)) continue;
    const finished = millis(run.finishedAt);
    const ended = Number.isFinite(finished) ? finished : now;
    if (ended < from || started > now) continue;
    const key = run.agent || "agent";
    const target = lane(key, key);
    target.bars.push({
      runId: run.id,
      nodeId: run.nodeId,
      label: titleOf(run.nodeId) || run.nodeId,
      kind: run.kind === "review" ? "review" : "run",
      status: run.status,
      start: at(started),
      end: at(ended),
      open: !Number.isFinite(finished),
    });
    target.latest = Math.max(target.latest, ended);
    if (run.status === "failed" && LIMIT_KINDS.has(run.failureKind ?? "") && Number.isFinite(finished)) {
      target.marks.push({
        kind: "limit",
        at: at(finished),
        text: `${agentWho(run.agent, run.model)}: ${run.error || run.failureKind}`,
        nodeId: run.nodeId,
        runId: run.id,
      });
    }
  }

  for (const ask of asks) {
    // The hub allowed it on an earlier "allow for this run": nothing new.
    if (isAutoAllowedAsk(ask)) continue;
    const created = millis(ask.createdAt);
    if (!Number.isFinite(created) || created < from || created > now) continue;
    const key = ask.agent || ask.sessionName || "agent";
    const target = lane(key, key);
    target.marks.push({ kind: "ask", at: at(created), text: askQuestionText(t, ask), nodeId: ask.nodeId });
    target.latest = Math.max(target.latest, created);
  }

  for (const message of messages) {
    // A person's own messages are not an agent's activity.
    if (!message.senderAgent || message.senderAgent === "human") continue;
    const created = millis(message.createdAt);
    if (!Number.isFinite(created) || created < from || created > now) continue;
    const key = message.senderAgent;
    const target = lane(key, key);
    target.marks.push({ kind: "message", at: at(created), text: `→ ${message.to}: ${message.body}`, nodeId: message.nodeId });
    target.latest = Math.max(target.latest, created);
  }

  for (const found of lanes.values()) {
    found.bars.sort((a, b) => a.start - b.start);
    found.marks.sort((a, b) => a.at - b.at);
  }
  return [...lanes.values()].sort((a, b) => b.latest - a.latest).slice(0, MAX_LANES);
}
