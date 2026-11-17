import { render, screen } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { readPreferences } from "../preferences";
import { EditionProvider } from "../runners/edition";
import { useBudget } from "../runs/budgetStore";
import { useApp } from "../store";
import { useBuilding } from "./buildingStore";
import { useGlobalAgents } from "./globalAgentsStore";
import { useStation } from "./stationStore";
import StationView from "./StationView";

// Station's parts are tested on their own; here only where the describe box goes.
vi.mock("./SimpleAgentBox", () => ({ SimpleAgentBox: () => <div data-testid="simple-box" /> }));
vi.mock("./StationWaiting", () => ({ StationWaiting: () => <div data-testid="station-waiting" /> }));
vi.mock("./StationWarRoom", () => ({ StationWarRoom: () => null }));
vi.mock("./StationSide", () => ({ StationSide: () => null }));
vi.mock("./StationControl", () => ({ StationControlReport: () => null, StationPauseButton: () => null }));
vi.mock("./GlobalAgentsPanel", () => ({ GlobalAgentsPanel: () => <div data-testid="global-panel" /> }));
vi.mock("./StationTabs", () => ({
  STATION_PANEL_ID: "station-tabpanel",
  stationTabId: (tab: string) => `station-tab-${tab}`,
  StationTabs: () => null,
}));
vi.mock("../autopilot/AutopilotPanel", () => ({ AutopilotPanel: () => null }));
vi.mock("../game/useWorld", () => ({ useWorld: () => null }));
vi.mock("../game/GameActions", () => ({
  GameActionsProvider: ({ children }: { children: ReactNode }) => <>{children}</>,
  useGameActions: () => ({ dispatch: () => {} }),
}));

function renderStation(edition: "single" | "team", experience: "simple" | "full", tab: "project" | "global") {
  useApp.setState({ preferences: { ...readPreferences(), language: "en", experience } });
  useStation.setState({ tab });
  return render(
    <EditionProvider edition={edition}>
      <StationView drawer={null} />
    </EditionProvider>,
  );
}

beforeEach(() => {
  // No loads: the stores only watch.
  vi.spyOn(useStation.getState(), "setWatching").mockImplementation(() => {});
  vi.spyOn(useStation.getState(), "openProject").mockImplementation(() => {});
  vi.spyOn(useBuilding.getState(), "setWatching").mockImplementation(() => {});
  vi.spyOn(useGlobalAgents.getState(), "setWatching").mockImplementation(() => {});
  vi.spyOn(useBudget.getState(), "load").mockResolvedValue(undefined as never);
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe("StationView in Simple", () => {
  it("puts the describe box above what waits on you on the project tab", () => {
    renderStation("single", "simple", "project");
    const box = screen.getByTestId("simple-box");
    const waiting = screen.getByTestId("station-waiting");
    expect(box.compareDocumentPosition(waiting) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  it.each([
    ["single", "full"],
    ["team", "simple"],
    ["team", "full"],
  ] as const)("leaves the project tab as it is in %s with %s", (edition, experience) => {
    renderStation(edition, experience, "project");
    expect(screen.queryByTestId("simple-box")).not.toBeInTheDocument();
    expect(screen.getByTestId("station-waiting")).toBeInTheDocument();
  });

  it("leaves the global tab to its own panel", () => {
    renderStation("single", "simple", "global");
    expect(screen.queryByTestId("simple-box")).not.toBeInTheDocument();
    expect(screen.getByTestId("global-panel")).toBeInTheDocument();
  });
});
