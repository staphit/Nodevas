import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "../api";
import { readPreferences } from "../preferences";
import { useApp } from "../store";
import type { Tab } from "../state/types";
import type { Graph } from "../types";
import { TabBody } from "./Drawer";

const loaded: Tab = {
  id: "design",
  pinned: false,
  content: "# Design\n",
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
    preferences: { ...readPreferences(), language: "en" },
    activeProject: "demo",
    graph: {
      version: 1,
      nodes: [
        {
          id: "design",
          title: "Design",
          kind: "task",
          acceptance: { items: [{ id: "a1", text: "the mockup is approved" }] },
        },
      ],
      edges: [],
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

describe("node panel acceptance criteria", () => {
  it("sit at the top of the Content tab, above the editor", () => {
    const { container } = render(<TabBody id="design" />);
    const criteria = screen.getByRole("region", { name: "Acceptance criteria" });
    const content = container.querySelector(".node-content-view");
    expect(content?.firstElementChild).toContainElement(criteria);
    const fileRow = container.querySelector(".editor-file-row")!;
    expect(criteria.compareDocumentPosition(fileRow) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });
});
