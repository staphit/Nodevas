import { render } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "../../api";
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
