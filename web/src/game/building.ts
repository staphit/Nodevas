// The workspace as a building. The current project's office comes from the
// local stores in full (office.ts); every other project the viewer may read
// comes from one summary request (GET /api/station/building) and is drawn as
// a glance through its door: who is at a desk, how much waits on the viewer
// there. Idle projects and the overflow past MAX_COMPACT are door signs only,
// so a workspace of many projects stays one screen. Agents with nothing to do
// share a lounge; the runners stand as racks in a server room. The viewer's
// global agents (no project, no node) have an office of their own under the
// current one, built the same way from Station's global store.

import type { BuildingProject, BuildingResponse, BuildingRun } from "../api/station";
import type { Runner } from "../api/runners";
import { runFinal, runInteractive, runWaitsForLimit, type Run } from "../api/runs";
import type { WaitingItem } from "../station/waiting";
import { waitingOnYou } from "../station/waiting";
import type { WorldInputs } from "./buildWorld";
import { nodeTitles } from "./buildWorld";
import { buildCurrentOffice, dedupeRuns, holdsSlot, seatedRuns } from "./office";
import { resolveOfficeStyle } from "./officeStyle";
import { sessionOnline } from "./presence";
import {
  GLOBAL_OFFICE,
  type Actor,
  type Building,
  type ClickTarget,
  type CompactOffice,
  type Energy,
  type LoungeSpot,
  type MiniDesk,
  type Pose,
  type ProjectOffice,
  type Rack,
} from "./world";

/** Other busy offices drawn as compact offices; the rest are door signs. */
export const MAX_COMPACT = 8;
/** Seats in the lounge before "+N". */
export const MAX_LOUNGE = 10;
/** Desks a compact office draws. */
export const MINI_DESKS = 3;
/** A compact office's menu: its runs, then what waits on the viewer there. */
const MAX_RUN_ACTIONS = 5;
const MAX_WAITING_ACTIONS = 3;

export type BuildingInputs = WorldInputs & {
  activeProject: string;
  /** The current project's name as the explorer shows it. */
  projectLabel: string;
  /** The hub's summary of every project, or null before it loads (or on a
   * hub without it): then only the current office is drawn. */
  remote: BuildingResponse | null;
  /** What waits on the viewer in the current project. */
  waitingItems: WaitingItem[];
  /** The viewer's global agents' runs (no project, no node); none or absent:
   * no global office. Duplicates allowed, the last copy wins. */
  globalRuns?: Run[];
};

const inProject = (project: string, then: ClickTarget): ClickTarget => ({ kind: "in-project", project, then });

function sleeping(run: BuildingRun): boolean {
  return runWaitsForLimit(run);
}

function offered(run: BuildingRun): boolean {
  return (run.status === "offered" || run.status === "queued") && !sleeping(run);
}

function runPoseOf(run: BuildingRun, asking: boolean): Pose {
  if (asking) return "asking-user";
  if (run.status === "running") return run.kind === "review" ? "reviewing" : "working";
  return "awaiting-accept";
}

/** Another project's office, drawn small: who is at a desk and what waits on
 * the viewer there. The hub already left out asks and held mail the viewer
 * may not act on, so its counts are taken as they are; runs are counted by
 * the same rules as the current project's "Waiting on you". */
