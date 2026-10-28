import { act, fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "../api";
import { readPreferences } from "../preferences";
import { useSettingsRequest } from "../settings/openSettings";
import { useControl } from "../station/controlStore";
import { useApp } from "../store";
import { CommandPalette } from "./CommandPalette";
import { openCommandPalette } from "./topbar/actions";

const confirm = vi.hoisted(() => ({ answer: true }));
vi.mock("./ConfirmDialog", () => ({
  confirmAction: vi.fn(async () => confirm.answer),
}));

const switchProject = vi.fn(async () => {});
const pauseAll = vi.fn(async () => true);

beforeEach(() => {
  confirm.answer = true;
  switchProject.mockClear();
  pauseAll.mockClear();
  useApp.setState({
    preferences: { ...readPreferences(), language: "en", projectMode: "info", agentWorkstation: true },
    activeProject: "beta",
    projects: [
      { name: "alpha", label: "Alpha", depth: 0, path: "alpha", nodes: 3 },
      { name: "beta", label: "Beta", depth: 0, path: "beta", nodes: 7 },
    ],
    workspaces: [],
    switchProject,
  });
  useControl.setState({ pauseAll });
});

function open(view?: "help") {
  render(<CommandPalette />);
  act(() => openCommandPalette(view));
}

function search(text: string) {
  fireEvent.change(screen.getByRole("textbox", { name: "Search" }), { target: { value: text } });
}

describe("CommandPalette: what the top bar no longer shows", () => {
  it("finds every relocated control by name", () => {
    open();
    for (const name of [
      "Open settings",
      "Open Station",
      "Pause all agents",
      "Add a runner",
      "Daily budget",
      "Routing rules",
      "Usage",
      "Replay tour",
      "Switch to light mode",
      "Settings: Appearance and language",
      "Settings: Backup and history",
      "Switch to Alpha",
    ]) {
      expect(screen.getByRole("option", { name: new RegExp(`^${name}`) }), name).toBeTruthy();
    }
    // The project on screen is not offered as a switch.
    expect(screen.queryByRole("option", { name: /^Switch to Beta/ })).toBeNull();
  });

  it("opens a part of Settings", async () => {
    const user = userEvent.setup();
    open();
    await user.click(screen.getByRole("option", { name: /^Daily budget/ }));
    const prefs = useApp.getState().preferences;
    expect(prefs.projectMode).toBe("settings");
    expect(prefs.settingsSection).toBe("budget");
  });

  it("asks Settings for the add-runner form from Add a runner", async () => {
    const user = userEvent.setup();
    open();
    await user.click(screen.getByRole("option", { name: /^Add a runner/ }));
    expect(useApp.getState().preferences.settingsSection).toBe("runners");
    expect(useSettingsRequest.getState().add).toBe(true);
  });

  it("switches project", async () => {
    const user = userEvent.setup();
    open();
    search("alpha");
    await user.click(screen.getByRole("option", { name: /^Switch to Alpha/ }));
    expect(switchProject).toHaveBeenCalledWith("alpha");
  });

  it("pauses all agents once confirmed, leaving running runs, and opens Station", async () => {
    const user = userEvent.setup();
    open();
    await user.click(screen.getByRole("option", { name: /^Pause all agents/ }));
    expect(pauseAll).toHaveBeenCalledWith(false);
    expect(useApp.getState().preferences.projectMode).toBe("station");
  });

  it("does nothing when the pause is declined", async () => {
    confirm.answer = false;
    const user = userEvent.setup();
    open();
    await user.click(screen.getByRole("option", { name: /^Pause all agents/ }));
    expect(pauseAll).not.toHaveBeenCalled();
  });

  it("hides the agent commands while the workstation is hidden", () => {
    useApp.setState({ preferences: { ...useApp.getState().preferences, agentWorkstation: false } });
    open();
    expect(screen.queryByRole("option", { name: /^Pause all agents/ })).toBeNull();
    expect(screen.queryByRole("option", { name: /^Daily budget/ })).toBeNull();
  });

  it("opens straight on the help view when asked", () => {
    open("help");
    expect(screen.getByText("Keyboard shortcuts and help")).toBeTruthy();
    expect(screen.queryByRole("textbox", { name: "Search" })).toBeNull();
  });
});

describe("CommandPalette: new nodes by type", () => {
  it("creates a Review from \"New node: Review\"", async () => {
    const createNode = vi.fn(async () => "n1");
    useApp.setState({
      createNode,
      graph: { version: 1, nodes: [], edges: [] },
    });
    const user = userEvent.setup();
    open();
    search("Review");
    await user.click(screen.getByRole("option", { name: /^New node: Review/ }));
    expect(createNode).toHaveBeenCalledWith({ title: expect.any(String), kind: "gate" });
  });
});

describe("CommandPalette: jumping to a node", () => {
  it("opens a found node and scrolls the board to it", async () => {
    const openTab = vi.fn(async () => {});
    useApp.setState({ openTab, revealRequest: null });
    const searchSpy = vi.spyOn(api, "search").mockResolvedValue({
      results: [{ project: "beta", nodeId: "far-card", title: "Far card", kind: "node" }],
    });
    try {
      const user = userEvent.setup();
      open();
      search("Far card");
      await user.click(await screen.findByRole("option", { name: /Far card/ }));
      expect(openTab).toHaveBeenCalledWith("far-card");
      // The board may be showing anywhere else: the jump has to reach it too.
      expect(useApp.getState().revealRequest?.nodeId).toBe("far-card");
    } finally {
      searchSpy.mockRestore();
    }
  });
});

describe("CommandPalette: while a search is on its way", () => {
  it("says it is searching, not that nothing matched, until the answer arrives", async () => {
    let answer: (value: Awaited<ReturnType<typeof api.search>>) => void = () => {};
    const searchSpy = vi
      .spyOn(api, "search")
      .mockImplementation(() => new Promise((resolve) => (answer = resolve)));
    vi.useFakeTimers();
    try {
      open();
      search("zzqx");
      // Inside the debounce, and while the request is out: no "no results".
      expect(screen.queryByText("No matching commands or content")).toBeNull();
      expect(screen.getByText("Searching…")).toBeTruthy();
      await act(async () => {
        await vi.advanceTimersByTimeAsync(200);
      });
      expect(searchSpy).toHaveBeenCalledWith("zzqx");
      expect(screen.queryByText("No matching commands or content")).toBeNull();
      expect(screen.getByText("Searching…")).toBeTruthy();
      await act(async () => {
        answer({ results: [] });
        await vi.advanceTimersByTimeAsync(0);
      });
      expect(screen.getByText("No matching commands or content")).toBeTruthy();
      expect(screen.queryByText("Searching…")).toBeNull();
      // A new query is pending again until its own answer arrives.
      search("zzqxy");
      expect(screen.queryByText("No matching commands or content")).toBeNull();
      expect(screen.getByText("Searching…")).toBeTruthy();
    } finally {
      vi.useRealTimers();
      searchSpy.mockRestore();
    }
  });
});
