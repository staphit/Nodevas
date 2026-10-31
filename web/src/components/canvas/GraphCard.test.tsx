import { render, screen } from "@testing-library/react";
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

beforeEach(() => {
  installPointerShims();
  vi.mocked(api.getState).mockResolvedValue({ state: { nodes: {}, history: [] }, statuses: {} });
  resetBoardStore([]);
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