export function compactOffice(p: BuildingProject, now: number): CompactOffice {
  const active = p.runs.filter((run) => !runFinal(run.status));
  const askingRuns = new Set(
    p.sessions
      .filter((session) => session.runId && p.asks.some((ask) => ask.sessionId === session.id || ask.runId === session.runId))
      .map((session) => session.runId),
  );
  for (const ask of p.asks) {
    if (ask.runId) askingRuns.add(ask.runId);
  }
  const running = active.filter((run) => run.status === "running" && run.kind !== "plan");
  const planning = active.filter((run) => run.kind === "plan" && !sleeping(run));
  const waitingAtDoor = active.filter((run) => offered(run) && run.kind !== "plan");
  // A person's own agent at work in the project, with no run behind it.
  const runIds = new Set(p.runs.map((run) => run.id));
  const busySessions = p.sessions.filter(
    (session) => session.online && session.state === "busy" && !(session.runId && runIds.has(session.runId)),
  );

  const desks: MiniDesk[] = [
    ...running.map((run) => ({ agent: run.agent, pose: runPoseOf(run, askingRuns.has(run.id)), title: run.title })),
    ...busySessions.map((session) => ({
      agent: session.agent,
      pose: (p.asks.some((ask) => ask.sessionId === session.id) ? "asking-user" : "working") as Pose,
      title: session.doing || session.name,
    })),
    ...planning.map((run) => ({ agent: run.agent, pose: runPoseOf(run, askingRuns.has(run.id)), title: run.title })),
    ...waitingAtDoor.map((run) => ({ agent: run.agent, pose: "awaiting-accept" as Pose, title: run.title })),
  ];

  const runWaiting = waitingOnYou<BuildingRun>({
    runs: p.runs,
    active,
    asks: [],
    held: [],
    reviews: [],
    canAnswer: () => false,
    now,
  }).length;
  const counts = {
    running: running.length + busySessions.length + planning.length,
    waiting: runWaiting + p.waiting.total,
    offered: waitingAtDoor.length,
    sleeping: active.filter(sleeping).length,
    othersActive: p.othersActive,
  };

  const target: ClickTarget = { kind: "project", project: p.name };
  const actions: ClickTarget[] = [target];
  const byStatus = [...active].sort((a, b) => Number(b.status === "running") - Number(a.status === "running"));
  // Each run: open it, then open its workspace.
  for (const run of byStatus.slice(0, MAX_RUN_ACTIONS)) {
    actions.push(inProject(p.name, { kind: "run", runId: run.id, nodeId: run.nodeId }));
    actions.push(inProject(p.name, { kind: "run-workspace", runId: run.id, nodeId: run.nodeId }));
  }
  const waiting: ClickTarget[] = [
    ...p.asks.map((ask): ClickTarget => inProject(p.name, { kind: "ask", askId: ask.id, sessionId: ask.sessionId, askKind: ask.kind })),
    ...p.reviews.map((review): ClickTarget => inProject(p.name, { kind: "autopilot", goalNodeId: review.goalNodeId })),
  ];
  // Several tasks of one autopilot wait in the same goal: one entry for it.
  const seen = new Set<string>();
  for (const item of waiting) {
    const key = JSON.stringify(item);
    if (seen.has(key)) continue;
    seen.add(key);
    if (seen.size > MAX_WAITING_ACTIONS) break;
    actions.push(item);
  }

  return {
    project: p.name,
    label: p.label || p.name,
    style: resolveOfficeStyle({ officeStyle: p.officeStyle, layout: p.layout }),
    desks: desks.slice(0, MINI_DESKS),
    more: Math.max(0, desks.length - MINI_DESKS),
    counts,
    busy: counts.running + counts.offered + counts.sleeping + counts.othersActive + counts.waiting > 0,
    target,
    actions,
  };
}

/** Busiest first: most at work, then most waiting on the viewer, then by
 * name. */
export function busyOrder(a: CompactOffice, b: CompactOffice): number {
  return (
    b.counts.running - a.counts.running ||
    b.counts.waiting - a.counts.waiting ||
    a.label.localeCompare(b.label) ||
    a.project.localeCompare(b.project)
  );
}

interface RackRun {
  id: string;
  runnerId: string;
  runnerName?: string;
  status: string;
}

/** A rack per runner, its lights on while it is online; busy counts the runs
 * offered to or running on it in every project in view. A runner the caller
 * cannot list (an administrator's view of someone else's) still gets a rack
 * while it holds a run, its state unknown. */
