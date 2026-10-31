import { beforeEach, describe, expect, it, vi } from "vitest";
import { api, ConflictError } from "../api";
import { describeGraphCommand, dependencyConnectionError } from "../domain";
import type { Language } from "../preferences";
import { readPreferences } from "../preferences";
import { useApp } from "../store";
import type { Graph } from "../types";
import { clearUndo } from "./undo";

// Refusals and failures of a board edit are read in the drawer banner and the
// top bar, so they come out in the reader's language: an English board used
// to say 「此連線會產生循環依賴。」 and 「建立前置關係失敗」.

vi.mock("../api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api")>();
  return {
    ...actual,
    api: {
      getGraph: vi.fn(),
      getState: vi.fn(),
      putGraph: vi.fn(),
      graphOps: vi.fn(),
    },
  };
});

const CJK = /[㐀-鿿]/;

function chain(): Graph {
  // b waits for a, so a waiting for b closes a loop.
  return {
    version: 1,
    nodes: [
      { id: "a", title: "A" },
      { id: "b", title: "B", requires: "a" },
    ],
    edges: [{ from: "a", to: "b" }],
    ui: {},
  };
}

function setLanguage(language: Language) {
  useApp.setState({ preferences: { ...readPreferences(), language } });
}

function resetStore(graph: Graph | null = chain()) {
  clearUndo();
  useApp.setState({
    graph,
    graphRev: "rev-1",
    issues: [],
    error: null,
    operations: {},
    lastOperation: null,
  });
}

beforeEach(() => {
  vi.mocked(api.getState).mockResolvedValue({ state: { nodes: {}, history: [] }, statuses: {} });
  vi.mocked(api.getGraph).mockResolvedValue({ graph: chain(), rev: "rev-9", statuses: {}, issues: null });
  resetStore();
});

describe("dependency refusals", () => {
  it("are written in English on an English board", () => {
    setLanguage("en");
    const edges = chain().edges!;
    expect(dependencyConnectionError(edges, "a", "a")).toBe("A node can't depend on itself.");
    expect(dependencyConnectionError(edges, "a", "b")).toBe("This dependency already exists.");
    expect(dependencyConnectionError(edges, "b", "a")).toBe(
      "This connection would create a circular dependency.",
    );
  });

  it("keep their Traditional Chinese wording on a zh-TW board", () => {
    setLanguage("zh-TW");
    const edges = chain().edges!;
    expect(dependencyConnectionError(edges, "a", "a")).toBe("不可連接節點自身。");
    expect(dependencyConnectionError(edges, "a", "b")).toBe("此關係已存在。");
    expect(dependencyConnectionError(edges, "b", "a")).toBe("此連線會產生循環依賴。");
  });

  it("reach the top bar in English when Add dependency closes a loop", async () => {
    setLanguage("en");
    const result = await useApp
      .getState()
      .updateNode({ type: "node.addDependency", sourceId: "b", targetId: "a" });
    expect(result.ok === false && result.message).toBe(
      "This connection would create a circular dependency.",
    );
    expect(useApp.getState().lastOperation?.message).toBe(
      "This connection would create a circular dependency.",
    );
  });
});

describe("graph command failures", () => {
  it("say no project is loaded in the reader's language", async () => {
    setLanguage("en");
    resetStore(null);
    const result = await useApp.getState().updateNodeMetadata("a", { title: "x" });
    expect(result.ok === false && result.message).toBe("No project is loaded.");
  });

  it("name the failed edit in the reader's language when the error has no words", async () => {
    vi.mocked(api.graphOps).mockRejectedValue(new Error(""));
    setLanguage("en");
    const en = await useApp.getState().updateNodeMetadata("a", { title: "x" });
    expect(en.ok === false && en.message).toBe("Edit node details failed");
    setLanguage("zh-TW");
    resetStore();
    const zh = await useApp.getState().updateNodeMetadata("a", { title: "x" });
    expect(zh.ok === false && zh.message).toBe("修改節點資料失敗");
  });

  it("explain a version conflict in the reader's language", async () => {
    vi.mocked(api.graphOps).mockRejectedValue(new ConflictError("rev-9", "..."));
    setLanguage("en");
    const result = await useApp.getState().updateNodeMetadata("a", { title: "x" });
    expect(result.ok === false && result.message).not.toMatch(CJK);
  });

  it("label every edit for the undo stack in the reader's language", () => {
    setLanguage("en");
    expect(describeGraphCommand({ type: "node.addDependency", sourceId: "a", targetId: "b" })).toBe(
      "Add prerequisite",
    );
    expect(describeGraphCommand({ type: "plan.remove", nodeId: "a", milestoneType: "done" })).toBe(
      "Delete planned milestone",
    );
    setLanguage("zh-TW");
    expect(describeGraphCommand({ type: "node.addDependency", sourceId: "a", targetId: "b" })).toBe(
      "建立前置關係",
    );
  });
});
