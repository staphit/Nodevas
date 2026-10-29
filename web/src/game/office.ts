// The current project's office: a character per run (and per agent session
// with no run in view), each in the part of the office its state puts it
// (areas.ts): at a desk, at the meeting-room table, at the door, or off to
// the shared lounge. What waits on the viewer without an agent at it (a PR
// to merge, a review, a held message) lies on the meeting-room table.

import type { AgentAsk, AgentMessage, AgentSession } from "../api/agents";
import type { Runner } from "../api/runners";
import { runFinal, type PipelineState, type Run, type RunBudgetStatus } from "../api/runs";
import { excerpt } from "../agents/agentStore";
import type { WaitingItem } from "../station/waiting";
import { areaOf } from "./areas";
import { joinSessions } from "./joins";
import { resolveOfficeStyle } from "./officeStyle";
import { roleOfRun, testStateOf } from "./testState";
import { finishedRunStaysInRoom, runPose, sessionPose } from "./pose";
import { bubbleVisible, sessionOnline } from "./presence";
import type { Actor, ClickTarget, Energy, Meeting, MeetingItem, ProjectOffice, SwappedFrom } from "./world";

/** Failure kinds a fallback run takes over from (runs/dispatcher_routing.go):
 * the previous agent or model could not run, not the task itself failed. */
export const FALLBACK_FAILURE_KINDS: ReadonlySet<string> = new Set([
  "rate_limit",
  "quota",
  "model_unavailable",
  "agent_unavailable",
]);

/** How long a finished run that still needs someone (failed, CI pending or
 * red, a merge in flight) keeps its seat. */
export const FINISHED_RUN_SEAT_MS = 24 * 60 * 60 * 1000;

/** How long a run that succeeded keeps its seat, cheering, before it
 * leaves the room. */
export const FINISHED_SHOW_MS = 10 * 60 * 1000;

export interface OfficeInputs {
  /** Runs from every source the view holds (the active list, latest per
   * node, a node's full list), duplicates allowed: the last copy of a run
   * wins, so pass the freshest source last. */
  runs: Run[];
  runners: Runner[];
  sessions: AgentSession[];
  asks: AgentAsk[];
  /** Messages a person has to approve (status "held"). */
  held: AgentMessage[];
  messages: AgentMessage[];
  /** Loaded review pipelines, by id. */
  pipelines?: Record<string, PipelineState | undefined>;
  budget?: RunBudgetStatus | null;
  /** Node id → title. */
  titles: ReadonlyMap<string, string>;
  now: number;
}

export interface CurrentOfficeInputs extends OfficeInputs {
  project: string;
  /** The project's name as the explorer shows it. */
  label: string;
  /** graph.yaml's type. */
  layout: string;
  /** The project's chosen style, if any (graph.officeStyle). */
  officeStyle?: string;
  /** What waits on the viewer (station/useWaitingItems): the table's
   * merges, reviews and held messages come from here. */
  waitingItems: WaitingItem[];
  /** Runs offered to or running on each runner across every project in
   * view (building.ts): what is left of its slots is the free desks. Absent:
   * counted from this project's runs. */
  runnerBusy?: ReadonlyMap<string, number>;
}

export interface CurrentOffice {
  office: ProjectOffice;
  actors: Actor[];
  /** Actor ids that went to the shared lounge. */
  loungeIds: string[];
}

/** How many free desks an office shows next to its occupied ones. */
export const MAX_FREE_DESKS = 3;

/** Runs that hold a runner's slot. */
export function holdsSlot(status: string): boolean {
  return status === "offered" || status === "running";
}

export function runActorId(runId: string): string {
  return `run:${runId}`;
}

export function sessionActorId(sessionId: string): string {
  return `session:${sessionId}`;
}

function time(value: string | undefined): number {
  if (!value) return NaN;
  return Date.parse(value);
}

function sameName(a: string | undefined, b: string | undefined): boolean {
  return Boolean(a) && Boolean(b) && a!.toLowerCase() === b!.toLowerCase();
}

function totalTokens(run: Run): number {
  if (!run.usage) return 0;
  return (run.usage.inputTokens ?? 0) + (run.usage.outputTokens ?? 0);
}

function hasChanges(run: Run): boolean {
  return (
    Boolean(run.commit) ||
    (run.filesChanged ?? 0) > 0 ||
    (run.additions ?? 0) > 0 ||
    (run.deletions ?? 0) > 0
  );
}