export function racksFor(runners: Runner[], runs: RackRun[]): Rack[] {
  const unique = new Map<string, RackRun>();
  for (const run of runs) if (run?.id) unique.set(run.id, run);
  const busy = new Map<string, number>();
  const unknown = new Map<string, string>();
  for (const run of unique.values()) {
    if (!run.runnerId || !holdsSlot(run.status)) continue;
    busy.set(run.runnerId, (busy.get(run.runnerId) ?? 0) + 1);
    if (!unknown.has(run.runnerId)) unknown.set(run.runnerId, run.runnerName ?? "");
  }
  const racks: Rack[] = runners.map((runner) => ({
    runnerId: runner.id,
    name: runner.name,
    ownerName: runner.ownerName,
    hostname: runner.info?.hostname,
    online: Boolean(runner.online),
    slots: runner.info?.maxConcurrent || undefined,
    busy: busy.get(runner.id) ?? 0,
    agentKinds: (runner.info?.agents ?? []).filter((agent) => agent.installed).map((agent) => agent.kind),
    target: { kind: "runners" },
  }));
  const known = new Set(runners.map((runner) => runner.id));
  for (const [runnerId, name] of unknown) {
    if (known.has(runnerId)) continue;
    racks.push({
      runnerId,
      name,
      online: null,
      busy: busy.get(runnerId) ?? 0,
      agentKinds: [],
      target: { kind: "runners" },
    });
  }
  return racks;
}

/** Other projects' agents with nothing to do: connected sessions idling with
 * no node, and runs asleep until their agent's usage limit resets. The
 * current project's own come from its office instead. */
export function loungeVisitors(remote: BuildingResponse | null, activeProject: string, now: number): LoungeSpot[] {
  const spots: LoungeSpot[] = [];
  for (const p of remote?.projects ?? []) {
    if (p.name === activeProject) continue;
    const label = p.label || p.name;
    const go: ClickTarget = { kind: "project", project: p.name };
    for (const session of p.sessions) {
      if (session.nodeId || session.state === "busy") continue;
      if (!sessionOnline(session, now)) continue;
      const target = inProject(p.name, { kind: "session", sessionId: session.id });
      spots.push({
        id: `visitor:${p.name}:session:${session.id}`,
        visitor: {
          project: p.name,
          label,
          agent: session.agent,
          model: session.model,
          pose: "idle",
          title: session.name,
          target,
          actions: [target, go],
        },
      });
    }
    for (const run of p.runs) {
      if (!sleeping(run)) continue;
      const target = inProject(p.name, { kind: "run", runId: run.id, nodeId: run.nodeId });
      spots.push({
        id: `visitor:${p.name}:run:${run.id}`,
        visitor: {
          project: p.name,
          label,
          agent: run.agent,
          model: run.model,
          pose: "limit",
          title: run.title,
          target,
          actions: [target, go],
        },
      });
    }
  }
  return spots;
}

/** Extra run metadata carried onto an actor for the story room's group view,
 * contest pairs and overlap badge. */
function runActorExtras(run: Run | undefined): Partial<Actor> {
  if (!run) return {};
  const extras: Partial<Actor> = {};
  if (run.repoName) extras.repoName = run.repoName;
  if (run.groupId) extras.groupId = run.groupId;
  if (run.contestId) extras.contestId = run.contestId;
  if (run.contestLabel) extras.contestLabel = run.contestLabel;
  if (run.overlapCount) extras.overlapCount = run.overlapCount;
  return extras;
}

/** A global agent's character: a click opens its workspace; its menu adds
 * Station's Global agents tab, its diff and its PR. Never a node, the run
 * view in a node's drawer or another project: it has none of them. */
export function globalActor(actor: Actor, run: Run | undefined): Actor {
  const runId = actor.runId ?? "";
  const target: ClickTarget = { kind: "run-workspace", runId, nodeId: "" };
  const actions: ClickTarget[] = [target, { kind: "global-agents", runId }];
  for (const action of actor.actions) {
    if (action.kind === "run-diff" || action.kind === "pr") actions.push(action);
  }
  return {
    ...actor,
    ...runActorExtras(run),
    global: true,
    // Headless and still going: no board, so nobody it could ask.
    cannotAsk: Boolean(run && !runInteractive(run) && !runFinal(run.status)),
    target,
    actions,
  };
}

/** The global agents' office: their runs seated as in a project's office,
 * with no node titles, asks, mail or meeting table. null while none of them
 * has a seat. */
