import { describe, expect, it } from "vitest";
import type { Run } from "../api/runs";
import type { WaitingItem } from "../station/waiting";
import {
  buildCurrentOffice,
  dedupeRuns,
  FINISHED_RUN_SEAT_MS,
  FINISHED_SHOW_MS,
  runActorId,
  seatedRuns,
  sessionActorId,
  type CurrentOfficeInputs,
} from "./office";
import {
  ago,
  makeAsk,
  makeBudget,
  makeMessage,
  makePipeline,
  makeRun,
  makeRunner,
  makeSession,
  makeTask,
  NOW,
} from "./testFixtures";
import type { Actor } from "./world";

function office(overrides: Partial<CurrentOfficeInputs> = {}) {
  const built = buildCurrentOffice({
    runs: [],
    runners: [makeRunner()],
    sessions: [],
    asks: [],
    held: [],
    messages: [],
    titles: new Map([
      ["login", "Login form"],
      ["schema", "DB schema"],
    ]),
    now: NOW,
    project: "main",
    label: "Main",
    layout: "workflow",
    waitingItems: [],
    ...overrides,
  });
  return { ...built, meetings: built.office.meetings };
}

/** A runner taking `slots` runs at once. */
function runner(id: string, slots: number, online = true) {
  const base = makeRunner({ id, name: id, online });
  return { ...base, info: { ...base.info!, maxConcurrent: slots } };
}

function actor(actors: Actor[], id: string): Actor {
  const found = actors.find((a) => a.id === id);
  if (!found) throw new Error(`no actor ${id} in ${actors.map((a) => a.id).join(", ")}`);
  return found;
}

