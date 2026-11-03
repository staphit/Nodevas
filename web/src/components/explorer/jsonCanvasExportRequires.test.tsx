import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "../../api";
import { useApp } from "../../store";
import type { Graph } from "../../types";
import { useProjectTransfer } from "./useProjectTransfer";

vi.mock("../ConfirmDialog", () => ({
  confirmAction: vi.fn(async () => true),
}));

// A hand-written graph.yaml may state `requires: n0001` with no `edges:` entry.
// The board draws that wire (requiresProjection.ts); the JSON Canvas export
// must carry it too, or the exported file silently loses a dependency.
const GRAPH: Graph = {
  nodes: [
    { id: "n0001", title: "One" },
    { id: "n0002", title: "Two", requires: "n0001" },
    { id: "n0003", title: "Three", requires: "n0002 and n0001" },
  ],
  edges: [{ from: "n0001", to: "n0002" }],
} as Graph;

let blobs: Blob[] = [];

beforeEach(() => {
  blobs = [];
  useApp.setState({
    activeProject: "daily",
    projects: [{ name: "daily", label: "daily", depth: 0, path: "daily", nodes: 3 }],
  });
  vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => undefined);
  Object.assign(URL, {
    createObjectURL: vi.fn((blob: Blob) => {
      blobs.push(blob);
      return "blob:canvas";
    }),
    revokeObjectURL: vi.fn(),
  });
  vi.spyOn(api, "getNode").mockRejectedValue(new Error("offline"));
});

afterEach(() => {
  vi.restoreAllMocks();
});

function readBlob(blob: Blob): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result));
    reader.onerror = () => reject(reader.error);
    reader.readAsText(blob);
  });
}

describe("exporting a JSON Canvas", () => {
  it("includes the dependencies only `requires` states, as the board draws them", async () => {
    const { result } = renderHook(() =>
      useProjectTransfer({
        graph: GRAPH,
        activeProject: "daily",
        expandedProjects: new Set(),
        persistExpandedProjects: () => undefined,
        setProjectTransferNotice: () => undefined,
      }),
    );
    await act(async () => {
      await result.current.exportJSONCanvas();
    });
    expect(blobs).toHaveLength(1);
    const canvas = JSON.parse(await readBlob(blobs[0])) as {
      edges: Array<{ id: string; fromNode: string; toNode: string }>;
    };
    const pairs = canvas.edges.map((edge) => `${edge.fromNode}>${edge.toNode}`).sort();
    expect(pairs).toEqual(["n0001>n0002", "n0001>n0003", "n0002>n0003"]);
    // Every edge keeps a distinct id.
    expect(new Set(canvas.edges.map((edge) => edge.id)).size).toBe(canvas.edges.length);
  });
});
