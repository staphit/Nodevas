import { fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { readPreferences } from "../../preferences";
import type { ControlRow } from "../../station/control";
import { useApp } from "../../store";
import { GameActionsProvider } from "../GameActions";
import { buildOffice, type OfficeInputs } from "../office";
import { ago, makeAsk, makeMessage, makeRun, makeRunner, makeSession, NOW } from "../testFixtures";
import WarRoomStage, { artPixel } from "./WarRoom";

const titles = new Map([
  ["login", "Login form"],
  ["schema", "DB schema"],
  ["docs", "API docs"],
]);

function office(overrides: Partial<OfficeInputs> = {}) {
  return buildOffice({
    runs: [],
    runners: [],
    sessions: [],
    asks: [],
    held: [],
    messages: [],
    titles,
    now: NOW,
    ...overrides,
  });
}

/** A runner taking `slots` runs at once. */
function runner(id: string, name: string, slots: number, online = true) {
  const base = makeRunner({ id, name, online });
  return { ...base, info: { ...base.info!, maxConcurrent: slots } };
}

const titleOf = (id: string) => titles.get(id) ?? "";

function mount(world: ReturnType<typeof office>, rows: Record<string, ControlRow> = {}) {
  return render(
    <GameActionsProvider>
      <WarRoomStage office={world} titleOf={titleOf} rowOf={(id) => rows[id]} />
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

beforeEach(() => {
  reducedMotion(false);
  useApp.setState({
    activeProject: "p",
    preferences: { ...readPreferences(), language: "en", agentWorkstation: true },
  });
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe("the pixel war room", () => {
  it("draws a room per runner with a desk per slot, the lights off when offline", () => {
    const working = makeRun({ id: "w1", runnerId: "rn1", nodeId: "login", status: "running" });
    const { container } = mount(
      office({
        runners: [runner("rn1", "mac-mini", 3), runner("rn2", "old-box", 2, false)],
        runs: [working],
      }),
    );
    const mini = screen.getByRole("region", { name: "Room mac-mini" });
    const box = screen.getByRole("region", { name: "Room old-box" });
    // Three desks: one taken, two free (drawn, not pressable).
    expect(within(mini).getAllByRole("button")).toHaveLength(1);
    expect(mini.querySelectorAll(".px-cell.is-free")).toHaveLength(2);
    expect(box.querySelectorAll(".px-cell.is-free")).toHaveLength(2);
    expect(box).toHaveClass("is-lights-off");
    expect(mini).not.toHaveClass("is-lights-off");
    expect(within(box).getByText("Offline")).toBeInTheDocument();
    // Pixel art: crisp edges, sized in whole art pixels.
    const sprite = container.querySelector(".px-sprite")!;
    expect(sprite.getAttribute("shape-rendering")).toBe("crispEdges");
    expect(sprite.getAttribute("viewBox")).toMatch(/^0 0 \d+ \d+$/);
  });

  it("draws each state with its own picture", () => {
    const run = (id: string, nodeId: string, over: Parameters<typeof makeRun>[0]) =>
      makeRun({ id, nodeId, runnerId: "rn1", ...over });
    const world = office({
      runners: [runner("rn1", "mac-mini", 6)],
      runs: [
        run("a", "login", { status: "running" }),
        run("b", "schema", { status: "failed", finishedAt: ago(1_000) }),
        run("c", "docs", { status: "queued", retryAt: ago(-60_000) }),
        run("d", "x1", { status: "succeeded", finishedAt: ago(1_000) }),
        run("e", "x2", { status: "offered" }),
      ],
    });
    const { container } = mount(world);
    const desk = (pose: string) => container.querySelector(`.px-desk.pose-${pose}`)!;
    expect(desk("working").querySelector('[data-part="screen.code.lines"]')).not.toBeNull();
    expect(desk("working").querySelector(".px-sprite")).toHaveClass("px-anim");
    expect(desk("failed").querySelector('[data-part="screen.fail.glyph"]')).not.toBeNull();
    expect(desk("limit").querySelector('[data-part="bubble.sleep"]')).not.toBeNull();
    expect(desk("finished").querySelector('[data-part="screen.ok.glyph"]')).not.toBeNull();
    expect(desk("finished").querySelector(".px-sprite")).not.toHaveClass("px-anim");
    // An offered run waits at the door, not at a desk.
    expect(desk("awaiting-accept")).toHaveClass("at-door");
    expect(desk("awaiting-accept").querySelector('[data-part="bubble.wait"]')).not.toBeNull();
    // Each name tag is text: the agent and its node.
    expect(within(desk("working") as HTMLElement).getByText("Login form")).toBeInTheDocument();
  });

  it("opens a character's actions: the question first, then the run's own", () => {
    const run = makeRun({ id: "r1", runnerId: "rn1", nodeId: "login", status: "running" });
    const session = makeSession({ id: "s1", name: "app-claude-r1", run: true, runId: "r1", nodeId: "login" });
    const world = office({
      runners: [runner("rn1", "mac-mini", 2)],
      runs: [run],
      sessions: [session],
      asks: [makeAsk({ sessionId: "s1" })],
      held: [makeMessage({ id: "m1", to: "app-claude-r1", status: "held" })],
    });
    mount(world, { r1: { run } });

    const desk = screen.getByRole("button", { name: /waiting for your answer, on “Login form”/ });
    expect(desk.querySelector('[data-part="bubble.ask"]')).not.toBeNull();
    expect(desk.querySelector('[data-part="mail"]')).not.toBeNull();
    fireEvent.click(desk);
    const menu = screen.getByRole("menu");
    const items = within(menu)
      .getAllByRole("menuitem")
      .map((item) => item.textContent);
    expect(items.slice(0, 2)).toEqual(["Answer question", "Review held message"]);
    expect(items).toEqual(expect.arrayContaining(["Open run", "View changes", "Open “Login form”", "Cancel run"]));
    fireEvent.click(within(menu).getByRole("menuitem", { name: "Answer question" }));
    expect(screen.queryByRole("menu")).not.toBeInTheDocument();
  });

  it("opens a terminal agent's node from its menu", () => {
    const openTab = vi.fn().mockResolvedValue(undefined);
    useApp.setState({ openTab } as never);
    mount(office({ sessions: [makeSession({ id: "t1", nodeId: "docs", state: "busy" })] }));
    const room = screen.getByRole("region", { name: "Room Terminals" });
    fireEvent.click(within(room).getByRole("button", { name: /working, on “API docs”/ }));
    fireEvent.click(screen.getByRole("menuitem", { name: "Open “API docs”" }));
    expect(openTab).toHaveBeenCalledWith("docs");
  });

  it("holds still under reduced motion", () => {
    const world = office({
      runners: [runner("rn1", "mac-mini", 1)],
      runs: [makeRun({ id: "r1", runnerId: "rn1", status: "running" })],
    });
    const { container, unmount } = mount(world);
    expect(container.querySelector(".px-office")).toHaveAttribute("data-motion", "full");
    unmount();
    reducedMotion(true);
    const again = mount(world);
    expect(again.container.querySelector(".px-office")).toHaveAttribute("data-motion", "reduced");
  });

  it("says the same rooms in words for a screen reader", () => {
    const world = office({
      runners: [runner("rn1", "mac-mini", 3), runner("rn2", "old-box", 1, false)],
      runs: [makeRun({ id: "r1", runnerId: "rn1", nodeId: "login", status: "running" })],
    });
    mount(world);
    const text = screen.getByTestId("war-room-text");
    expect(within(text).getByRole("heading", { name: "Room mac-mini (Online)" })).toBeInTheDocument();
    expect(within(text).getByRole("heading", { name: "Room old-box (Offline)" })).toBeInTheDocument();
    expect(within(text).getByText(/: working, on “Login form”\./)).toBeInTheDocument();
    expect(within(text).getByText("2 free desk(s).")).toBeInTheDocument();
    expect(within(text).getByText("1 free desk(s).")).toBeInTheDocument();
  });

  it("asks to turn the workstation on when it is hidden", () => {
    useApp.setState({ preferences: { ...useApp.getState().preferences, agentWorkstation: false } });
    mount(office({ runners: [runner("rn1", "mac-mini", 1)] }));
    expect(screen.queryByRole("region", { name: "Room mac-mini" })).not.toBeInTheDocument();
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
