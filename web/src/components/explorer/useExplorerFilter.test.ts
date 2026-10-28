import { renderHook } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { useExplorerFilter } from "./useExplorerFilter";
import type { Graph } from "../../types";

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