describe("the office", () => {
  it("names the project and its style, the layout's default until one is chosen", () => {
    expect(office().office).toMatchObject({ project: "main", label: "Main", style: "engineering", styleSet: false });
    expect(office({ layout: "story" }).office.style).toBe("study");
    expect(office({ officeStyle: "cafe" }).office).toMatchObject({ style: "cafe", styleSet: true });
    expect(office({ label: "" }).office.label).toBe("main");
  });

  it("puts each character where its state says: desk, meeting, door or lounge", () => {
    const working = makeRun({ createdAt: ago(50_000) });
    const offered = makeRun({ status: "offered", nodeId: "schema", createdAt: ago(40_000) });
    const plan = makeRun({ kind: "plan", nodeId: "plan", createdAt: ago(30_000) });
    const limit = makeRun({ status: "queued", retryAt: ago(-60_000), nodeId: "x", createdAt: ago(20_000) });
    const merging = makeRun({
      status: "succeeded",
      nodeId: "m",
      merge: { id: "mg", runId: "", headSha: "", status: "pending", requestedBy: "u", createdAt: ago(1_000) },
      createdAt: ago(10_000),
      finishedAt: ago(5_000),
    });
    const built = office({
      runners: [runner("rn1", 8)],
      runs: [working, offered, plan, limit, merging],
      sessions: [makeSession({ id: "idle", name: "idle-one" }), makeSession({ id: "ask", name: "asker", nodeId: "login" })],
      asks: [makeAsk({ sessionId: "ask" })],
    });
    expect(built.office.work).toEqual([runActorId(working.id), runActorId(merging.id)]);
    expect(built.office.meeting).toEqual([runActorId(plan.id), sessionActorId("ask")]);
    expect(built.office.entrance).toEqual([runActorId(offered.id)]);
    expect(built.loungeIds).toEqual([runActorId(limit.id), sessionActorId("idle")]);
    expect(actor(built.actors, runActorId(merging.id)).pose).toBe("awaiting-accept");
  });

  it("shows the free desks the online runners have room for, three at most", () => {
    const one = makeRun();
    expect(office({ runners: [runner("rn1", 2)], runs: [one] }).office.freeDesks).toBe(1);
    expect(office({ runners: [runner("rn1", 8)] }).office.freeDesks).toBe(3);
    expect(office({ runners: [runner("rn1", 4, false)] }).office.freeDesks).toBe(0);
    // A runner that does not say takes one run at once.
    expect(office({ runners: [makeRunner()], runs: [one] }).office.freeDesks).toBe(0);
    // Busy counts every project's runs when the building says so.
    expect(office({ runners: [runner("rn1", 3)], runnerBusy: new Map([["rn1", 3]]) }).office.freeDesks).toBe(0);
  });

  it("knows the runner a character works at, and an unknown runner is never offline", () => {
    const run = makeRun({ runnerId: "rn-other", runnerName: "cal-pc" });
    const a = office({ runs: [run] }).actors[0];
    expect(a).toMatchObject({ runnerId: "rn-other", runnerName: "cal-pc", runnerOnline: null, pose: "working" });
    const offline = office({ runners: [makeRunner({ online: false })], runs: [makeRun()] }).actors[0];
    expect(offline).toMatchObject({ runnerOnline: false, pose: "offline", area: "work" });
  });

  it("lays what waits on the viewer on the meeting table", () => {
    const pr = makeRun({ status: "succeeded", nodeId: "login", prUrl: "https://x/pull/4", prNumber: 4 });
    const task = makeTask({ nodeId: "schema", title: "" });
    const items: WaitingItem[] = [
      { kind: "merge", run: pr },
      { kind: "review", task: { autopilotId: "ap1", goalNodeId: "goal", task } },
      { kind: "held", message: makeMessage({ id: "loose", to: "nobody-here", body: "please rebase", status: "held" }) },
      { kind: "ask", ask: makeAsk() },
      { kind: "offer", run: makeRun({ status: "offered" }) },
    ];
    const built = office({ waitingItems: items });
    expect(built.office.items).toEqual([
      {
        id: `merge:${pr.id}`,
        kind: "merge",
        nodeId: "login",
        title: "Login form",
        target: { kind: "run", runId: pr.id, nodeId: "login" },
        actions: [
          { kind: "run", runId: pr.id, nodeId: "login" },
          { kind: "node", nodeId: "login" },
          { kind: "pr", runId: pr.id, url: "https://x/pull/4" },
        ],
      },
      {
        id: "review:ap1:schema",
        kind: "review",
        nodeId: "schema",
        title: "DB schema",
        target: { kind: "autopilot", goalNodeId: "goal" },
        actions: [
          { kind: "autopilot", goalNodeId: "goal" },
          { kind: "node", nodeId: "schema" },
        ],
      },
      {
        id: "held:loose",
        kind: "held",
        title: "please rebase",
        target: { kind: "held-message", messageId: "loose" },
        actions: [{ kind: "held-message", messageId: "loose" }],
      },
    ]);
  });

  it("leaves a held message off the table when its agent holds it already", () => {
    const session = makeSession({ id: "s", name: "laptop" });
    const message = makeMessage({ id: "env", to: "laptop", status: "held" });
    const built = office({ sessions: [session], held: [message], waitingItems: [{ kind: "held", message }] });
    expect(built.actors[0].heldMail.map((m) => m.id)).toEqual(["env"]);
    expect(built.office.items).toEqual([]);
  });
});

