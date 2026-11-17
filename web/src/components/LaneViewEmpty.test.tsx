import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api, type Actor } from "../api";
import { translate } from "../i18n";
import { readPreferences } from "../preferences";
import { EditionProvider, type Edition } from "../runners/edition";
import { useStation } from "../station/stationStore";
import { useApp } from "../store";
import { LaneView } from "./LaneView";
import { OPEN_START_SCREEN_EVENT } from "./StartScreen";

vi.mock("../api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api")>();
  return {
    ...actual,
    api: { ...actual.api, getState: vi.fn() },
  };
});

let viewer: Actor | null = null;
vi.mock("./SignIn", async (original) => ({
  ...(await original<typeof import("./SignIn")>()),
  useViewer: () => viewer,
}));

const paneProps = {
  collapsed: false,
  onToggle: () => undefined,
  selectedNodeId: null,
  onSelectedNodeChange: () => undefined,
  keyboardActive: false,
  onActivate: () => undefined,
};

const example = () => translate("lane.emptyExample", "en");

beforeEach(() => {
  viewer = null;
  vi.mocked(api.getState).mockResolvedValue({ state: { nodes: {}, history: [] }, statuses: {} });
  useApp.setState({
    preferences: { ...readPreferences(), language: "en" },
    graph: { version: 1, nodes: [], edges: [], ui: {} },
    issues: [],
  });
});

afterEach(() => {
  vi.restoreAllMocks();
});

// An empty board said "create the first node" and nothing about what a node
// is for, and someone who would rather look at a finished board first had to
// know the start screen could be brought back.
describe("the empty board", () => {
  it("says what a node is for, keeps Create node first, and offers an example", async () => {
    const user = userEvent.setup();
    const opened = vi.fn();
    window.addEventListener(OPEN_START_SCREEN_EVENT, opened);
    try {
      render(<LaneView variant="graph" {...paneProps} />);
      expect(screen.getByText(translate("lane.emptyDescription", "en"))).toBeInTheDocument();
      const buttons = screen
        .getByText(translate("lane.emptyTitle", "en"))
        .closest(".ui-empty-state")!
        .querySelectorAll("button");
      expect([...buttons].map((button) => button.textContent)).toEqual([
        translate("nodeCreate.create", "en"),
        example(),
      ]);

      await user.click(screen.getByRole("button", { name: example() }));
      expect(opened).toHaveBeenCalledTimes(1);
    } finally {
      window.removeEventListener(OPEN_START_SCREEN_EVENT, opened);
    }
  });

  // Opening an example makes a project, which on a team server is an
  // administrator's call: the start screen offers itself by the same rule.
  it("offers the example to an administrator and not to a member", () => {
    viewer = { id: "u1", name: "Ada", role: "admin" };
    const { unmount } = render(<LaneView variant="graph" {...paneProps} />);
    expect(screen.getByRole("button", { name: example() })).toBeInTheDocument();
    unmount();

    viewer = { id: "u2", name: "Bo", role: "member" };
    render(<LaneView variant="graph" {...paneProps} />);
    expect(screen.queryByRole("button", { name: example() })).toBeNull();
    expect(screen.getByRole("button", { name: translate("nodeCreate.create", "en") })).toBeInTheDocument();
  });
});

// Single-user edition: someone who uses agents can hand the work to one
// straight from the empty board. A plain planner (no workstation) and a team
// hub see the board as before.
describe("the empty board's Ask an agent", () => {
  const ask = () => translate("lane.emptyAskAgent", "en");

  function board(edition: Edition, workstation: boolean) {
    useApp.setState((state) => ({
      preferences: { ...state.preferences, agentWorkstation: workstation, projectMode: "info" },
    }));
    return render(
      <EditionProvider edition={edition}>
        <LaneView variant="graph" {...paneProps} />
      </EditionProvider>,
    );
  }

  it("opens Station's global tab when the workstation is shown", async () => {
    const user = userEvent.setup();
    board("single", true);
    await user.click(screen.getByRole("button", { name: ask() }));
    expect(useApp.getState().preferences.projectMode).toBe("station");
    expect(useStation.getState().tab).toBe("global");
  });

  it("is not offered to a plain planner", () => {
    board("single", false);
    expect(screen.queryByRole("button", { name: ask() })).toBeNull();
  });

  it("is not offered on a team hub", () => {
    board("team", true);
    expect(screen.queryByRole("button", { name: ask() })).toBeNull();
  });
});
