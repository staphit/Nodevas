import { renderHook } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { useExplorerFilter } from "./useExplorerFilter";
import type { ProjectEntry } from "../../state/types";
import type { Graph } from "../../types";
import { useApp } from "../../store";

describe("useExplorerFilter node order", () => {
  it("sorts node titles in natural order, so Card 2 follows Card 1", () => {
    const titles = ["Card 1000", "Card 10", "Card 2", "Card 122", "Card 1219", "Card 1", "Card 1220", "Card 0"];
    const graph: Graph = {
      version: 1,
      nodes: titles.map((title, index) => ({ id: `node-${index}`, title })),
      edges: [],
    };
    const { result } = renderHook(() =>
      useExplorerFilter(graph, [], "", new Map(), new Set()),
    );
    expect(result.current.activeNodes.map((node) => node.title)).toEqual([
      "Card 0",
      "Card 1",
      "Card 2",
      "Card 10",
      "Card 122",
      "Card 1000",
      "Card 1219",
      "Card 1220",
    ]);
  });
});

describe("useExplorerFilter project node count", () => {
  const project: ProjectEntry = { name: "board", label: "board", depth: 0, path: "board", nodes: 0 };
  const graph: Graph = {
    version: 1,
    nodes: [{ id: "a" }, { id: "b" }, { id: "c" }],
    edges: [],
  };

  afterEach(() => useApp.setState({ graphProject: "" }));

  it("follows the graph on screen, not the count fetched with the project list", () => {
    useApp.setState({ graphProject: "board" });
    const { result } = renderHook(() =>
      useExplorerFilter(graph, [project], "board", new Map([["board", project]]), new Set()),
    );
    expect(result.current.visibleProjects[0].nodes).toBe(3);
  });

  it("keeps the listed count while the graph on screen is still the previous project's", () => {
    useApp.setState({ graphProject: "previous" });
    const { result } = renderHook(() =>
      useExplorerFilter(graph, [project], "board", new Map([["board", project]]), new Set()),
    );
    expect(result.current.visibleProjects[0].nodes).toBe(0);
  });
});
