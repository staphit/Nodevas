import { describe, expect, it } from "vitest";

import type { BuildingProject, BuildingResponse, BuildingRun, BuildingSession } from "../api/station";
import { waitingOnYou } from "../station/waiting";
import {
  buildBuilding,
  busyOrder,
  compactOffice,
  loungeVisitors,
  MAX_COMPACT,
  racksFor,
  type BuildingInputs,
} from "./building";
import { runActorId, sessionActorId } from "./office";
import { ago, makeBudget, makeRun, makeRunner, makeSession, NOW } from "./testFixtures";

let seq = 0;

function brun(over: Partial<BuildingRun> = {}): BuildingRun {
  seq += 1;
  return {
    id: `r${seq}`,
    nodeId: `n${seq}`,
    title: `Task ${seq}`,
    agent: "claude",
    model: "opus",
    kind: "",
    status: "running",
    runnerId: "rn1",
    runnerName: "mac-mini",
    createdAt: ago(60_000),
    ...over,
  };
}

function bsession(over: Partial<BuildingSession> = {}): BuildingSession {
  seq += 1;
  return {
    id: `s${seq}`,
    name: `session-${seq}`,
    agent: "codex",
    model: "o3",
    state: "idle",
    nodeId: "",
    doing: "",
    runId: "",
    lastSeen: NOW - 1_000,
    online: true,
    ...over,
  };
}

function project(name: string, over: Partial<BuildingProject> = {}): BuildingProject {
  return {
    name,
    label: name.toUpperCase(),
    layout: "workflow",
    officeStyle: "engineering",
    officeStyleSet: false,
    runs: [],
    runsTruncated: false,
    othersActive: 0,
    sessions: [],
    waiting: { asks: 0, held: 0, reviews: 0, total: 0 },
    asks: [],
    reviews: [],
    ...over,
  };
}

function remote(...projects: BuildingProject[]): BuildingResponse {
  return { projects, truncated: false, total: projects.length };
}

function inputs(over: Partial<BuildingInputs> = {}): BuildingInputs {
  return {
    graph: { version: 1, nodes: [{ id: "login", title: "Login form" }], type: "story" },
    runs: [],
    runners: [makeRunner()],
    sessions: [],
    asks: [],
    held: [],
    messages: [],
    now: NOW,
    activeProject: "main",
    projectLabel: "Main",
    remote: null,
    waitingItems: [],
    ...over,
  };
}

describe("the current office", () => {
  it("is the active project's, from the local stores, styled by its layout", () => {
    const run = makeRun();
    const offered = makeRun({ status: "offered", nodeId: "x" });
    const building = buildBuilding(inputs({ runs: [run, offered], runners: [makeRunner()] }));
    expect(building.current).toMatchObject({
      project: "main",
      label: "Main",
      layout: "story",
      style: "study",
      work: [runActorId(run.id)],
      entrance: [runActorId(offered.id)],
    });
    // The runner takes one run at once and holds two: no free desk.
    expect(building.current?.freeDesks).toBe(0);
    expect(building.actors.map((actor) => actor.project)).toEqual(["main", "main"]);
  });

  it("lays the meeting table from what waits on the viewer", () => {
    const pr = makeRun({ status: "succeeded", nodeId: "login", prUrl: "https://x/pull/1" });
    const building = buildBuilding(inputs({ waitingItems: [{ kind: "merge", run: pr }] }));
    expect(building.current?.items.map((item) => [item.id, item.title])).toEqual([[`merge:${pr.id}`, "Login form"]]);
  });

  it("keeps the day's tokens against the budget", () => {
    expect(buildBuilding(inputs({ budget: makeBudget(40_000, 100_000) })).energy).toEqual({ used: 40_000, cap: 100_000 });
    expect(buildBuilding(inputs({ budget: makeBudget(5, 0) })).energy).toEqual({ used: 5 });
    expect(buildBuilding(inputs()).energy).toBeUndefined();
  });

  it("ignores the hub's summary of the active project: the stores are fresher", () => {
    const building = buildBuilding(inputs({ remote: remote(project("main", { runs: [brun()] }), project("app")) }));
    expect(building.others.map((office) => office.project)).toEqual([]);
    expect(building.doorSigns.map((office) => office.project)).toEqual(["app"]);
    expect(building.total).toBe(2);
  });
});

