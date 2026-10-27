import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { explorerApi } from "../api/explorer";
import { readPreferences } from "../preferences";
import { useApp } from "../store";
import ExplorerMain from "./ExplorerMain";
import { useExplorer } from "./explorerStore";

beforeEach(() => {
  useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
  useExplorer.setState({ view: null });
  vi.spyOn(explorerApi, "workspaceFile").mockResolvedValue({
    path: "docs/notes.txt",
    size: 6,
    kind: "text",
    content: "notes\n",
    truncated: false,
  });
});
afterEach(() => vi.restoreAllMocks());

// W-16: the main area shows the chosen item instead of the board.
describe("ExplorerMain", () => {
  it("opens and closes the store's view", () => {
    act(() => useExplorer.getState().open({ kind: "file", source: "workspace", path: "docs/notes.txt" }));
    expect(useExplorer.getState().view).toEqual({ kind: "file", source: "workspace", path: "docs/notes.txt" });
    act(() => useExplorer.getState().close());
    expect(useExplorer.getState().view).toBeNull();
  });

  it("shows the chosen file and goes back to the board", async () => {
    act(() => useExplorer.getState().open({ kind: "file", source: "workspace", path: "docs/notes.txt" }));
    render(<ExplorerMain />);
    expect(await screen.findByText("notes")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Back to board" }));
    expect(useExplorer.getState().view).toBeNull();
  });

  it("explains itself when nothing is chosen", () => {
    render(<ExplorerMain />);
    expect(screen.getByText("Pick a file, branch or agent")).toBeInTheDocument();
    expect(screen.getByText("Four tabs on the left: Nodes, Files, Git, Agent")).toBeInTheDocument();
  });
});