/** Only http(s) links leave the page: a PR URL is the runner's word. */
export function safePRUrl(url: string | undefined): string | undefined {
  if (!url) return undefined;
  try {
    const parsed = new URL(url);
    return parsed.protocol === "https:" || parsed.protocol === "http:" ? parsed.href : undefined;
  } catch {
    return undefined;
  }
}

/** Unique runs, the last copy of each winning. */
export function dedupeRuns(runs: Run[]): Run[] {
  const byId = new Map<string, Run>();
  for (const run of runs) {
    if (!run?.id) continue;
    byId.delete(run.id); // keep the order of the last copy
    byId.set(run.id, run);
  }
  return [...byId.values()];
}

/** The runs that get a character: every run that has not ended, a finished
 * one that still needs someone, and one that just succeeded, unless a later
 * run on its node replaced it (a fallback of it, or new work outside its
 * pipeline) or it finished more than FINISHED_RUN_SEAT_MS (a success:
 * FINISHED_SHOW_MS) ago. */
export function seatedRuns(runs: Run[], now: number): Run[] {
  const retried = new Set(runs.map((run) => run.retryOf).filter(Boolean) as string[]);
  return runs.filter((run) => {
    if (!runFinal(run.status)) return true;
    if (retried.has(run.id)) return false;
    const finished = time(run.finishedAt);
    if (finishedRunStaysInRoom(run)) {
      if (Number.isFinite(finished) && now - finished > FINISHED_RUN_SEAT_MS) return false;
    } else if (run.status !== "succeeded" || !Number.isFinite(finished) || now - finished > FINISHED_SHOW_MS) {
      return false;
    }
    const created = time(run.createdAt);
    return !runs.some(
      (other) =>
        other.id !== run.id &&
        other.nodeId === run.nodeId &&
        time(other.createdAt) > created &&
        !(run.pipelineId && other.pipelineId === run.pipelineId),
    );
  });
}

function pendingAskFor(
  asks: AgentAsk[],
  sessionId: string | undefined,
  runId?: string,
): AgentAsk | undefined {
  let newest: AgentAsk | undefined;
  for (const ask of asks) {
    if (ask.status !== "pending") continue;
    const matchesSession = Boolean(sessionId && ask.sessionId === sessionId);
    const matchesRun = Boolean(runId && ask.runId === runId);
    if (!matchesSession && !matchesRun) continue;
    if (!newest || ask.createdAt > newest.createdAt) newest = ask;
  }
  return newest;
}

function bubbleFor(
  messages: AgentMessage[],
  session: AgentSession | undefined,
  now: number,
): AgentMessage | undefined {
  if (!session) return undefined;
  let newest: AgentMessage | undefined;
  for (const message of messages) {
    if (message.status === "held" || message.status === "rejected") continue;
    if (!sameName(message.senderSession, session.name)) continue;
    if (!bubbleVisible(message.createdAt, now)) continue;
    if (!newest || message.createdAt > newest.createdAt) newest = message;
  }
  return newest;
}

function heldFor(held: AgentMessage[], session: AgentSession | undefined): AgentMessage[] {
  if (!session) return [];
  return held
    .filter((message) => message.status === "held" && sameName(message.to, session.name))
    .sort((a, b) => a.createdAt - b.createdAt);
}

function swappedFrom(run: Run, byId: Map<string, Run>): SwappedFrom | undefined {
  if (!run.retryOf) return undefined;
  const previous = byId.get(run.retryOf);
  if (!previous || !FALLBACK_FAILURE_KINDS.has(previous.failureKind ?? "")) return undefined;
  if (previous.agent === run.agent && previous.model === run.model) return undefined;
  return {
    runId: previous.id,
    agent: previous.agent,
    model: previous.model,
    failureKind: previous.failureKind ?? "",
  };
}

function runnerOnline(runner: Runner | undefined): boolean | null {
  return runner ? Boolean(runner.online) : null;
}

/** Free slots on the online runners, at most MAX_FREE_DESKS. */
function freeDesks(runners: Runner[], busy: ReadonlyMap<string, number>): number {
  let free = 0;
  for (const runner of runners) {
    if (!runner.online) continue;
    const slots = Math.max(1, runner.info?.maxConcurrent ?? 1);
    free += Math.max(0, slots - (busy.get(runner.id) ?? 0));
  }
  return Math.max(0, Math.min(MAX_FREE_DESKS, free));
}

