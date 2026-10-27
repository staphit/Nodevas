import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { configurePreferenceStorage, readPreferences } from "../preferences";
import { useApp } from "../store";
import type { Graph } from "../types";
import { OPEN_START_SCREEN_EVENT, StartScreen, useNewProjectRequest } from "./StartScreen";

function fakeStorage(initial: Record<string, string> = {}) {
  const map = new Map(Object.entries(initial));
  return {
    map,
    getItem: (key: string) => map.get(key) ?? null,
    setItem: (key: string, value: string) => void map.set(key, value),
    removeItem: (key: string) => void map.delete(key),
  };
}

function graph(nodes: Graph["nodes"]): Graph {
  return { version: 1, nodes, edges: [], ui: {} };
}

/** What a server hands a browser for a workspace it just created: "main". */
function seedFreshWorkspace() {
  useApp.setState({
    graph: graph([]),
    projects: [{ name: "main", label: "main", depth: 0, path: "/ws/main", nodes: 0 }],
    activeProject: "main",
  });
}

beforeEach(() => {
  configurePreferenceStorage(fakeStorage());
  useApp.setState({
    preferences: { ...readPreferences(), language: "en" },
  });
  useNewProjectRequest.getState().set(false);
});

describe("StartScreen", () => {
  it("greets a brand-new workspace with a blank start and the examples", () => {
    seedFreshWorkspace();
    render(<StartScreen />);

    expect(screen.getByRole("dialog", { name: "Welcome to Nodevas" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Start with a blank project/ })).toBeInTheDocument();
    for (const title of ["Daily work schedule", "Novel writing", "AI harness engineering"]) {
      expect(screen.getByRole("button", { name: new RegExp(title) })).toBeInTheDocument();
    }
  });

  it("stays out of the way of a workspace that has work in it", () => {
    useApp.setState({
      graph: graph([{ id: "a", title: "A" }]),
      projects: [{ name: "main", label: "main", depth: 0, path: "/ws/main", nodes: 1 }],
    });
    const { container } = render(<StartScreen />);
    expect(container).toBeEmptyDOMElement();
  });

  it("starts blank by getting out of the way, and remembers it", async () => {
    seedFreshWorkspace();
    const user = userEvent.setup();
    render(<StartScreen />);

    await user.click(screen.getByRole("button", { name: /Start with a blank project/ }));

    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(useApp.getState().preferences.startScreenDismissed).toBe(true);
    // The blank project is already open; no second one is started.
    expect(useNewProjectRequest.getState().pending).toBe(false);
  });

  it("opens an example through the store and closes", async () => {
    seedFreshWorkspace();
    const openExample = vi.fn(async () => "novel-writing");
    useApp.setState({ openExample });
    const user = userEvent.setup();
    render(<StartScreen />);

    await user.click(screen.getByRole("button", { name: /Novel writing/ }));

    expect(openExample).toHaveBeenCalledWith("novel-writing");
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("says so when an example cannot be opened, and stays", async () => {
    seedFreshWorkspace();
    useApp.setState({
      openExample: vi.fn(async () => {
        throw new Error("disk full");
      }),
    });
    const user = userEvent.setup();
    render(<StartScreen />);

    await user.click(screen.getByRole("button", { name: /Novel writing/ }));

    expect(await screen.findByRole("alert")).toHaveTextContent("disk full");
    expect(screen.getByRole("dialog")).toBeInTheDocument();
  });

  it("can be asked for again, and then starting blank means a new project", async () => {
    useApp.setState({
      graph: graph([{ id: "a", title: "A" }]),
      projects: [{ name: "main", label: "main", depth: 0, path: "/ws/main", nodes: 1 }],
    });
    const user = userEvent.setup();
    render(<StartScreen />);
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();

    act(() => {
      window.dispatchEvent(new Event(OPEN_START_SCREEN_EVENT));
    });
    await user.click(screen.getByRole("button", { name: /Start with a blank project/ }));

    expect(useNewProjectRequest.getState().pending).toBe(true);
    expect(useApp.getState().preferences.explorerCollapsed).toBe(false);
  });
});
