import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { readPreferences } from "../../preferences";
import { useApp } from "../../store";
import type { Tab } from "../../state/types";
import type { Graph } from "../../types";
import { DrawerTabs } from "./DrawerTabs";
import { openNodeDetail, useNodeDetailView } from "./openNodeDetail";

function tab(id: string): Tab {
  return {
    id,
    pinned: false,
    content: "",
    rev: "",
    dirty: false,
    loaded: true,
    diskChanged: false,
    draftPending: null,
    conflict: null,
  };
}

const updateNodeMetadata = vi.fn(async () => ({ ok: true }) as never);

beforeEach(() => {
  updateNodeMetadata.mockClear();
  useApp.setState({
    preferences: { ...readPreferences(), language: "zh-TW" },
    graph: {
      version: 1,
      nodes: [
        { id: "a", title: "設計稿" },
        { id: "b", title: "審查" },
      ],
      edges: [],
      ui: {},
    } as unknown as Graph,
    tabs: [tab("a")],
    activeTab: "a",
    updateNodeMetadata,
  });
});

function renderHead(onToggleCollapsed = vi.fn(), onClose = vi.fn()) {
  render(
    <DrawerTabs
      activeTab="a"
      menuOpenRef={{ current: false }}
      onToggleCollapsed={onToggleCollapsed}
      onClose={onClose}
    />,
  );
  return { onToggleCollapsed, onClose };
}

describe("DrawerTabs", () => {
  it("is the node's editable title, a menu and close — no tab strip for one node", async () => {
    const user = userEvent.setup();
    renderHead();
    const title = screen.getByRole("textbox", { name: "標題" });
    expect(title).toHaveValue("設計稿");
    expect(screen.queryByRole("group", { name: "已開啟的節點" })).not.toBeInTheDocument();

    await user.clear(title);
    await user.type(title, "設計稿 v2{Enter}");
    expect(updateNodeMetadata).toHaveBeenCalledWith("a", { title: "設計稿 v2" });
  });

  it("shows the strip once a second node is open", () => {
    useApp.setState({ tabs: [tab("a"), tab("b")] });
    renderHead();
    expect(screen.getByRole("group", { name: "已開啟的節點" })).toHaveTextContent("審查");
  });

  it("names each tab's close button after the node's title, not its id", () => {
    useApp.setState({
      tabs: [tab("a"), tab("b"), tab("gone")],
      preferences: { ...useApp.getState().preferences, language: "en" },
    });
    renderHead();
    const strip = screen.getByRole("group", { name: "Open nodes" });
    const closers = [...strip.querySelectorAll("button.tab-close")];
    expect(closers.map((button) => button.getAttribute("aria-label"))).toEqual([
      "Close 設計稿",
      "Close 審查",
      // A node the graph no longer has keeps the name its tab shows.
      "Close gone",
    ]);
    expect(closers.map((button) => button.getAttribute("title"))).toEqual(["Close 設計稿", "Close 審查", "Close gone"]);
    expect(screen.queryByRole("button", { name: "Close a" })).not.toBeInTheDocument();
  });

  it("keeps the tab actions and collapsing reachable from the ⋯ menu", async () => {
    const user = userEvent.setup();
    const { onToggleCollapsed } = renderHead();
    await user.click(screen.getByRole("button", { name: "面板動作" }));
    expect(screen.getByRole("menuitem", { name: "複製相對路徑" })).toBeInTheDocument();
    await user.click(screen.getByRole("menuitem", { name: "收合資訊欄" }));
    expect(onToggleCollapsed).toHaveBeenCalled();
  });
});

describe("openNodeDetail", () => {
  it("asks the panel for a view and opens the node", () => {
    const openTab = vi.fn(async () => undefined);
    useApp.setState({ openTab });
    openNodeDetail("b", "dependencies");
    expect(useNodeDetailView.getState().request).toMatchObject({
      nodeId: "b",
      view: "dependencies",
    });
    expect(openTab).toHaveBeenCalledWith("b");
    useNodeDetailView.getState().clearRequest();
  });
});