/** What waits on the viewer and has no agent at the table for it. */
function meetingItems(
  items: WaitingItem[],
  titles: ReadonlyMap<string, string>,
  attachedHeld: ReadonlySet<string>,
): MeetingItem[] {
  const out: MeetingItem[] = [];
  for (const item of items) {
    if (item.kind === "merge") {
      const target: ClickTarget = { kind: "run", runId: item.run.id, nodeId: item.run.nodeId };
      const actions: ClickTarget[] = [target, { kind: "node", nodeId: item.run.nodeId }];
      const url = safePRUrl(item.run.prUrl);
      if (url) actions.push({ kind: "pr", runId: item.run.id, url });
      out.push({
        id: `merge:${item.run.id}`,
        kind: "merge",
        nodeId: item.run.nodeId,
        title: titles.get(item.run.nodeId) || (item.run.prNumber ? `#${item.run.prNumber}` : item.run.nodeId),
        target,
        actions,
      });
    } else if (item.kind === "review") {
      const nodeId = item.task.task.nodeId;
      const target: ClickTarget = { kind: "autopilot", goalNodeId: item.task.goalNodeId };
      out.push({
        id: `review:${item.task.autopilotId}:${nodeId}`,
        kind: "review",
        nodeId,
        title: item.task.task.title || titles.get(nodeId) || nodeId,
        target,
        actions: [target, { kind: "node", nodeId }],
      });
    } else if (item.kind === "held" && !attachedHeld.has(item.message.id)) {
      const target: ClickTarget = { kind: "held-message", messageId: item.message.id };
      out.push({
        id: `held:${item.message.id}`,
        kind: "held",
        title: excerpt(item.message.body, 60),
        target,
        actions: [target],
      });
    }
  }
  return out;
}