describe("compactOffice", () => {
  it("counts running, waiting, at the door, asleep and others' runs", () => {
    const p = project("app", {
      runs: [
        brun({ status: "running" }),
        brun({ status: "running", kind: "review" }),
        brun({ status: "offered" }),
        brun({ status: "queued", retryAt: ago(-60_000) }),
        brun({ status: "succeeded", prUrl: "https://x/pull/2", prState: "open", ciStatus: "success", finishedAt: ago(1_000) }),
      ],
      othersActive: 2,
      waiting: { asks: 1, held: 0, reviews: 1, total: 2 },
    });
    const office = compactOffice(p, NOW);
    expect(office.counts).toEqual({ running: 2, waiting: 1 + 1 + 2, offered: 1, sleeping: 1, othersActive: 2 });
    expect(office.busy).toBe(true);
    expect(office.desks.map((desk) => desk.pose)).toEqual(["working", "reviewing", "awaiting-accept"]);
    expect(office.more).toBe(0);
    expect(office.style).toBe("engineering");
  });

  it("counts the runs waiting on the viewer exactly as Waiting on you does", () => {
    const runs = [
      brun({ status: "succeeded", prUrl: "https://x/pull/3", prState: "open", ciStatus: "success", finishedAt: ago(1_000) }),
      brun({ status: "succeeded", prUrl: "https://x/pull/4", prState: "open", ciStatus: "failure", finishedAt: ago(1_000) }),
      brun({ status: "failed", failureKind: "quota", finishedAt: ago(1_000) }),
      brun({ status: "offered" }),
    ];
    const expected = waitingOnYou<BuildingRun>({
      runs,
      active: runs.filter((run) => run.status === "offered"),
      asks: [],
      held: [],
      reviews: [],
      canAnswer: () => true,
      now: NOW,
    }).length;
    expect(compactOffice(project("app", { runs }), NOW).counts.waiting).toBe(expected);
    expect(expected).toBe(4);
  });

  it("draws three desks, running then planning then at the door, and counts the rest", () => {
    const p = project("app", {
      runs: [
        brun({ status: "offered", agent: "gemini" }),
        brun({ status: "running", kind: "plan" }),
        brun({ status: "running", agent: "codex" }),
        brun({ status: "running", agent: "claude" }),
        brun({ status: "running", agent: "opencode" }),
      ],
    });
    const office = compactOffice(p, NOW);
    expect(office.desks.map((desk) => desk.agent)).toEqual(["codex", "claude", "opencode"]);
    expect(office.more).toBe(2);
  });

  it("raises a hand where a run's agent asked the viewer something", () => {
    const run = brun({ status: "running" });
    const session = bsession({ runId: run.id, state: "busy" });
    const p = project("app", {
      runs: [run],
      sessions: [session],
      asks: [{ id: "a1", sessionId: session.id, sessionName: session.name, agent: "claude", question: "?", nodeId: "", createdAt: NOW }],
      waiting: { asks: 1, held: 0, reviews: 0, total: 1 },
    });
    expect(compactOffice(p, NOW).desks[0].pose).toBe("asking-user");
  });

  it("offers going there, then its runs, then what waits there", () => {
    const run = brun({ status: "running", nodeId: "n-run" });
    const p = project("app", {
      runs: [run],
      asks: [{ id: "a1", sessionId: "s1", sessionName: "x", agent: "claude", question: "?", nodeId: "", createdAt: NOW }],
      reviews: [
        { autopilotId: "ap", goalNodeId: "goal", nodeId: "t1", pipelineId: "p" },
        { autopilotId: "ap", goalNodeId: "goal", nodeId: "t2", pipelineId: "q" },
      ],
    });
    expect(compactOffice(p, NOW).actions).toEqual([
      { kind: "project", project: "app" },
      { kind: "in-project", project: "app", then: { kind: "run", runId: run.id, nodeId: "n-run" } },
      { kind: "in-project", project: "app", then: { kind: "ask", askId: "a1", sessionId: "s1" } },
      { kind: "in-project", project: "app", then: { kind: "autopilot", goalNodeId: "goal" } },
    ]);
  });

  it("is idle with nothing going on", () => {
    const office = compactOffice(project("quiet", { layout: "story", officeStyle: "" }), NOW);
    expect(office).toMatchObject({ busy: false, desks: [], more: 0, style: "study" });
  });
});