describe("run characters", () => {
  it("sits at its runner's desk with its node's title and click targets", () => {
    const run = makeRun({
      status: "succeeded",
      prUrl: "https://github.com/o/r/pull/9",
      prState: "open",
      ciStatus: "pending",
      commit: "abc",
      filesChanged: 2,
    });
    const world = office({ runs: [run] });
    const a = actor(world.actors, runActorId(run.id));
    expect(a).toMatchObject({
      kind: "run",
      agent: "claude",
      model: "opus",
      project: "main",
      area: "work",
      runnerName: "mac-mini",
      runnerOnline: true,
      nodeId: "login",
      nodeTitle: "Login form",
      runId: run.id,
      ownerName: "bea",
      pose: "waiting-ci",
      target: { kind: "run", runId: run.id, nodeId: "login" },
    });
    expect(a.actions).toEqual([
      { kind: "run", runId: run.id, nodeId: "login" },
      { kind: "node", nodeId: "login" },
      { kind: "run-diff", runId: run.id, nodeId: "login" },
      { kind: "pr", runId: run.id, url: "https://github.com/o/r/pull/9" },
    ]);
  });

  it("offers no diff without changes and no PR link that is not http(s)", () => {
    const run = makeRun({ prUrl: "javascript:alert(1)" });
    const a = office({ runs: [run] }).actors[0];
    expect(a.actions.map((t) => t.kind)).toEqual(["run", "node"]);
  });

  it("keeps parallel reviewers on one node apart, around one meeting table", () => {
    const pipeline = makePipeline({
      id: "p1",
      round: 2,
      status: "running",
      votes: [
        { agent: "codex", model: "o3", verdict: "changes", round: 1, runId: "old" },
        { agent: "codex", model: "o3", verdict: "approve", round: 2 },
        { agent: "gemini", model: "pro", verdict: "", round: 2 },
      ],
    });
    const implement = makeRun({
      status: "succeeded",
      pipelineId: "p1",
      prUrl: "https://x/pull/1",
      prState: "open",
      ciStatus: "pending",
      createdAt: ago(300_000),
    });
    const r1 = makeRun({ kind: "review", pipelineId: "p1", agent: "codex", model: "o3", createdAt: ago(100_000) });
    const r2 = makeRun({ kind: "review", pipelineId: "p1", agent: "gemini", model: "pro", createdAt: ago(90_000) });
    const world = office({ runs: [implement, r1, r2], pipelines: { p1: pipeline } });
    expect(world.actors.map((a) => a.id)).toEqual([runActorId(implement.id), runActorId(r1.id), runActorId(r2.id)]);
    expect(actor(world.actors, runActorId(r1.id)).pose).toBe("reviewing");
    expect(actor(world.actors, runActorId(implement.id)).pose).toBe("waiting-ci");
    expect(world.meetings).toHaveLength(1);
    expect(world.meetings[0]).toMatchObject({
      pipelineId: "p1",
      nodeId: "login",
      nodeTitle: "Login form",
      project: "main",
      round: 2,
      status: "running",
      waitingHuman: false,
      actorIds: [runActorId(r1.id), runActorId(r2.id)],
      target: { kind: "node", nodeId: "login" },
    });
    expect(world.meetings[0].votes.map((v) => v.verdict)).toEqual(["approve", ""]);
  });

  it("marks a meeting waiting for a person, and has none without a pipeline loaded", () => {
    const r1 = makeRun({ kind: "review", pipelineId: "p1" });
    const waiting = office({ runs: [r1], pipelines: { p1: makePipeline({ status: "waiting_human" }) } });
    expect(waiting.meetings[0].waitingHuman).toBe(true);
    const unloaded = office({ runs: [r1] });
    expect(unloaded.meetings[0]).toMatchObject({ status: "", round: 0, votes: [], waitingHuman: false });
  });

  it("swaps seats when a fallback takes over from a rate-limited run", () => {
    const failed = makeRun({ status: "failed", failureKind: "rate_limit", agent: "claude", model: "opus", createdAt: ago(50_000) });
    const fallback = makeRun({ retryOf: failed.id, agent: "codex", model: "o3", createdAt: ago(40_000) });
    const world = office({ runs: [failed, fallback] });
    // The predecessor gave its seat up.
    expect(world.actors.map((a) => a.id)).toEqual([runActorId(fallback.id)]);
    expect(world.actors[0].swappedFrom).toEqual({
      runId: failed.id,
      agent: "claude",
      model: "opus",
      failureKind: "rate_limit",
    });
  });

  it("does not swap seats for a task failure or the same agent and model", () => {
    const failed = makeRun({ status: "failed", failureKind: "task" });
    const retry = makeRun({ retryOf: failed.id, agent: "codex" });
    expect(office({ runs: [failed, retry] }).actors.at(-1)?.swappedFrom).toBeUndefined();
    const limited = makeRun({ status: "failed", failureKind: "quota" });
    const same = makeRun({ retryOf: limited.id });
    expect(office({ runs: [limited, same] }).actors.at(-1)?.swappedFrom).toBeUndefined();
  });

  it("shows energy from the run's live usage, against the daily budget when there is one", () => {
    const run = makeRun({ usage: { inputTokens: 1_000, outputTokens: 250 } });
    expect(office({ runs: [run] }).actors[0].energy).toEqual({ used: 1_250 });
    const capped = office({ runs: [run], budget: makeBudget(40_000, 100_000) });
    expect(capped.actors[0].energy).toEqual({ used: 1_250, cap: 100_000 });
    expect(office({ runs: [makeRun()] }).actors[0].energy).toBeUndefined();
  });
});