export function globalOffice(
  inputs: BuildingInputs,
  runnerBusy: ReadonlyMap<string, number>,
): { office: ProjectOffice; actors: Actor[]; loungeIds: string[] } | null {
  const runs = dedupeRuns(inputs.globalRuns ?? []);
  if (seatedRuns(runs, inputs.now).length === 0) return null;
  const built = buildCurrentOffice({
    project: GLOBAL_OFFICE,
    label: "",
    layout: "",
    runs,
    runners: inputs.runners,
    sessions: [],
    asks: [],
    held: [],
    messages: [],
    pipelines: {},
    budget: inputs.budget,
    titles: new Map(),
    now: inputs.now,
    waitingItems: [],
    runnerBusy,
  });
  const byId = new Map(runs.map((run) => [run.id, run]));
  return {
    // The runners' free slots are the project office's to show: once is enough.
    office: { ...built.office, freeDesks: 0 },
    actors: built.actors.map((actor) => globalActor(actor, actor.runId ? byId.get(actor.runId) : undefined)),
    loungeIds: built.loungeIds,
  };
}

export function buildBuilding(inputs: BuildingInputs): Building {
  const { now, remote, activeProject } = inputs;
  const others = (remote?.projects ?? []).filter((p) => p.name !== activeProject);

  const racks = racksFor(inputs.runners, [
    ...inputs.runs,
    ...(inputs.globalRuns ?? []),
    ...others.flatMap((p) => p.runs),
  ]);
  const runnerBusy = new Map(racks.map((rack) => [rack.runnerId, rack.busy]));

  const cap = inputs.budget?.budget.dailyTokens;
  const energy: Energy | undefined = inputs.budget
    ? cap && cap > 0
      ? { used: inputs.budget.used?.tokens ?? 0, cap }
      : { used: inputs.budget.used?.tokens ?? 0 }
    : undefined;

  const current = activeProject
    ? buildCurrentOffice({
        runs: inputs.runs,
        runners: inputs.runners,
        sessions: inputs.sessions,
        asks: inputs.asks,
        held: inputs.held,
        messages: inputs.messages,
        pipelines: inputs.pipelines,
        budget: inputs.budget,
        titles: nodeTitles(inputs.graph),
        now,
        project: activeProject,
        label: inputs.projectLabel,
        layout: inputs.graph?.type ?? "",
        officeStyle: inputs.graph?.officeStyle,
        waitingItems: inputs.waitingItems,
        runnerBusy,
      })
    : null;

  const global = globalOffice(inputs, runnerBusy);

  const compact = others.map((p) => compactOffice(p, now));
  const busy = compact.filter((office) => office.busy).sort(busyOrder);
  const idle = compact
    .filter((office) => !office.busy)
    .sort((a, b) => a.label.localeCompare(b.label) || a.project.localeCompare(b.project));

  const lounge: LoungeSpot[] = [
    ...(current?.loungeIds ?? []).map((actorId) => ({ id: actorId, actorId })),
    ...(global?.loungeIds ?? []).map((actorId) => ({ id: actorId, actorId })),
    ...loungeVisitors(remote, activeProject, now),
  ];

  const currentRunsById = new Map(dedupeRuns(inputs.runs).map((run) => [run.id, run]));
  const currentActors = (current?.actors ?? []).map((actor) => {
    if (actor.kind !== "run" || !actor.runId) return actor;
    const extras = runActorExtras(currentRunsById.get(actor.runId));
    return Object.keys(extras).length > 0 ? { ...actor, ...extras } : actor;
  });

  return {
    current: current?.office ?? null,
    global: global?.office ?? null,
    others: busy.slice(0, MAX_COMPACT),
    doorSigns: [...busy.slice(MAX_COMPACT), ...idle],
    lounge: lounge.slice(0, MAX_LOUNGE),
    loungeMore: Math.max(0, lounge.length - MAX_LOUNGE),
    racks,
    actors: [...currentActors, ...(global?.actors ?? [])],
    energy,
    truncated: Boolean(remote?.truncated),
    total: Math.max(remote?.total ?? 0, compact.length + (activeProject ? 1 : 0)),
  };
}