export function buildCurrentOffice(inputs: CurrentOfficeInputs): CurrentOffice {
  const { now, titles, project } = inputs;
  const runs = dedupeRuns(inputs.runs);
  const byId = new Map(runs.map((run) => [run.id, run]));
  const seated = seatedRuns(runs, now).sort(
    (a, b) => (time(a.createdAt) || 0) - (time(b.createdAt) || 0) || a.id.localeCompare(b.id),
  );
  const join = joinSessions(seated, inputs.sessions);
  const cap = inputs.budget?.budget.dailyTokens;
  const capOf = (used: number): Energy => (cap && cap > 0 ? { used, cap } : { used });
  const runners = new Map(inputs.runners.map((runner) => [runner.id, runner]));
  const byPipeline = new Map<string, Run[]>();
  for (const run of runs) {
    if (!run.pipelineId) continue;
    const list = byPipeline.get(run.pipelineId);
    if (list) list.push(run);
    else byPipeline.set(run.pipelineId, [run]);
  }

  const actors: Actor[] = [];
  for (const run of seated) {
    const role = roleOfRun(run);
    const runner = runners.get(run.runnerId);
    // A runner the caller cannot list (an administrator looking at someone
    // else's): its state is unknown, so it never reads offline.
    const online = runnerOnline(runner);
    const session = join.byRun.get(run.id);
    const ask = pendingAskFor(inputs.asks, session?.id, run.id);
    const heldMail = heldFor(inputs.held, session);
    const target: ClickTarget = { kind: "run", runId: run.id, nodeId: run.nodeId };
    const actions: ClickTarget[] = [target, { kind: "node", nodeId: run.nodeId }];
    if (hasChanges(run)) actions.push({ kind: "run-diff", runId: run.id, nodeId: run.nodeId });
    const prUrl = safePRUrl(run.prUrl);
    if (prUrl) actions.push({ kind: "pr", runId: run.id, url: prUrl });
    if (ask) actions.push({ kind: "ask", askId: ask.id, sessionId: ask.sessionId });
    for (const message of heldMail) actions.push({ kind: "held-message", messageId: message.id });
    const pose = runPose(run, { runnerOnline: online, asking: Boolean(ask) });
    actors.push({
      id: runActorId(run.id),
      kind: "run",
      agent: run.agent,
      model: run.model,
      project,
      area: areaOf({ kind: "run", pose, nodeId: run.nodeId }, run),
      runnerId: run.runnerId || undefined,
      runnerName: runner?.name || run.runnerName || undefined,
      runnerOnline: online,
      pose,
      nodeId: run.nodeId,
      nodeTitle: titles.get(run.nodeId) ?? "",
      runId: run.id,
      sessionId: session?.id,
      sessionName: session?.name,
      ownerName: run.createdByName,
      ask,
      bubble: bubbleFor(inputs.messages, session, now),
      heldMail,
      swappedFrom: swappedFrom(run, byId),
      energy: run.usage ? capOf(totalTokens(run)) : undefined,
      pipelineId: run.pipelineId || undefined,
      role,
      testState: run.pipelineId ? testStateOf(role, byPipeline.get(run.pipelineId) ?? []) : undefined,
      target,
      actions,
    });
  }

  const loose = [...join.unjoined].sort((a, b) => a.name.localeCompare(b.name) || a.id.localeCompare(b.id));
  for (const session of loose) {
    // A run's agent whose run is out of view and that has gone: the run
    // ended, and its agent with it.
    if ((session.run || session.runId) && !sessionOnline(session, now)) continue;
    const ask = pendingAskFor(inputs.asks, session.id);
    const heldMail = heldFor(inputs.held, session);
    const nodeId = session.nodeId || undefined;
    const target: ClickTarget = nodeId
      ? { kind: "node", nodeId }
      : { kind: "session", sessionId: session.id };
    const actions: ClickTarget[] = [target];
    if (nodeId) actions.push({ kind: "session-diff", sessionId: session.id, nodeId });
    if (ask) actions.push({ kind: "ask", askId: ask.id, sessionId: ask.sessionId });
    for (const message of heldMail) actions.push({ kind: "held-message", messageId: message.id });
    const pose = sessionPose(session, { now, asking: Boolean(ask) });
    actors.push({
      id: sessionActorId(session.id),
      kind: "session",
      agent: session.agent,
      model: session.model ?? "",
      project,
      area: areaOf({ kind: "session", pose, nodeId }),
      runnerOnline: null,
      pose,
      nodeId,
      nodeTitle: nodeId ? (titles.get(nodeId) ?? "") : "",
      runId: session.runId || undefined,
      sessionId: session.id,
      sessionName: session.name,
      ownerName: session.actorName,
      ask,
      bubble: bubbleFor(inputs.messages, session, now),
      heldMail,
      target,
      actions,
    });
  }

  let busy = inputs.runnerBusy;
  if (!busy) {
    const counted = new Map<string, number>();
    for (const run of runs) if (holdsSlot(run.status)) counted.set(run.runnerId, (counted.get(run.runnerId) ?? 0) + 1);
    busy = counted;
  }
  const attachedHeld = new Set(actors.flatMap((actor) => actor.heldMail.map((message) => message.id)));
  const ids = (area: Actor["area"]) => actors.filter((actor) => actor.area === area).map((actor) => actor.id);

  return {
    office: {
      project,
      label: inputs.label || project,
      layout: inputs.layout,
      style: resolveOfficeStyle({ officeStyle: inputs.officeStyle, layout: inputs.layout }),
      styleSet: Boolean(inputs.officeStyle),
      work: ids("work"),
      freeDesks: freeDesks(inputs.runners, busy),
      meeting: ids("meeting"),
      items: meetingItems(inputs.waitingItems, titles, attachedHeld),
      entrance: ids("entrance"),
      meetings: buildMeetings(actors, byId, inputs.pipelines ?? {}, titles),
    },
    actors,
    loungeIds: ids("lounge"),
  };
}

/** One table per review pipeline with a reviewer in the office. */
function buildMeetings(
  actors: Actor[],
  runs: Map<string, Run>,
  pipelines: Record<string, PipelineState | undefined>,
  titles: ReadonlyMap<string, string>,
): Meeting[] {
  const meetings = new Map<string, Meeting>();
  for (const actor of actors) {
    const run = actor.runId ? runs.get(actor.runId) : undefined;
    if (!run || run.kind !== "review" || !run.pipelineId || actor.kind !== "run") continue;
    let meeting = meetings.get(run.pipelineId);
    if (!meeting) {
      const pipeline = pipelines[run.pipelineId];
      const round = pipeline?.round ?? 0;
      meeting = {
        pipelineId: run.pipelineId,
        nodeId: run.nodeId,
        nodeTitle: titles.get(run.nodeId) ?? "",
        project: actor.project,
        round,
        status: pipeline?.status ?? "",
        waitingHuman: pipeline?.status === "waiting_human",
        actorIds: [],
        votes: (pipeline?.votes ?? [])
          .filter((vote) => !round || vote.round === round)
          .map((vote) => ({
            agent: vote.agent,
            model: vote.model,
            verdict: vote.verdict,
            runId: vote.runId,
          })),
        target: { kind: "node", nodeId: run.nodeId },
      };
      meetings.set(run.pipelineId, meeting);
    }
    meeting.actorIds.push(actor.id);
  }
  return [...meetings.values()];
}
