import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "../api";
import { readPreferences } from "../preferences";
import { useApp } from "../store";
import type { Tab } from "../state/types";
import type { Graph } from "../types";
import { TabBody } from "./Drawer";

// Another window holds the soft lock on this document, so this one reads it.
// The banner saying so used to sit in the flex row that lays the outline and
// the editor side by side, and took the width the document should have had:
// with large text the editor was squeezed to nothing. And the footer still
// promised to save edits this window cannot make.

const dirty: Tab = {
  id: "api",
  pinned: false,
  content: "# 後端 API\n",
  rev: "r1",
  dirty: true,
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
      nodes: [{ id: "api", title: "後端 API" }],
      edges: [],
      ui: {},
    } as unknown as Graph,
    runState: { nodes: {}, history: [] },
    statuses: { api: "ready" },
    stagedLifecycle: {},
    tabs: [dirty],
    activeTab: "api",
    locks: [{ nodeId: "api", actor: { name: "local" } as never, since: "2026-10-29T00:00:00Z", mine: false }],
    requestLock: vi.fn(),
    releaseLock: vi.fn(),
  });
});

describe("a document another window is editing", () => {
  it("states the lock above the editor, not in the row the editor shares with the outline", () => {
    const { container } = render(<TabBody id="api" />);
    const banner = screen.getByText("local 正在編輯這份文件，目前為唯讀。").closest(".editor-lock-banner")!;
    expect(banner).toBeInTheDocument();
    const stage = container.querySelector(".editor-stage")!;
    expect(stage.contains(banner)).toBe(false);
    // Before the stage, so it reads first and spans the view's full width.
    expect(banner.compareDocumentPosition(stage) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  it("does not claim to be editing or saving while it is read-only", () => {
    const { container } = render(<TabBody id="api" />);
    expect(container.querySelector(".editor-path-dirty")).toBeNull();
    const footer = container.querySelector(".tab-footer")!;
    expect(footer).not.toHaveTextContent("編輯中，將自動儲存");
    expect(footer).not.toHaveTextContent("立即儲存");
    expect(footer).toHaveTextContent("唯讀，不會儲存");
  });
});