describe("the other offices", () => {
  it("draws the busy ones busiest first, and the idle ones as door signs", () => {
    const building = buildBuilding(
      inputs({
        remote: remote(
          project("zeta"),
          project("beta", { runs: [brun()] }),
          project("alpha", { runs: [brun(), brun()] }),
          project("gamma", { waiting: { asks: 1, held: 0, reviews: 0, total: 1 } }),
          project("delta"),
        ),
      }),
    );
    expect(building.others.map((office) => office.project)).toEqual(["alpha", "beta", "gamma"]);
    expect(building.doorSigns.map((office) => office.project)).toEqual(["delta", "zeta"]);
  });

  it("keeps the busy ones past MAX_COMPACT as door signs, ahead of the idle", () => {
    const busy = Array.from({ length: MAX_COMPACT + 2 }, (_, i) => project(`p${String(i).padStart(2, "0")}`, { runs: [brun()] }));
    const building = buildBuilding(inputs({ remote: remote(...busy, project("idle")) }));
    expect(building.others).toHaveLength(MAX_COMPACT);
    expect(building.doorSigns.map((office) => office.project)).toEqual(["p08", "p09", "idle"]);
  });

  it("orders by work, then what waits, then name", () => {
    const office = (name: string, running: number, waiting: number) =>
      ({ project: name, label: name, counts: { running, waiting, offered: 0, sleeping: 0, othersActive: 0 } }) as never;
    const list = [office("b", 1, 0), office("a", 1, 0), office("c", 1, 2), office("d", 3, 0)];
    expect(list.sort(busyOrder).map((o: { project: string }) => o.project)).toEqual(["d", "c", "a", "b"]);
  });

  it("says when the hub left projects out", () => {
    const response = { ...remote(project("app")), truncated: true, total: 70 };
    const building = buildBuilding(inputs({ remote: response }));
    expect(building).toMatchObject({ truncated: true, total: 70 });
  });
});

describe("the lounge", () => {
  it("seats the current project's idle first, then other projects' visitors", () => {
    const idleHere = makeSession({ id: "here" });
    const visitor = bsession({ id: "there", agent: "gemini" });
    const asleep = brun({ status: "queued", retryAt: ago(-60_000), title: "Search" });
    const building = buildBuilding(
      inputs({
        sessions: [idleHere],
        remote: remote(project("app", { sessions: [visitor, bsession({ state: "busy" }), bsession({ nodeId: "n" })], runs: [asleep] })),
      }),
    );
    expect(building.lounge.map((spot) => spot.actorId ?? spot.id)).toEqual([
      sessionActorId("here"),
      "visitor:app:session:there",
      `visitor:app:run:${asleep.id}`,
    ]);
    expect(building.lounge[2].visitor).toMatchObject({ pose: "limit", title: "Search", label: "APP" });
  });

  it("skips the active project, and sessions that went quiet", () => {
    const response = remote(
      project("main", { sessions: [bsession()] }),
      project("app", { sessions: [bsession({ lastSeen: NOW - 120_000 })] }),
    );
    expect(loungeVisitors(response, "main", NOW)).toEqual([]);
  });

  it("caps the seats and counts the rest", () => {
    const sessions = Array.from({ length: 13 }, () => bsession());
    const building = buildBuilding(inputs({ remote: remote(project("app", { sessions })) }));
    expect(building.lounge).toHaveLength(10);
    expect(building.loungeMore).toBe(3);
  });
});

describe("the server room", () => {
  it("racks every runner, busy with its runs in every project, each counted once", () => {
    const runner = makeRunner({ id: "rn1" });
    const racks = racksFor(
      [{ ...runner, info: { ...runner.info!, maxConcurrent: 3 } }, makeRunner({ id: "rn2", name: "box", online: false })],
      [
        { id: "a", runnerId: "rn1", status: "running" },
        { id: "a", runnerId: "rn1", status: "running" },
        { id: "b", runnerId: "rn1", status: "offered" },
        { id: "c", runnerId: "rn1", status: "succeeded" },
        { id: "d", runnerId: "rn-x", runnerName: "cal-pc", status: "running" },
      ],
    );
    expect(racks.map((rack) => [rack.runnerId, rack.online, rack.busy, rack.slots])).toEqual([
      ["rn1", true, 2, 3],
      ["rn2", false, 0, undefined],
      ["rn-x", null, 1, undefined],
    ]);
    expect(racks[2].name).toBe("cal-pc");
    expect(racks[0].target).toEqual({ kind: "runners" });
  });

  it("counts other projects' runs on the shared runners", () => {
    const local = makeRun({ id: "loc", runnerId: "rn1" });
    const building = buildBuilding(
      inputs({ runs: [local], remote: remote(project("app", { runs: [brun({ id: "loc" }), brun({ id: "far" })] })) }),
    );
    expect(building.racks[0].busy).toBe(2);
  });
});
