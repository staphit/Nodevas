import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "../api";
import { readPreferences } from "../preferences";
import { useApp } from "../store";
import type { Tab } from "../state/types";
import type { Graph } from "../types";
import { TabBody } from "./Drawer";
import { useNodeDetailView } from "./drawer/openNodeDetail";

const loaded: Tab = {
  id: "design",
  pinned: false,
  content: "# 設計\n",
  rev: "r1",
  dirty: false,
  loaded: true,
  diskChanged: false,
  draftPending: null,
  conflict: null,
};

beforeEach(() => {
  vi.spyOn(api, "getNodeLinks").mockResolvedValue({ backlinks: [], outgoing: [] });
  vi.spyOn(api, "listNodePages").mockResolvedValue({ pages: [] } as never);
  vi.spyOn(api, "getHistory").mockResolvedValue({ versions: [] } as never);
  useApp.setState({
    preferences: { ...readPreferences(), language: "zh-TW" },
    activeProject: "demo",
    graph: {
      version: 1,
      nodes: [
        { id: "plan", title: "企劃" },
        { id: "design", title: "設計" },
      ],
      edges: [{ from: "plan", to: "design" }],
      ui: {},
    } as unknown as Graph,
    runState: { nodes: {}, history: [] },
    statuses: { design: "ready" },
    stagedLifecycle: {},
    tabs: [loaded],
    activeTab: "design",
    requestLock: vi.fn(),
    releaseLock: vi.fn(),
  });
});

describe("node panel views", () => {
  it("has three views, and folds the rarely used properties away", async () => {
    const user = userEvent.setup();
    render(<TabBody id="design" />);
    const tabs = screen.getAllByRole("tab").filter((tab) => !tab.closest(".content-page-tabs")).map((tab) => tab.textContent);
    expect(tabs).toEqual(["內容", "相依1", "活動"]);
    const more = screen.getByText("更多屬性").closest("details")!;
    expect(more).not.toHaveAttribute("open");
    expect(screen.queryByLabelText("類型")).not.toBeInTheDocument();

    await user.click(screen.getByText("更多屬性"));
    expect(screen.getByLabelText("類型")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "刪除節點" })).toBeInTheDocument();
  });

  it("opens on the view another part of the app asked for", async () => {
    render(<TabBody id="design" />);
    expect(screen.getByRole("tab", { name: "內容" })).toHaveAttribute("aria-selected", "true");

    act(() => useNodeDetailView.getState().requestView("design", "dependencies"));
    expect(screen.getByRole("tab", { name: /相依/ })).toHaveAttribute("aria-selected", "true");
    expect(screen.getByRole("button", { name: "移除對「企劃」的相依" })).toBeInTheDocument();
    // Consumed, so it does not apply again the next time the node opens.
    expect(useNodeDetailView.getState().request).toBeNull();
  });
});
