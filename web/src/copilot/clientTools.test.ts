import { describe, expect, it, vi } from "vitest";
import { runClientTool, SUPPORTED_CLIENT_TOOLS } from "./clientTools";

function fakeStore() {
  const state = {
    graph: { version: 1, nodes: [{ id: "n1", title: "Login" }] },
    projects: [
      { name: "alpha", label: "Alpha", depth: 0, path: "alpha", nodes: 1 },
      { name: "group", label: "Group", depth: 0, path: "group", nodes: 0, isFolder: true },
    ],
    activeProject: "main",
    paneOpen: { graph: true, timeline: true },
    openTab: vi.fn(async () => undefined),
    revealNode: vi.fn(),
    togglePane: vi.fn((pane: "graph" | "timeline") => {
      state.paneOpen = { ...state.paneOpen, [pane]: !state.paneOpen[pane] };
    }),
    switchProject: vi.fn(async () => undefined),
  };
  return { state, access: () => state as never };
}

describe("runClientTool", () => {
  it("declares only the tools it implements", () => {
    expect(SUPPORTED_CLIENT_TOOLS).toEqual(["ui_open_node", "ui_set_view", "ui_switch_project"]);
  });

  it("ui_open_node opens and reveals the node", async () => {
    const { state, access } = fakeStore();
    const result = await runClientTool("ui_open_node", { id: "n1" }, access);
    expect(result).toEqual({ output: "opened n1 (Login)", isError: false });
    expect(state.openTab).toHaveBeenCalledWith("n1");
    expect(state.revealNode).toHaveBeenCalledWith("n1");
    expect((await runClientTool("ui_open_node", { id: "zz" }, access)).isError).toBe(true);
  });

  it("ui_set_view leaves only the requested pane open", async () => {
    const { state, access } = fakeStore();
    await runClientTool("ui_set_view", '{"view":"timeline"}', access);
    expect(state.paneOpen).toEqual({ graph: false, timeline: true });
    await runClientTool("ui_set_view", { view: "board" }, access);
    expect(state.paneOpen).toEqual({ graph: true, timeline: false });
    expect((await runClientTool("ui_set_view", { view: "kanban" }, access)).isError).toBe(true);
  });

  it("ui_switch_project switches to a real project only", async () => {
    const { state, access } = fakeStore();
    const result = await runClientTool("ui_switch_project", { name: "alpha" }, access);
    expect(result.switchedProject).toBe(true);
    expect(state.switchProject).toHaveBeenCalledWith("alpha");
    expect((await runClientTool("ui_switch_project", { name: "group" }, access)).isError).toBe(true);
    expect((await runClientTool("ui_switch_project", { name: "main" }, access)).switchedProject).toBeUndefined();
  });

  it("reports failures and unknown tools as errors", async () => {
    const { state, access } = fakeStore();
    state.switchProject.mockRejectedValueOnce(new Error("refused"));
    expect(await runClientTool("ui_switch_project", { name: "alpha" }, access)).toEqual({
      output: "refused",
      isError: true,
    });
    expect((await runClientTool("ui_set_filter", {}, access)).isError).toBe(true);
  });
});