describe("the run's agent session", () => {
  const RUN_ID = "c0ffee000000000000000000";

  it("puts the session's hand, bubble and held mail on the run's character", () => {
    const run = makeRun({ id: RUN_ID });
    const session = makeSession({ id: "s-run", name: "visual-editor-claude-opus-c0ffee", run: true });
    const world = office({
      runs: [run],
      sessions: [session],
      asks: [makeAsk({ id: "ask1", sessionId: "s-run" }), makeAsk({ id: "done", sessionId: "s-run", status: "approved" })],
      messages: [makeMessage({ id: "said", senderSession: "visual-editor-claude-opus-c0ffee", createdAt: NOW - 2_000 })],
      held: [makeMessage({ id: "env", to: "visual-editor-claude-opus-c0ffee", status: "held" })],
    });
    expect(world.actors).toHaveLength(1);
    const a = world.actors[0];
    expect(a).toMatchObject({ sessionId: "s-run", sessionName: session.name, pose: "asking-user" });
    expect(a.ask?.id).toBe("ask1");
    expect(a.bubble?.id).toBe("said");
    expect(a.heldMail.map((m) => m.id)).toEqual(["env"]);
    expect(a.actions).toContainEqual({ kind: "ask", askId: "ask1", sessionId: "s-run" });
    expect(a.actions).toContainEqual({ kind: "held-message", messageId: "env" });
  });

  it("joins by the runId the hub reports, whatever the name", () => {
    const run = makeRun({ id: RUN_ID });
    const world = office({ runs: [run], sessions: [makeSession({ id: "s", name: "renamed", runId: RUN_ID })] });
    expect(world.actors).toHaveLength(1);
    expect(world.actors[0].sessionId).toBe("s");
  });

  it("lets a bubble pop after the window, and ignores held or rejected mail as speech", () => {
    const run = makeRun({ id: RUN_ID });
    const session = makeSession({ id: "s", runId: RUN_ID, name: "n" });
    const old = office({ runs: [run], sessions: [session], messages: [makeMessage({ senderSession: "n", createdAt: NOW - 10_000 })] });
    expect(old.actors[0].bubble).toBeUndefined();
    const held = office({
      runs: [run],
      sessions: [session],
      messages: [makeMessage({ senderSession: "n", status: "held" })],
    });
    expect(held.actors[0].bubble).toBeUndefined();
  });
});

