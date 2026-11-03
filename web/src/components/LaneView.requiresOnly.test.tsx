import { act, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "../api";
import { translate } from "../i18n";
import { readPreferences } from "../preferences";
import { useApp } from "../store";
import type { Graph, RunState } from "../types";
import { LaneView } from "./LaneView";

vi.mock("../api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api")>();
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

const EMPTY_RUN: RunState = { nodes: {}, history: [] };

// A hand-written graph.yaml: the dependencies are only in `requires`, with no
// `edges:` block. The expression is the executable condition and the wires
// are its visual projection (docs/concepts.md), so the board draws them.
const REQUIRES_ONLY: Graph = {
  version: 1,
  nodes: [
    { id: "n0001", title: "Card 1" },
    { id: "n0002", title: "Card 2", requires: "n0001" },
    { id: "n0003", title: "Card 3", requires: "n0001" },
  ],
  ui: {},
};

const paneProps = {
  collapsed: false,
  onToggle: () => undefined,
  selectedNodeId: null,
  onSelectedNodeChange: () => undefined,
  keyboardActive: false,
  onActivate: () => undefined,
};

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(api.getState).mockResolvedValue({ state: EMPTY_RUN, statuses: {} });
  useApp.setState({
    preferences: { ...readPreferences(), language: "en" },
    graph: REQUIRES_ONLY,
    graphRev: "rev-1",
    runState: EMPTY_RUN,
    statuses: {},
    issues: [],
    operations: {},
    error: null,
    tabs: [],
    activeTab: null,
  });
  useApp.getState().updateUIPreference("language", "en");
});

function cardTooltip(container: HTMLElement, id: string): string {
  const card = container.querySelector<HTMLElement>(`.col-card[data-node-id="${id}"]`);
  expect(card).not.toBeNull();
  return card!.querySelector<HTMLElement>("[title]")?.title ?? card!.title;
}

describe("a board whose dependencies live only in requires", () => {
  it("draws the wires the expressions name", () => {
    const { container } = render(<LaneView variant="graph" {...paneProps} />);
    expect(container.querySelector('[data-edge-key="n0001->n0002"]')).not.toBeNull();
    expect(container.querySelector('[data-edge-key="n0001->n0003"]')).not.toBeNull();
  });

  it("does not call a node a start node when its requires names another", async () => {
    const { container } = render(<LaneView variant="graph" {...paneProps} />);
    const start = translate("canvas.entry.auto", "en");
    expect(cardTooltip(container, "n0001")).toContain(start);
    expect(cardTooltip(container, "n0002")).not.toContain(start);
    expect(cardTooltip(container, "n0003")).not.toContain(start);

    const user = userEvent.setup();
    await act(async () => {
      await user.click(screen.getByRole("button", { name: /^Problems/ }));
    });
    const panel = screen.getByRole("complementary", { name: translate("board.problems", "en") });
    expect(within(panel).getByText(translate("analysis.entry", "en", { count: "1" }))).toBeTruthy();
  });
});
