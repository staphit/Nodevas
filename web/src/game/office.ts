// The war room: a room per runner, a character per run (and per agent
// session with no run in view), meeting tables for review pipelines.

import type { AgentAsk, AgentMessage, AgentSession } from "../api/agents";
import type { Runner } from "../api/runners";
import { runFinal, type PipelineState, type Run, type RunBudgetStatus } from "../api/runs";
import { joinSessions } from "./joins";
import { finishedRunStaysInRoom, runPose, sessionPose } from "./pose";
import { bubbleVisible, sessionOnline } from "./presence";
import {
  TERMINAL_ROOM_ID,
  type Actor,
  type ClickTarget,
  type Energy,
  type Meeting,
  type Office,
  type Room,
  type SwappedFrom,
} from "./world";

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

export function runnerRoomId(runnerId: string): string {
  return `runner:${runnerId}`;
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
function safePRUrl(url: string | undefined): string | undefined {
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

/** The runs that get a character: every run that has not ended, and a
 * finished one that still needs someone, unless a later run on its node
 * replaced it (a fallback of it, or new work outside its pipeline) or it
 * finished more than FINISHED_RUN_SEAT_MS ago. */
export function seatedRuns(runs: Run[], now: number): Run[] {
  const retried = new Set(runs.map((run) => run.retryOf).filter(Boolean) as string[]);
  return runs.filter((run) => {
    if (!runFinal(run.status)) return true;
    if (!finishedRunStaysInRoom(run) || retried.has(run.id)) return false;
    const finished = time(run.finishedAt);
    if (Number.isFinite(finished) && now - finished > FINISHED_RUN_SEAT_MS) return false;
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

function pendingAskFor(asks: AgentAsk[], sessionId: string | undefined): AgentAsk | undefined {
  if (!sessionId) return undefined;
  let newest: AgentAsk | undefined;
  for (const ask of asks) {
    if (ask.status !== "pending" || ask.sessionId !== sessionId) continue;
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

function roomFor(runner: Runner): Room {
  return {
    id: runnerRoomId(runner.id),
    kind: "runner",
    name: runner.name,
    runnerId: runner.id,
    ownerName: runner.ownerName,
    hostname: runner.info?.hostname,
    online: Boolean(runner.online),
    agentKinds: (runner.info?.agents ?? [])
      .filter((agent) => agent.installed)
      .map((agent) => agent.kind),
    actorIds: [],
  };
}

export function buildOffice(inputs: OfficeInputs): Office {
  const { now, titles } = inputs;
  const runs = dedupeRuns(inputs.runs);
  const byId = new Map(runs.map((run) => [run.id, run]));
  const seated = seatedRuns(runs, now).sort(
    (a, b) => (time(a.createdAt) || 0) - (time(b.createdAt) || 0) || a.id.localeCompare(b.id),
  );
  const join = joinSessions(seated, inputs.sessions);
  const cap = inputs.budget?.budget.dailyTokens;
  const capOf = (used: number): Energy => (cap && cap > 0 ? { used, cap } : { used });

  const rooms = new Map<string, Room>();
  for (const runner of inputs.runners) rooms.set(runnerRoomId(runner.id), roomFor(runner));
  const terminal: Room = {
    id: TERMINAL_ROOM_ID,
    kind: "terminal",
    name: "",
    online: null,
    agentKinds: [],
    actorIds: [],
  };

  const actors: Actor[] = [];
  for (const run of seated) {
    const roomId = runnerRoomId(run.runnerId);
    let room = rooms.get(roomId);
    if (!room) {
      // A runner the caller cannot list: an administrator looking at
      // someone else's. Its state is unknown, so it never reads offline.
      room = {
        id: roomId,
        kind: "runner",
        name: run.runnerName,
        runnerId: run.runnerId,
        online: null,
        agentKinds: [],
        actorIds: [],
      };
      rooms.set(roomId, room);
    }
    const session = join.byRun.get(run.id);
    const ask = pendingAskFor(inputs.asks, session?.id);
    const heldMail = heldFor(inputs.held, session);
    const target: ClickTarget = { kind: "run", runId: run.id, nodeId: run.nodeId };
    const actions: ClickTarget[] = [target, { kind: "node", nodeId: run.nodeId }];
    if (hasChanges(run)) actions.push({ kind: "run-diff", runId: run.id, nodeId: run.nodeId });
    const prUrl = safePRUrl(run.prUrl);
    if (prUrl) actions.push({ kind: "pr", runId: run.id, url: prUrl });
    if (ask) actions.push({ kind: "ask", askId: ask.id, sessionId: ask.sessionId });
    for (const message of heldMail) actions.push({ kind: "held-message", messageId: message.id });
    const actor: Actor = {
      id: runActorId(run.id),
      kind: "run",
      agent: run.agent,
      model: run.model,
      roomId,
      pose: runPose(run, { runnerOnline: room.online, asking: Boolean(ask) }),
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
      target,
      actions,
    };
    actors.push(actor);
    room.actorIds.push(actor.id);
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
    const actor: Actor = {
      id: sessionActorId(session.id),
      kind: "session",
      agent: session.agent,
      model: session.model ?? "",
      roomId: TERMINAL_ROOM_ID,
      pose: sessionPose(session, { now, asking: Boolean(ask) }),
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
    };
    actors.push(actor);
    terminal.actorIds.push(actor.id);
  }

  return {
    rooms: [...rooms.values(), terminal],
    actors,
    meetings: buildMeetings(actors, byId, inputs.pipelines ?? {}, titles),
    energy: inputs.budget ? capOf(inputs.budget.used?.tokens ?? 0) : undefined,
  };
}

/** One table per review pipeline with a reviewer in the room. */
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
        roomId: actor.roomId,
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
