/**
 * A hand-written graph.yaml can name a prerequisite only in `requires`; the
 * board draws its wire from the expression (requiresProjection). Restyling
 * that wire from its context menu, or bending it, has to reach the hub — which
 * writes the explicit edge it stands for — and the board must go from the
 * drawn wire to the stored one without the wire vanishing in between or
 * being drawn twice.
 */
import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "../api";
import { localizedLineLabel, localizedRelationLabel } from "../i18n";
import { useApp } from "../store";
import type { Graph } from "../types";
import { installPointerShims, resetBoardStore } from "./canvas/boardTestKit";
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
      graphOps: vi.fn(),
    },
  };
});

// jsdom has no SVG geometry: a pointer lands where its client point says.
vi.mock("./canvas/geometry", async (importOriginal) => {
  const actual = await importOriginal<typeof import("./canvas/geometry")>();
  return {
    ...actual,
    svgPointerPoint: vi.fn((event: { clientX: number; clientY: number }) => ({
      x: event.clientX,
      y: event.clientY,
    })),
  };
});

const paneProps = {
  collapsed: false,
  onToggle: () => undefined,
  selectedNodeId: null as string | null,
  onSelectedNodeChange: () => undefined,
  keyboardActive: false,
  onActivate: () => undefined,
};

/** b waits for a, said only in its `requires`: no `edges:` entry. */
function requiresOnlyGraph(): Graph {
  return {
    version: 1,
    nodes: [
      { id: "a", title: "Alpha" },
      { id: "b", title: "Beta", requires: "a" },
    ],
    edges: [],
    ui: { positions: { a: { x: 0, y: 0 }, b: { x: 3, y: 0 } } },
  };
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}

const wires = (container: HTMLElement) =>
  container.querySelectorAll<SVGGElement>('.dependency-line[data-edge-key="a->b"]');

beforeEach(() => {
  installPointerShims();
  // EdgeMenu hands a failed apply to the browser's global reportError, which
  // jsdom lacks; a failure here shows in the assertions instead.
  vi.stubGlobal("reportError", vi.fn());
  vi.mocked(api.getState).mockResolvedValue({ state: { nodes: {}, history: [] }, statuses: {} });
  vi.mocked(api.putGraph).mockResolvedValue({ ok: true, rev: "rev-put", issues: [] });
  resetBoardStore([]);
  useApp.setState({ graph: requiresOnlyGraph(), graphRev: "rev-1" });
});

afterEach(() => {
  vi.clearAllMocks();
  vi.unstubAllGlobals();
});

async function openWireMenu(container: HTMLElement) {
  const hit = wires(container)[0]?.querySelector(".edge-hit-target");
  expect(hit).toBeTruthy();
  await act(async () => {
    fireEvent.contextMenu(hit!, { clientX: 200, clientY: 120 });
  });
}

describe("a wire drawn from a requires-only dependency", () => {
  it("is drawn once before anything is changed", () => {
    const { container } = render(<LaneView variant="graph" {...paneProps} />);
    expect(wires(container)).toHaveLength(1);
  });

  it("restyles from its context menu: the op goes out and the stored edge replaces the drawn one", async () => {
    const reply = deferred<Awaited<ReturnType<typeof api.graphOps>>>();
    vi.mocked(api.graphOps).mockReturnValue(reply.promise);
    const { container } = render(<LaneView variant="graph" {...paneProps} />);
    await openWireMenu(container);

    await act(async () => {
      fireEvent.click(screen.getByRole("menuitemradio", { name: new RegExp(localizedLineLabel("dashed")) }));
    });

    expect(api.graphOps).toHaveBeenCalledTimes(1);
    expect(vi.mocked(api.graphOps).mock.calls[0][0]).toEqual([
      expect.objectContaining({ kind: "set-edge-style", from: "a", to: "b", line: "dashed" }),
    ]);
    // Waiting on the hub: the wire stays, once.
    expect(wires(container)).toHaveLength(1);

    const stored: Graph = {
      ...requiresOnlyGraph(),
      edges: [{ from: "a", to: "b", line: "dashed" }],
    };
    await act(async () => {
      reply.resolve({ ok: true, graph: stored, rev: "rev-2", statuses: {}, issues: [] });
      await reply.promise;
    });
    expect(useApp.getState().graph?.edges).toEqual([{ from: "a", to: "b", line: "dashed" }]);
    expect(wires(container)).toHaveLength(1);
    expect(wires(container)[0].querySelector(".edge-wire")).toHaveClass("line-dashed");
    expect(api.putGraph).not.toHaveBeenCalled();
  });

  it("changes what it means from its context menu, and the condition follows on the hub", async () => {
    vi.mocked(api.graphOps).mockResolvedValue({
      ok: true,
      graph: {
        ...requiresOnlyGraph(),
        nodes: [
          { id: "a", title: "Alpha" },
          { id: "b", title: "Beta" },
        ],
        edges: [{ from: "a", to: "b", relation: "optional" }],
      },
      rev: "rev-2",
      statuses: {},
      issues: [],
    });
    const { container } = render(<LaneView variant="graph" {...paneProps} />);
    await openWireMenu(container);

    await act(async () => {
      fireEvent.click(
        screen.getByRole("menuitemradio", { name: new RegExp(localizedRelationLabel("optional")) }),
      );
    });

    expect(vi.mocked(api.graphOps).mock.calls[0][0]).toEqual([
      expect.objectContaining({ kind: "set-edge-style", from: "a", to: "b", relation: "optional" }),
    ]);
    expect(wires(container)).toHaveLength(1);
    expect(wires(container)[0].querySelector(".edge-wire")).toHaveClass("relation-optional");
  });

  it("keeps a bend put in it: the save carries the explicit edge, and the wire is not drawn twice", async () => {
    const saved = deferred<Awaited<ReturnType<typeof api.putGraph>>>();
    vi.mocked(api.putGraph).mockReturnValue(saved.promise);
    const { container } = render(<LaneView variant="graph" {...paneProps} />);
    const hit = wires(container)[0].querySelector(".edge-hit-target")!;
    const points = hit
      .getAttribute("points")!
      .trim()
      .split(/\s+/)
      .map((pair) => pair.split(",").map(Number));
    const [x1, y1] = points[0];
    const [x2, y2] = points[points.length - 1];
    const middle = { clientX: (x1 + x2) / 2, clientY: (y1 + y2) / 2 + 30 };

    await act(async () => {
      fireEvent.pointerDown(hit, { ...middle, altKey: true, button: 0, pointerId: 1 });
    });

    expect(api.putGraph).toHaveBeenCalledTimes(1);
    const put = vi.mocked(api.putGraph).mock.calls[0][0];
    expect(put.edges).toEqual([expect.objectContaining({ from: "a", to: "b" })]);
    expect(put.ui?.wireVertices?.["a->b"]).toHaveLength(1);
    // Shown straight away, with its bend, and once.
    expect(wires(container)).toHaveLength(1);
    expect(wires(container)[0].querySelectorAll(".edge-vertex")).toHaveLength(1);

    await act(async () => {
      saved.resolve({ ok: true, rev: "rev-2", issues: [] });
      await saved.promise;
    });
    expect(wires(container)).toHaveLength(1);
    expect(wires(container)[0].querySelectorAll(".edge-vertex")).toHaveLength(1);
  });
});
