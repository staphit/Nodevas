import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "../../api";
import { useApp } from "../../store";
import { LifecyclePanel } from "./LifecyclePanel";

vi.mock("../../api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../../api")>();
  return {
    ...actual,
    api: {
      setStatus: vi.fn(),
      getState: vi.fn(),
      getGraph: vi.fn(),
      getNode: vi.fn(),
      putNode: vi.fn(),
    },
  };
});

const STAGED = "尚未套用";

beforeEach(() => {
  vi.mocked(api.setStatus).mockResolvedValue({
    ok: true,
    state: { nodes: { a: { status: "in_progress" } }, history: [] },
    statuses: { a: "in_progress" },
  });
  useApp.setState({
    graph: { version: 1, nodes: [{ id: "a", title: "設計稿" }], edges: [], ui: {} },
    runState: { nodes: {}, history: [] },
    statuses: { a: "ready" },
    stagedLifecycle: {},
    operations: {},
    tabs: [],
    activeTab: null,
    error: null,
    preferences: { ...useApp.getState().preferences, language: "zh-TW" },
  });
});

describe("LifecyclePanel", () => {
  // The document auto-saves; this deliberately does not. A status is an append
  // to run/journal.jsonl, which cannot be taken back, so it may not be written
  // by a timer that nobody asked for. See runSlice.ts.
  it("stages a status instead of writing it straight away", async () => {
    const user = userEvent.setup();
    render(<LifecyclePanel id="a" />);
    expect(screen.queryByText(STAGED)).not.toBeInTheDocument();

    await user.selectOptions(screen.getByLabelText("改為"), "in_progress");
    await user.type(screen.getByLabelText("實際狀態註解"), "開工");

    expect(screen.getByText(STAGED)).toBeInTheDocument();
    expect(api.setStatus).not.toHaveBeenCalled();
    expect(useApp.getState().stagedLifecycle.a).toEqual({
      status: "in_progress",
      note: "開工",
    });

    // And it stays staged through an automatic save of the document, which is
    // the case the auto-save feature introduced.
    await act(async () => {
      await useApp.getState().saveTab("a", { auto: true });
    });
    expect(api.setStatus).not.toHaveBeenCalled();
    expect(screen.getByText(STAGED)).toBeInTheDocument();
  });

  // Since the document no longer needs a keystroke, a staged status with no
  // visible way to apply it would be a change that silently never happens.
  it("writes the staged status when its own apply button is pressed", async () => {
    const user = userEvent.setup();
    render(<LifecyclePanel id="a" />);
    await user.selectOptions(screen.getByLabelText("改為"), "in_progress");
    await user.type(screen.getByLabelText("實際狀態註解"), "開工");

    await user.click(screen.getByRole("button", { name: "套用狀態" }));

    expect(api.setStatus).toHaveBeenCalledWith("a", "in_progress", "開工");
    await waitFor(() => expect(screen.queryByText(STAGED)).not.toBeInTheDocument());
    expect(useApp.getState().stagedLifecycle.a).toBeUndefined();
  });

  it("writes the staged status when the node is saved on purpose", async () => {
    const user = userEvent.setup();
    render(<LifecyclePanel id="a" />);
    await user.selectOptions(screen.getByLabelText("改為"), "in_progress");
    await user.type(screen.getByLabelText("實際狀態註解"), "開工");

    // An explicit save — Ctrl/⌘ + S, save-all, the popout's button — still
    // carries the status out, even with untouched text.
    await act(async () => {
      await useApp.getState().saveTab("a");
    });

    expect(api.setStatus).toHaveBeenCalledWith("a", "in_progress", "開工");
    await waitFor(() => expect(screen.queryByText(STAGED)).not.toBeInTheDocument());
    expect(useApp.getState().stagedLifecycle.a).toBeUndefined();
  });

  it("saves nothing when no status is staged", async () => {
    render(<LifecyclePanel id="a" />);
    await act(async () => {
      await useApp.getState().saveTab("a");
    });
    expect(api.setStatus).not.toHaveBeenCalled();
  });

  it("keeps the choice on screen when the write fails", async () => {
    vi.mocked(api.setStatus).mockRejectedValue(new Error("磁碟已滿"));
    const user = userEvent.setup();
    render(<LifecyclePanel id="a" />);
    await user.selectOptions(screen.getByLabelText("改為"), "in_progress");

    await act(async () => {
      await useApp.getState().saveTab("a");
    });

    expect(screen.getByText(STAGED)).toBeInTheDocument();
    expect(useApp.getState().stagedLifecycle.a?.status).toBe("in_progress");
  });
});
