import { render, screen } from "@testing-library/react";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it } from "vitest";
import { translate } from "../../i18n";
import { useApp } from "../../store";
import type { Graph, GraphNode } from "../../types";
import { ConnectTip, noteConnectionMade, wantsConnectTip } from "./ConnectTip";

function graph(nodes: GraphNode[], edges: Graph["edges"] = []): Graph {
  return { version: 1, nodes, edges, ui: {} };
}

const two = () =>
  graph([
    { id: "a", title: "Alpha" },
    { id: "b", title: "Bravo" },
  ]);

beforeEach(() => {
  useApp.getState().updateUIPreference("language", "en");
  useApp.getState().updateUIPreference("graphConnectTipDismissed", false);
});

// Two new nodes were met by a red Problems badge and nothing that said how to
// connect them. The tip is the one passive line that does.
describe("wantsConnectTip", () => {
  it("waits for a second node that can wait for the first", () => {
    expect(wantsConnectTip(null)).toBe(false);
    expect(wantsConnectTip(graph([]))).toBe(false);
    expect(wantsConnectTip(graph([{ id: "a", title: "Alpha" }]))).toBe(false);
    expect(wantsConnectTip(two())).toBe(true);
  });

  it("does not count a Note, which has no status and never waits", () => {
    expect(
      wantsConnectTip(
        graph([
          { id: "a", title: "Alpha" },
          { id: "n", title: "Notes", kind: "note" },
        ]),
      ),
    ).toBe(false);
  });

  it("has nothing to say once anything is connected", () => {
    expect(wantsConnectTip({ ...two(), edges: [{ from: "a", to: "b" }] })).toBe(false);
  });
});

describe("ConnectTip", () => {
  it("says how to connect two cards, in the reader's language", () => {
    const { unmount } = render(<ConnectTip graph={two()} />);
    expect(screen.getByRole("note")).toHaveTextContent(translate("canvas.connectTip", "en"));
    expect(screen.getByRole("note")).toHaveTextContent("round dot");
    unmount();

    useApp.getState().updateUIPreference("language", "zh-TW");
    render(<ConnectTip graph={two()} />);
    expect(screen.getByRole("note")).toHaveTextContent(translate("canvas.connectTip", "zh-TW"));
  });

  it("is not there for a graph it has nothing to say about", () => {
    render(<ConnectTip graph={graph([{ id: "a", title: "Alpha" }])} />);
    expect(screen.queryByRole("note")).toBeNull();
  });

  it("goes for good when it is closed", async () => {
    const user = userEvent.setup();
    const { unmount } = render(<ConnectTip graph={two()} />);
    await user.click(screen.getByRole("button", { name: "Hide this tip" }));
    expect(screen.queryByRole("note")).toBeNull();
    expect(useApp.getState().preferences.graphConnectTipDismissed).toBe(true);
    unmount();

    // Another project with two unconnected nodes does not bring it back.
    render(<ConnectTip graph={two()} />);
    expect(screen.queryByRole("note")).toBeNull();
  });

  // Recorded where the dependency is made, not read off the graph: opening an
  // example project that already has wires must not dismiss the tip unread.
  it("goes for good once this person has made a dependency", () => {
    const connected = { ...two(), edges: [{ from: "a", to: "b" }] };
    const { rerender } = render(<ConnectTip graph={connected} />);
    expect(screen.queryByRole("note")).toBeNull();
    expect(useApp.getState().preferences.graphConnectTipDismissed).toBe(false);
    rerender(<ConnectTip graph={two()} />);
    expect(screen.getByRole("note")).toBeInTheDocument();

    noteConnectionMade();
    rerender(<ConnectTip graph={two()} />);
    expect(screen.queryByRole("note")).toBeNull();
    expect(useApp.getState().preferences.graphConnectTipDismissed).toBe(true);
  });
});

// jsdom applies no CSS, so this reads the contract from the stylesheet. The
// tip lies over the board's corner, and a card or an empty spot under its
// words must still take a press; only the close button is the tip's own.
describe("ConnectTip's place on the board", () => {
  const css = readFileSync(resolve(process.cwd(), "src/styles/10a-canvas-tools.css"), "utf8");
  const body = (selector: string) =>
    new RegExp(`^${selector.replace(/[.]/g, "\\.")}\\s*\\{([^}]*)\\}`, "m").exec(css)?.[1] ?? "";

  it("lets presses through everywhere but its close button", () => {
    expect(body(".board-connect-tip")).toMatch(/pointer-events:\s*none/);
    expect(body(".board-connect-tip button")).toMatch(/pointer-events:\s*auto/);
  });
});
