// The war room's empty states in the single-user edition: no runner to add
// and no "Agents and runners" section to point at, so neither is named.

import { render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { api } from "../../api";
import { readPreferences } from "../../preferences";
import { EditionProvider, type Edition } from "../../runners/edition";
import { useRunners } from "../../runners/runnerStore";
import { useApp } from "../../store";
import { useThemePacks } from "../../theme-pack/themePackStore";
import { buildBuilding } from "../building";
import { GameActionsProvider } from "../GameActions";
import { NOW } from "../testFixtures";
import WarRoomStage from "./WarRoom";

/** A workspace with no project open and no global agent: no room to draw. */
const nothing = buildBuilding({
  graph: null,
  runs: [],
  runners: [],
  sessions: [],
  asks: [],
  held: [],
  messages: [],
  now: NOW,
  activeProject: "",
  projectLabel: "",
  remote: null,
  waitingItems: [],
});

function mount(edition: Edition) {
  return render(
    <EditionProvider edition={edition}>
      <GameActionsProvider>
        <WarRoomStage building={nothing} titleOf={() => ""} rowOf={() => undefined} />
      </GameActionsProvider>
    </EditionProvider>,
  );
}

beforeEach(() => {
  window.matchMedia = vi.fn().mockReturnValue({ matches: false, addEventListener: vi.fn(), removeEventListener: vi.fn() });
  vi.spyOn(api, "listThemePacks").mockResolvedValue({ packs: [], default: "" });
  useThemePacks.getState().reset();
  useRunners.setState({ runners: [] });
  useApp.setState({
    activeProject: "",
    preferences: { ...readPreferences(), language: "en", agentWorkstation: true },
  });
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe("the war room with no room to draw", () => {
  it("says how an agent gets here, without a runner to add, in the single-user edition", () => {
    const { container } = mount("single");
    expect(screen.getByText("No agents yet.")).toBeInTheDocument();
    expect(screen.getByText("Send a task to an agent and its office fills up here.")).toBeInTheDocument();
    expect(container.textContent).not.toMatch(/runner/i);
  });

  it("keeps a team hub's words about runners", () => {
    mount("team");
    expect(screen.getByText("No runners or agents yet.")).toBeInTheDocument();
    expect(screen.getByText(/Add a runner \(Settings → Agents\)/)).toBeInTheDocument();
  });
});

describe("the war room while the workstation is hidden", () => {
  beforeEach(() => {
    useApp.setState({ preferences: { ...useApp.getState().preferences, agentWorkstation: false } });
  });

  it("names the switch, not a Settings section the single-user edition does not have", () => {
    mount("single");
    const status = screen.getByRole("status");
    expect(status).toHaveTextContent("Agent activity shows the agents working on this computer.");
    expect(status).toHaveTextContent("Turn it on here, with “Let agents work here” in Settings");
    expect(status.textContent).not.toMatch(/runner|Settings →/i);
    expect(screen.getByRole("button", { name: "Enable agent workstation" })).toBeInTheDocument();
  });

  it("keeps a team hub's words", () => {
    mount("team");
    expect(screen.getByRole("status")).toHaveTextContent("Agent activity shows your agent workstation");
  });
});
