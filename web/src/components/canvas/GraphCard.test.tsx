import { render } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "../../api";
import { translate } from "../../i18n";
import { useApp } from "../../store";
import { LaneView } from "../LaneView";
import { installPointerShims, resetBoardStore } from "./boardTestKit";

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
    [...text].reduce((sum, ch) => sum + (/[　-鿿＀-￯]/.test(ch) ? 1 : 0.56) * fontPx, 0);
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
