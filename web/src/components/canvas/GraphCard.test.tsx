import { act, fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "../../api";
import { useAutopilot } from "../../autopilot/autopilotStore";
import { nodeTypeById } from "../../domain/graph/nodeTypes";
import { translate } from "../../i18n";
import { useApp } from "../../store";
import { LaneView } from "../LaneView";
import { installPointerShims, resetBoardStore } from "./boardTestKit";
import { GraphCard, type CardEvents } from "./GraphCard";

vi.mock("../../api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../../api")>();
  return {
    ...actual,
    api: {
      putGraph: vi.fn(),
      getGraph: vi.fn(),
      getState: vi.fn(),
      getTrash: vi.fn(),
      createNode: vi.fn(),
    },
  };
});

const paneProps = {
  collapsed: false,
  onToggle: () => undefined,
  selectedNodeId: null,
  onSelectedNodeChange: () => undefined,
  keyboardActive: false,
  onActivate: () => undefined,
};

// One test stubs it to see the command a drag sends; the rest get it back.
const realUpdateNode = useApp.getState().updateNode;

beforeEach(() => {
  installPointerShims();
  vi.mocked(api.getState).mockResolvedValue({ state: { nodes: {}, history: [] }, statuses: {} });
  resetBoardStore([]);
  useApp.setState({ updateNode: realUpdateNode });
  useApp.getState().updateUIPreference("graphConnectTipDismissed", false);
  useAutopilot.setState({ marks: {} });
});

const noop = () => undefined;
const cardEvents: CardEvents = {
  pointerDown: noop,
  pointerMove: noop,
  pointerUp: noop,
  pointerCancel: noop,
  pointerEnter: noop,
  pointerLeave: noop,
  lostPointerCapture: noop,
  focus: noop,
  blur: noop,
  keyDown: noop,
  contextMenu: noop,
  moreClick: noop,
  connectPointerDown: noop,
  connectPointerMove: noop,
  connectPointerUp: noop,
  connectPointerCancel: noop,
  resizePointerDown: noop,
  resizePointerMove: noop,
  resizePointerUp: noop,
  resizePointerCancel: noop,
  resizeKeyDown: noop,
};

// A phone's board starts far enough out to draw its cards at the minimal
// level: the card still wears its autopilot badge, as the panel the badge
// opens is reachable from the same board.
describe("GraphCard minimal detail", () => {
  it("keeps the autopilot badge on a task with an autopilot mark", () => {
    useApp.getState().updateUIPreference("language", "zh-TW");
    useAutopilot.setState({
      marks: {
        task: {
          autopilotId: "ap1",
          goalNodeId: "goal",
          state: "waiting_human",
          stage: "human_review",
        },
      },
    });

    render(
      <GraphCard
        node={{ id: "task", title: "Task" }}
        detail="minimal"
        left={0}
        top={0}
        width={152}
        height={72}
        className="col-card status-ready"
        cardBg={undefined}
        borderColor={undefined}
        typeColor={undefined}
        textColor={undefined}
        dragging={false}
        hasStatus
        status="ready"
        customStatuses={[]}
        nodeType={nodeTypeById(null, "task")}
        assigned=""
        markerKind={undefined}
        markerLabel={undefined}
        agentName={undefined}
        agentKind={undefined}
        ariaLabel="Task"
        tooltip="Task"
        selected={false}
        events={cardEvents}
      />,
    );

    const label = translate("autopilot.cardBadge", "zh-TW", {
      state: translate("autopilot.state.waiting_human", "zh-TW"),
      stage: translate("autopilot.stage.human_review", "zh-TW"),
    });
    expect(screen.getByLabelText(label)).toBeInTheDocument();
  });
});

function twoCards() {
  useApp.setState({
    graph: {
      version: 1,
      nodes: [
        { id: "first", title: "First" },
        { id: "second", title: "Second" },
      ],
      edges: [],
      ui: { positions: { first: { x: 0, y: 0 }, second: { x: 2, y: 0 } } },
    },
  });
  const view = render(<LaneView variant="graph" {...paneProps} />);
  const card = (id: string) =>
    view.container.querySelector<HTMLElement>(`.col-card[data-node-id="${id}"]`)!;
  return { ...view, card };
}