describe("sessions with no run in view", () => {
  it("seats them by name, works at their node, and lounges with none", () => {
    const world = office({
      sessions: [
        makeSession({ id: "b", name: "zeta-codex", agent: "codex", nodeId: "schema", state: "busy", model: "o3" }),
        makeSession({ id: "a", name: "alpha-claude" }),
      ],
    });
    expect(world.actors.map((a) => [a.id, a.area])).toEqual([
      [sessionActorId("a"), "lounge"],
      [sessionActorId("b"), "work"],
    ]);
    expect(world.loungeIds).toEqual([sessionActorId("a")]);
    expect(world.office.work).toEqual([sessionActorId("b")]);
    const zeta = actor(world.actors, sessionActorId("b"));
    expect(zeta).toMatchObject({
      kind: "session",
      agent: "codex",
      model: "o3",
      pose: "working",
      nodeTitle: "DB schema",
      ownerName: "bea",
      runnerOnline: null,
      target: { kind: "node", nodeId: "schema" },
    });
    expect(zeta.runnerName).toBeUndefined();
    expect(zeta.actions).toContainEqual({ kind: "session-diff", sessionId: "b", nodeId: "schema" });
    const alpha = actor(world.actors, sessionActorId("a"));
    expect(alpha.target).toEqual({ kind: "session", sessionId: "a" });
    expect(alpha.nodeTitle).toBe("");
  });

  it("shows a person's quiet session offline, and drops a gone run's agent", () => {
    const world = office({
      sessions: [
        makeSession({ id: "person", lastSeen: NOW - 120_000 }),
        makeSession({ id: "ghost", run: true, name: "repo-claude-opus-deadbe", lastSeen: NOW - 120_000 }),
        makeSession({ id: "live-run", run: true, name: "repo-claude-opus-beefed" }),
      ],
    });
    expect(world.actors.map((a) => [a.id, a.pose])).toEqual([
      [sessionActorId("person"), "offline"],
      [sessionActorId("live-run"), "idle"],
    ]);
  });

  it("raises the hand of a session asking", () => {
    const world = office({ sessions: [makeSession({ id: "s1" })], asks: [makeAsk({ sessionId: "s1" })] });
    expect(world.actors[0].pose).toBe("asking-user");
  });
});

describe("which runs get a seat", () => {
  it("dedupes runs, the last copy winning", () => {
    const run = makeRun({ status: "running" });
    const later = { ...run, status: "succeeded" } as Run;
    expect(dedupeRuns([run, later])).toEqual([later]);
    expect(office({ runs: [run, later] }).actors).toHaveLength(0);
  });

  it("keeps a failed run until newer work on its node replaces it", () => {
    const failed = makeRun({ status: "failed", createdAt: ago(50_000), finishedAt: ago(40_000) });
    expect(seatedRuns([failed], NOW)).toEqual([failed]);
    const again = makeRun({ createdAt: ago(10_000) });
    expect(seatedRuns([failed, again], NOW)).toEqual([again]);
    // Work on another node does not.
    const elsewhere = makeRun({ nodeId: "schema", createdAt: ago(10_000) });
    expect(seatedRuns([failed, elsewhere], NOW)).toEqual([failed, elsewhere]);
  });

  it("keeps a run that just succeeded for a moment, cheering", () => {
    const done = makeRun({ status: "succeeded", finishedAt: ago(FINISHED_SHOW_MS - 1) });
    expect(seatedRuns([done], NOW)).toEqual([done]);
    expect(office({ runs: [done] }).actors[0].pose).toBe("finished");
    expect(seatedRuns([{ ...done, finishedAt: ago(FINISHED_SHOW_MS + 1) }], NOW)).toEqual([]);
    // Cancelled or declined runs leave at once.
    expect(seatedRuns([{ ...done, status: "cancelled" }], NOW)).toEqual([]);
  });

  it("lets a finished run go after a day", () => {
    const failed = makeRun({ status: "failed", finishedAt: ago(FINISHED_RUN_SEAT_MS + 1) });
    expect(seatedRuns([failed], NOW)).toEqual([]);
    const recent = makeRun({ status: "failed", finishedAt: ago(FINISHED_RUN_SEAT_MS - 1) });
    expect(seatedRuns([recent], NOW)).toEqual([recent]);
  });
});
