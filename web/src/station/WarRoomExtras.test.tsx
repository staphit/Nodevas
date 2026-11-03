import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import * as analysis from "../analysis";
import { readPreferences } from "../preferences";
import { useApp } from "../store";
import type { Graph } from "../types";
import { WarRoomExtras } from "./WarRoomExtras";

vi.mock("../analysis", async (original) => {
  const actual = await original<typeof import("../analysis")>();
  return { ...actual, analyzeGraph: vi.fn(actual.analyzeGraph) };
});

// A hand-written graph.yaml can say `requires: plan` with no wire drawn. The
// board reads the graph as wires plus every node a requires expression
// names; the war room's waiting list must read the same dependency graph,
// not the raw file, so the two never tell different stories.
const GRAPH: Graph = {
  version: 1,
  nodes: [
    { id: "plan", title: "Plan" },
    { id: "build", title: "Build", requires: "plan" },
  ],
  edges: [],
  ui: {},
} as unknown as Graph;

beforeEach(() => {
  vi.mocked(analysis.analyzeGraph).mockClear();
  useApp.setState({
    preferences: { ...readPreferences(), language: "en" },
    graph: GRAPH,
    statuses: { plan: "ready", build: "ready" },
  });
});

describe("WarRoomExtras waiting tasks", () => {
  it("analyses the graph with its requires-only links drawn in", () => {
    render(<WarRoomExtras live={[]} ended={[]} pilots={[]} now={0} titleOf={(id) => id} />);
    expect(screen.getByRole("button", { name: /1 tasks waiting on prerequisites/ })).toBeInTheDocument();
    const analysed = vi.mocked(analysis.analyzeGraph).mock.calls.at(-1)?.[0];
    expect(analysed?.edges).toEqual([{ from: "plan", to: "build" }]);
  });
});