// A selected card showed eight squares and nothing else, and a beginner
// dragged the one on its right edge to draw a line: it resized the card over
// its neighbour. Connecting had no handle at all, only Alt+drag. The round
// dot beside that square is the handle.
describe("GraphCard connect dot", () => {
  it("says in words what dragging it does", () => {
    const { card } = twoCards();
    const dot = screen.getByRole("img", { name: "Connect First to another card" });
    expect(dot).toHaveClass("col-card-connect");
    expect(dot).toHaveAttribute(
      "title",
      "Drag onto another card: that card will wait for this one",
    );
    // A pointer's handle: the keyboard connects through the card's menu.
    expect(dot).not.toHaveAttribute("tabindex");
    // The card's sibling, so no control sits inside the card's own button.
    expect(card("first").contains(dot)).toBe(false);
  });

  it("leaves the eight resize squares where they were", async () => {
    const { card, container } = twoCards();
    fireEvent.focus(card("first"));
    await act(async () => undefined);
    expect(container.querySelectorAll(".card-resize-frame .col-card-handle")).toHaveLength(8);
    expect(container.querySelector(".card-resize-frame .col-card-handle.e")).not.toBeNull();
  });

  it("is in Chinese for a Chinese reader", () => {
    useApp.getState().updateUIPreference("language", "zh-TW");
    twoCards();
    const dot = screen.getByRole("img", {
      name: translate("canvas.connectPort", "zh-TW", { title: "First" }),
    });
    expect(dot).toHaveAttribute("title", translate("canvas.connectPortTitle", "zh-TW"));
  });

  // The dot is dragged from "first" onto "second": the card it lands on is
  // the one that waits, as the tooltip says.
  it("draws a dependency to the card it is dropped on, with no key held", async () => {
    const updateNode = vi.fn(async () => ({ ok: true }) as never);
    useApp.setState({ updateNode });
    const { card } = twoCards();
    Object.defineProperty(document, "elementFromPoint", {
      configurable: true,
      value: () => card("second"),
    });
    const dot = screen.getByRole("img", { name: "Connect First to another card" });

    fireEvent.pointerDown(dot, { button: 0, pointerId: 1, clientX: 10, clientY: 10 });
    expect(card("first")).toHaveClass("connection-source");
    fireEvent.pointerMove(dot, { pointerId: 1, clientX: 60, clientY: 10 });
    expect(card("second")).toHaveClass("connection-target");
    fireEvent.pointerUp(dot, { pointerId: 1, clientX: 60, clientY: 10 });
    await act(async () => undefined);

    expect(updateNode).toHaveBeenCalledWith({
      type: "node.addDependency",
      sourceId: "first",
      targetId: "second",
    });
    expect(card("first")).not.toHaveClass("connection-source");
    // The board's tip on how to connect has done its job.
    expect(useApp.getState().preferences.graphConnectTipDismissed).toBe(true);
  });

  // The dot sits a few pixels from its own card, so a drag that ends back on
  // it is a change of mind, not a request to make the card wait for itself.
  it("lets go quietly when it is dropped back on its own card", async () => {
    const updateNode = vi.fn(async () => ({ ok: true }) as never);
    useApp.setState({ updateNode });
    const { card } = twoCards();
    Object.defineProperty(document, "elementFromPoint", {
      configurable: true,
      value: () => card("first"),
    });
    const dot = screen.getByRole("img", { name: "Connect First to another card" });

    fireEvent.pointerDown(dot, { button: 0, pointerId: 1, clientX: 10, clientY: 10 });
    fireEvent.pointerMove(dot, { pointerId: 1, clientX: 30, clientY: 10 });
    expect(card("first")).not.toHaveClass("connection-target");
    fireEvent.pointerUp(dot, { pointerId: 1, clientX: 30, clientY: 10 });
    await act(async () => undefined);

    expect(updateNode).not.toHaveBeenCalled();
    expect(screen.queryByText(translate("dependency.error.self", "en"))).toBeNull();
  });

  it("does not let the board read a press on it as a pan or a deselect", async () => {
    const { card } = twoCards();
    fireEvent.focus(card("first"));
    await act(async () => undefined);
    expect(card("first")).toHaveClass("selected");
    const dot = screen.getByRole("img", { name: "Connect First to another card" });
    fireEvent.pointerDown(dot, { button: 0, pointerId: 1, clientX: 10, clientY: 10 });
    fireEvent.pointerUp(dot, { pointerId: 1, clientX: 10, clientY: 10 });
    expect(card("first")).toHaveClass("selected");
  });

  // A phone's board opens zoomed out to the minimal level, and a finger has
  // no Alt: the dot is the one way it has to draw a line by hand.
  it("is drawn at every level of detail", () => {
    render(
      <GraphCard
        node={{ id: "task", title: "Task" }}
        detail="minimal"
        left={0}
        top={0}
        width={152}
        height={72}
        className="col-card status-ready"
        cardBg={undefined}
        borderColor={undefined}
        typeColor={undefined}
        textColor={undefined}
        dragging={false}
        hasStatus
        status="ready"
        customStatuses={[]}
        nodeType={nodeTypeById(null, "task")}
        assigned=""
        markerKind={undefined}
        markerLabel={undefined}
        agentName={undefined}
        agentKind={undefined}
        ariaLabel="Task"
        tooltip="Task"
        selected={false}
        events={cardEvents}
      />,
    );
    expect(screen.getByRole("img", { name: "Connect Task to another card" })).toHaveClass(
      "col-card-connect",
    );
  });
});

