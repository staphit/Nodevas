import { fireEvent, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { BuildingProject, BuildingResponse } from "../../api/station";
import { readPreferences } from "../../preferences";
import type { ControlRow } from "../../station/control";
import { useApp } from "../../store";
import { buildBuilding, type BuildingInputs } from "../building";
import { GameActionsProvider } from "../GameActions";
import { ago, makeAsk, makeMessage, makeRun, makeRunner, makeSession, NOW } from "../testFixtures";
import type { Building } from "../world";
import * as PixelDesk from "./PixelDesk";
import WarRoomStage, { artPixel } from "./WarRoom";

const openSettings = vi.hoisted(() => vi.fn());
vi.mock("../../settings/openSettings", async (original) => ({
  ...(await original<typeof import("../../settings/openSettings")>()),
  openSettings,
}));

const GRAPH = {
  version: 1,
  type: "workflow",
  nodes: [
    { id: "login", title: "Login form" },
    { id: "schema", title: "DB schema" },
    { id: "docs", title: "API docs" },
  ],
};
const titles = new Map(GRAPH.nodes.map((node) => [node.id, node.title]));
const titleOf = (id: string) => titles.get(id) ?? "";

/** A runner taking `slots` runs at once. */
function runner(id: string, name: string, slots: number, online = true) {
  const base = makeRunner({ id, name, online });
  return { ...base, info: { ...base.info!, maxConcurrent: slots } };
}

function project(name: string, over: Partial<BuildingProject> = {}): BuildingProject {
  return {
    name,
    label: name,
    layout: "workflow",
    officeStyle: "cafe",
    officeStyleSet: true,
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

function building(over: Partial<BuildingInputs> = {}): Building {
  return buildBuilding({
    graph: GRAPH,
    runs: [],
    runners: [runner("rn1", "mac-mini", 3)],
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
  });
}

function mount(world: Building, rows: Record<string, ControlRow> = {}) {
  return render(
    <GameActionsProvider>
      <WarRoomStage building={world} titleOf={titleOf} rowOf={(id) => rows[id]} />
    </GameActionsProvider>,
  );
}

function reducedMotion(on: boolean) {
  window.matchMedia = vi.fn().mockReturnValue({
    matches: on,
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
  }) as never;
}

const remoteOf = (...projects: BuildingProject[]): BuildingResponse => ({
  projects,
  truncated: false,
  total: projects.length + 1,
});

beforeEach(() => {
  reducedMotion(false);
  openSettings.mockReset();
  useApp.setState({
    activeProject: "main",
    graph: GRAPH as never,
    preferences: { ...readPreferences(), language: "en", agentWorkstation: true },
  });
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe("the current office", () => {
  it("has a work area, a meeting room and an entrance, each with who belongs there", () => {
    const working = makeRun({ id: "w1", nodeId: "login", status: "running" });
    const offered = makeRun({ id: "o1", nodeId: "schema", status: "offered", agent: "codex", model: "gpt-5" });
    const { container } = mount(
      building({
        runs: [working, offered],
        sessions: [makeSession({ id: "s1", name: "laptop", nodeId: "docs" })],
        asks: [makeAsk({ sessionId: "s1" })],
      }),
    );
    const office = screen.getByRole("region", { name: "Office Main" });
    const work = within(office).getByRole("group", { name: "Work area" });
    const meeting = within(office).getByRole("group", { name: "Meeting room" });
    const entrance = within(office).getByRole("group", { name: "Entrance" });
    expect(within(work).getByRole("button", { name: /Claude opus in mac-mini: working, on “Login form”/ })).toBeInTheDocument();
    expect(within(meeting).getByRole("button", { name: /Claude in Terminals: waiting for your answer, on “API docs”/ })).toBeInTheDocument();
    const door = within(entrance).getByRole("button", { name: /Codex gpt-5 in mac-mini: waiting to be accepted/ });
    expect(door.querySelector('[data-part="bubble.wait"]')).not.toBeNull();
    // Free desks are drawn, not pressable: the one slot of three the runner
    // has left (with the working desk, the two desks an office has at least).
    expect(work.querySelectorAll(".px-cell.is-free")).toHaveLength(1);
    // The meeting table always has seats.
    expect(meeting.querySelectorAll(".px-cell.is-seat").length).toBeGreaterThanOrEqual(1);
    // Pixel art: crisp edges, sized in whole art pixels; the office in its style.
    const sprite = container.querySelector(".px-sprite")!;
    expect(sprite.getAttribute("shape-rendering")).toBe("crispEdges");
    expect(sprite.getAttribute("viewBox")).toMatch(/^0 0 \d+ \d+$/);
    expect(office).toHaveClass("px-style--engineering");
  });

  it("draws each state with its own picture", () => {
    const run = (id: string, nodeId: string, over: Parameters<typeof makeRun>[0]) => makeRun({ id, nodeId, ...over });
    const { container } = mount(
      building({
        runners: [runner("rn1", "mac-mini", 6)],
        runs: [
          run("a", "login", { status: "running" }),
          run("b", "schema", { status: "failed", finishedAt: ago(1_000) }),
          run("c", "docs", { status: "queued", retryAt: ago(-60_000) }),
          run("d", "x1", { status: "succeeded", finishedAt: ago(1_000) }),
        ],
      }),
    );
    const desk = (pose: string) => container.querySelector(`.px-desk.pose-${pose}`)!;
    expect(desk("working").querySelector('[data-part="screen.code.lines"]')).not.toBeNull();
    expect(desk("working").querySelector(".px-sprite")).toHaveClass("px-anim");
    expect(desk("failed").querySelector('[data-part="screen.fail.glyph"]')).not.toBeNull();
    expect(desk("finished").querySelector('[data-part="screen.ok.glyph"]')).not.toBeNull();
    expect(desk("finished").querySelector(".px-sprite")).not.toHaveClass("px-anim");
    // Asleep on a usage limit: on the lounge's sofa, zzz.
    const lounge = screen.getByRole("region", { name: "Lounge" });
    const asleep = within(lounge).getByRole("button", { name: /asleep until its usage limit resets/ });
    expect(asleep.querySelector('[data-part="bubble.sleep"]')).not.toBeNull();
    expect(within(desk("working") as HTMLElement).getByText("Login form")).toBeInTheDocument();
  });

  it("lays what waits on the viewer on the meeting table, each a menu", () => {
    const pr = makeRun({ id: "pr1", status: "succeeded", nodeId: "login", prUrl: "https://x/pull/2", prNumber: 2 });
    mount(building({ waitingItems: [{ kind: "merge", run: pr }] }));
    const meeting = screen.getByRole("group", { name: "Meeting room" });
    const item = within(meeting).getByRole("button", { name: "Pull request ready to merge: “Login form”." });
    expect(item.querySelector('[data-part="item.merge"]')).not.toBeNull();
    fireEvent.click(item);
    const items = within(screen.getByRole("menu")).getAllByRole("menuitem").map((entry) => entry.textContent);
    expect(items).toEqual(["Open run", "Open “Login form”", "Open pull request"]);
  });

  it("opens a character's actions from the keyboard: the question first, then the run's own", async () => {
    const user = userEvent.setup();
    const run = makeRun({ id: "r1", nodeId: "login", status: "running" });
    const session = makeSession({ id: "s1", name: "app-claude-r1", run: true, runId: "r1", nodeId: "login" });
    mount(
      building({
        runs: [run],
        sessions: [session],
        asks: [makeAsk({ sessionId: "s1" })],
        held: [makeMessage({ id: "m1", to: "app-claude-r1", status: "held" })],
      }),
      { r1: { run } },
    );
    const desk = screen.getByRole("button", { name: /waiting for your answer, on “Login form”/ });
    expect(desk.querySelector('[data-part="bubble.ask"]')).not.toBeNull();
    expect(desk.querySelector('[data-part="mail"]')).not.toBeNull();
    desk.focus();
    await user.keyboard("{Enter}");
    const items = within(screen.getByRole("menu"))
      .getAllByRole("menuitem")
      .map((item) => item.textContent);
    expect(items.slice(0, 2)).toEqual(["Answer question", "Review held message"]);
    expect(items).toEqual(expect.arrayContaining(["Open run", "View changes", "Open “Login form”", "Cancel run"]));
    await user.keyboard("{Escape}");
    expect(screen.queryByRole("menu")).not.toBeInTheDocument();
    expect(desk).toHaveFocus();
    await user.keyboard(" ");
    expect(screen.getByRole("menu")).toBeInTheDocument();
  });

  it("opens a terminal agent's node from its menu", () => {
    const openTab = vi.fn().mockResolvedValue(undefined);
    useApp.setState({ openTab } as never);
    mount(building({ sessions: [makeSession({ id: "t1", nodeId: "docs", state: "busy" })] }));
    const work = screen.getByRole("group", { name: "Work area" });
    fireEvent.click(within(work).getByRole("button", { name: /in Terminals: working, on “API docs”/ }));
    fireEvent.click(screen.getByRole("menuitem", { name: "Open “API docs”" }));
    expect(openTab).toHaveBeenCalledWith("docs");
  });

  it("changes its style from the door sign", async () => {
    const updateWorkflowDefinition = vi.fn().mockResolvedValue({ ok: true });
    useApp.setState({ updateWorkflowDefinition } as never);
    mount(building());
    const sign = screen.getByRole("button", { name: "Main, office style: Engineering. Change it" });
    fireEvent.click(sign);
    const picker = screen.getByRole("dialog", { name: "Office style of Main" });
    const options = within(picker).getAllByRole("radio");
    expect(options.map((option) => option.textContent)).toEqual([
      "Automatic (Engineering)",
      "Default office",
      "Engineering",
      "Study",
      "Studio",
      "Café",
    ]);
    fireEvent.click(within(picker).getByRole("radio", { name: "Study" }));
    await vi.waitFor(() =>
      expect(updateWorkflowDefinition).toHaveBeenCalledWith({ type: "workflow.setOfficeStyle", style: "study" }),
    );
    fireEvent.keyDown(document, { key: "Escape" });
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(sign).toHaveFocus();
  });
});

describe("the building", () => {
  it("goes to a busy office from its card, and straight there from a door sign", () => {
    const switchProject = vi.fn().mockResolvedValue(undefined);
    useApp.setState({ switchProject } as never);
    const busy = project("app", {
      runs: [
        { id: "x1", nodeId: "n", title: "Search", agent: "codex", model: "", kind: "", status: "running", runnerId: "rn1", runnerName: "mac-mini", createdAt: ago(1_000) },
      ],
      waiting: { asks: 1, held: 0, reviews: 0, total: 1 },
    });
    mount(building({ remote: remoteOf(busy, project("quiet")) }));
    const others = screen.getByRole("list", { name: "Other offices" });
    const card = within(others).getByRole("button", { name: /^app: 1 working, 1 waiting on you, Café\. Go to app$/ });
    expect(within(card).getByText("✋ 1")).toBeInTheDocument();
    fireEvent.click(card);
    fireEvent.click(screen.getByRole("menuitem", { name: "Go to app" }));
    expect(switchProject).toHaveBeenCalledWith("app");

    const doors = screen.getByRole("list", { name: "Quiet offices" });
    fireEvent.click(within(doors).getByRole("button", { name: /^quiet: nobody in/ }));
    expect(switchProject).toHaveBeenCalledWith("quiet");
    expect(screen.queryByRole("menu")).not.toBeInTheDocument();
  });

  it("opens the runners from a rack in the server room", () => {
    mount(building({ runners: [runner("rn1", "mac-mini", 2), runner("rn2", "old-box", 1, false)], runs: [makeRun()] }));
    const room = screen.getByRole("region", { name: "Server room" });
    const rack = within(room).getByRole("button", { name: "Runner mac-mini: Online, 1 of 2 slots busy. Manage runners" });
    expect(rack.querySelectorAll('[data-part$=".busy"]')).toHaveLength(1);
    const off = within(room).getByRole("button", { name: /Runner old-box: Offline/ });
    expect(off).toHaveClass("is-lights-off");
    fireEvent.click(rack);
    expect(openSettings).toHaveBeenCalledWith("runners");
  });

  it("says the building in words for a screen reader, by project and room", () => {
    const pr = makeRun({ id: "pr1", status: "succeeded", nodeId: "login", prUrl: "https://x/pull/2" });
    mount(
      building({
        runs: [makeRun({ id: "r1", nodeId: "login", status: "running" })],
        sessions: [makeSession({ id: "idle", name: "idle", agent: "codex" })],
        waitingItems: [{ kind: "merge", run: pr }],
        remote: remoteOf(project("app")),
      }),
    );
    const text = screen.getByTestId("war-room-text");
    const headings = within(text)
      .getAllByRole("heading")
      .map((heading) => heading.textContent);
    expect(headings).toEqual([
      "Office Main",
      "Work area",
      "Meeting room",
      "Entrance",
      "Other offices",
      "Lounge",
      "Server room",
    ]);
    expect(within(text).getByText(/in mac-mini: working, on “Login form”\./)).toBeInTheDocument();
    // Two desks at least, plus the two the runner has room for.
    expect(within(text).getByText("2 free desk(s).")).toBeInTheDocument();
    expect(within(text).getByText("Pull request ready to merge: “Login form”.")).toBeInTheDocument();
    expect(within(text).getByText(/^app: nobody in, Café\. Go to app$/)).toBeInTheDocument();
    expect(within(text).getByText(/Codex in Terminals: idle\./)).toBeInTheDocument();
    expect(within(text).getByText(/^Runner mac-mini: Online/)).toBeInTheDocument();
  });

  it("holds still under reduced motion", () => {
    const world = building({ runs: [makeRun({ id: "r1", status: "running" })] });
    const { container, unmount } = mount(world);
    expect(container.querySelector(".px-office")).toHaveAttribute("data-motion", "full");
    unmount();
    reducedMotion(true);
    const again = mount(world);
    expect(again.container.querySelector(".px-office")).toHaveAttribute("data-motion", "reduced");
  });

  it("does not redraw a desk on a heartbeat that changed nothing", () => {
    const runs = [makeRun({ id: "r1", status: "running", usage: { inputTokens: 5, outputTokens: 5 } })];
    const props = (world: Building) => {
      const actor = world.actors[0];
      return { actor, scene: "desk" as const, style: "engineering" as const, label: "x", titleOf };
    };
    const before = props(building({ runs }));
    // A tick later the world is rebuilt: new objects, the same desk.
    const after = props(building({ runs, now: NOW + 30_000 }));
    expect(after.actor).not.toBe(before.actor);
    expect(PixelDesk.sameDesk(before, after)).toBe(true);
    // A change it shows does redraw it.
    const moved = props(building({ runs: [{ ...runs[0], status: "failed", finishedAt: ago(1) }] }));
    expect(PixelDesk.sameDesk(before, moved)).toBe(false);
    expect(PixelDesk.sameDesk(before, { ...before, style: "cafe" })).toBe(false);
  });

  it("asks to turn the workstation on when it is hidden", () => {
    useApp.setState({ preferences: { ...useApp.getState().preferences, agentWorkstation: false } });
    mount(building());
    expect(screen.queryByRole("region", { name: "Office Main" })).not.toBeInTheDocument();
    expect(screen.getByRole("status")).toHaveTextContent("The war room shows your agent workstation");
  });
});

describe("artPixel", () => {
  it("is a whole number of device pixels, near three CSS pixels", () => {
    for (const ratio of [1, 1.25, 1.5, 1.75, 2, 2.5, 3]) {
      const devicePixels = artPixel(ratio) * ratio;
      expect(Math.abs(devicePixels - Math.round(devicePixels))).toBeLessThan(1e-9);
      expect(Math.abs(artPixel(ratio) - 3)).toBeLessThanOrEqual(0.5);
    }
    expect(artPixel(1)).toBe(3);
    expect(artPixel(2)).toBe(3);
    expect(artPixel(1.5)).toBeCloseTo(10 / 3);
    expect(artPixel(0)).toBe(3);
  });
});

// W-19: role badges and the red/green test dot on the war room's desks.
describe("role badges", () => {
  it("badges each built-in role with its short label, and a custom role", () => {
    const roles = ["planner", "qa", "tester", "executor", "reviewer", "researcher", "security-auditor"];
    const runs = roles.map((role, i) =>
      makeRun({ id: `r${i}`, status: "running", role, nodeId: ["login", "schema", "docs"][i % 3] }),
    );
    const { container } = mount(building({ runners: [runner("rn1", "mac-mini", 8)], runs }));
    const badges = Array.from(container.querySelectorAll(".px-badge.is-role")).map((el) => el.textContent);
    expect(badges.sort()).toEqual(["EXE", "PLN", "QA", "RES", "REV", "ROLE", "TST"].sort());
    expect(container.querySelector(".px-badge.is-role.role--tester")).not.toBeNull();
    expect(container.querySelector(".px-badge.is-role.role--custom")).not.toBeNull();
  });

  it("shows the test state as a dot with a label, and no badge for a session", () => {
    const tester = makeRun({ id: "t1", status: "running", role: "tester", kind: "test", pipelineId: "p1", createdAt: ago(60_000) });
    const verify = makeRun({
      id: "v1",
      status: "succeeded",
      kind: "verify",
      pipelineId: "p1",
      createdAt: ago(30_000),
      finishedAt: ago(20_000),
      verify: { phase: "green", exitCode: 0, passed: true, durationMs: 900 },
    });
    const { container } = mount(
      building({ runs: [tester, verify], sessions: [makeSession({ id: "s1", name: "laptop", nodeId: "docs" })] }),
    );
    const dot = container.querySelector(".px-badge.is-test.is-green");
    expect(dot).not.toBeNull();
    expect(dot).toHaveAttribute("aria-label", "Tests green");
    expect(container.querySelectorAll(".px-badge.is-role")).toHaveLength(1);
    expect(screen.getAllByText(/as Tester/).length).toBeGreaterThan(0);
  });

  it("redraws a desk when its role or test state changes", () => {
    const runs = [makeRun({ id: "r1", status: "running", role: "executor" })];
    const world = building({ runs });
    const before = { actor: world.actors[0], scene: "desk" as const, style: "engineering" as const, label: "x", titleOf };
    expect(PixelDesk.sameDesk(before, { ...before, actor: { ...before.actor, role: "tester" } })).toBe(false);
    expect(PixelDesk.sameDesk(before, { ...before, actor: { ...before.actor, testState: "red" } })).toBe(false);
  });
});