// The ⋯ button stopped every key, so with the keyboard left on it — as it is
// after its menu closes — Ctrl/⌘+Z never reached the window and undo did
// nothing. Only its own click keys stay with it.
describe("GraphCard menu button", () => {
  it("lets undo and Escape through and keeps Enter and Space", () => {
    twoCards();
    const more = screen.getByRole("button", { name: "Node actions: First" });
    const heard: string[] = [];
    const onKeyDown = (event: KeyboardEvent) => heard.push(event.key);
    window.addEventListener("keydown", onKeyDown);
    try {
      fireEvent.keyDown(more, { key: "z", ctrlKey: true });
      fireEvent.keyDown(more, { key: "z", metaKey: true });
      fireEvent.keyDown(more, { key: "Escape" });
      fireEvent.keyDown(more, { key: "Enter" });
      fireEvent.keyDown(more, { key: " " });
    } finally {
      window.removeEventListener("keydown", onKeyDown);
    }
    expect(heard).toEqual(["z", "z", "Escape"]);
  });
});

// A 152px card's second line holds the kind chip, the assignee and at most one
// marker. With the marker there, "Assignee: " used all of the room the line
// had left, so the name itself never showed: "Assignee:…", "負責人：尚…".
describe("GraphCard assignee line", () => {
  it("drops the label, not the name, when a marker shares the line", () => {
    useApp.setState({
      graph: {
        version: 1,
        nodes: [
          { id: "first", title: "First", assignee: "alex" },
          { id: "second", title: "Second", assignee: "alex" },
        ],
        edges: [{ from: "first", to: "second" }],
        users: [{ id: "alex", name: "Alex" }],
        ui: { positions: { first: { x: 0, y: 0 }, second: { x: 2, y: 0 } } },
      },
      statuses: { first: "done", second: "ready" },
    });
    const { container } = render(<LaneView variant="graph" {...paneProps} />);
    const card = (id: string) =>
      container.querySelector<HTMLElement>(`.col-card[data-node-id="${id}"]`)!;
    const assignee = (id: string) => card(id).querySelector<HTMLElement>(".col-card-assignee")!;

    // "first" starts the chain, so it carries the Start marker.
    expect(card("first").querySelector(".col-card-marker")).not.toBeNull();
    expect(assignee("first")).toHaveTextContent("Alex");
    expect(assignee("first")).not.toHaveAttribute("data-assignee-prefix");

    // "second" has nothing to wait on and blocks nothing: no marker, room for the label.
    expect(card("second").querySelector(".col-card-marker")).toBeNull();
    expect(assignee("second")).toHaveAttribute("data-assignee-prefix", "Assignee: ");
  });
});

// "Unassigned" beside a marker was cut to "Unassign…" on a default 152px card
// (Task 30px + Unassigned 49px + Blocking 45px + two 6px gaps against the
// line's 134px). Beside a marker the card says it in a short form, and the
// line's three words fit in both languages.
describe("GraphCard unassigned label", () => {
  it("uses the short form beside a marker and the full one alone", () => {
    useApp.getState().updateUIPreference("language", "en");
    useApp.setState({
      graph: {
        version: 1,
        nodes: [
          { id: "first", title: "First" },
          { id: "second", title: "Second" },
        ],
        edges: [{ from: "first", to: "second" }],
        ui: { positions: { first: { x: 0, y: 0 }, second: { x: 2, y: 0 } } },
      },
      statuses: { first: "done", second: "ready" },
    });
    const { container } = render(<LaneView variant="graph" {...paneProps} />);
    const assignee = (id: string) =>
      container.querySelector<HTMLElement>(`.col-card[data-node-id="${id}"] .col-card-assignee`)!;
    expect(container.querySelector('.col-card[data-node-id="first"] .col-card-marker')).not.toBeNull();
    expect(assignee("first")).toHaveTextContent(translate("canvas.unassignedShort", "en"));
    expect(assignee("second")).toHaveTextContent("Unassigned");
    // The tooltip still says it in full.
    expect(
      container.querySelector('.col-card[data-node-id="first"]')!.getAttribute("title"),
    ).toContain("Unassigned");
  });

  // jsdom has no text layout, so widths are estimated on the generous side:
  // a Latin glyph at 0.56em (measured: "Unassigned" at 9.5px is 49px, 0.52em),
  // a CJK one at a full em.
  const width = (text: string, fontPx: number) =>
    [...text].reduce((sum, ch) => sum + (/[\u3000-\u9fff\uff00-\uffef]/.test(ch) ? 1 : 0.56) * fontPx, 0);
  const LINE = 152 - 2 * 8 - 2; // card width less its padding and borders
  const GAP = 6;

  for (const language of ["en", "zh-TW"] as const) {
    it(`fits Task, the short form and any marker on a default card in ${language}`, () => {
      const type = width(translate("nodeType.task", language), 10) + 10 + 2;
      const label = width(translate("canvas.unassignedShort", language), 9.5);
      for (const key of ["board.card.waiting", "board.card.blocking", "canvas.entryLabel"]) {
        const marker = width(translate(key, language), 8.5) + 8 + 2;
        expect(type + GAP + label + GAP + marker, key).toBeLessThanOrEqual(LINE);
      }
    });
  }
});
