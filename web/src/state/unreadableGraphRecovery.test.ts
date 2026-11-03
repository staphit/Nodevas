import { beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "../api";
import { translate } from "../i18n";
import { readPreferences } from "../preferences";
import { useApp } from "../store";
import type { Graph } from "../types";
import { operationScope, selectOperation } from "./operations";
import { clearUndo } from "./undo";

// While graph.yaml cannot be read every edit is refused with "graph.yaml
// can't be read". Once the file is fixed and the graph reloads, those
// refusals no longer hold: the panels and the status line must not keep
// telling the reader to fix a file that is already fixed.

vi.mock("../api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api")>();
  return {
    ...actual,
    api: {
      getGraph: vi.fn(),
      getState: vi.fn(),
      getNodeFolders: vi.fn(),
      putGraph: vi.fn(),
      graphOps: vi.fn(),
    },
  };
});

const HUB_PARSE_EN =
  "graph.yaml: line 2 has an error. Fix the file, then reload. (yaml: line 2: did not find expected ',' or ']')";

const GRAPH: Graph = {
  version: 1,
  nodes: [{ id: "a", title: "A" }],
  edges: [],
  nodeTypes: [],
  ui: {},
};

beforeEach(() => {
  vi.clearAllMocks();
  clearUndo();
  vi.mocked(api.getState).mockResolvedValue({ statuses: {} } as never);
  vi.mocked(api.getNodeFolders).mockResolvedValue({} as never);
  vi.mocked(api.getGraph).mockResolvedValue({ graph: GRAPH, rev: "rev-2", issues: [] } as never);
  useApp.setState({
    preferences: { ...readPreferences(), language: "en" },
    graph: GRAPH,
    graphRev: "rev-1",
    graphUnreadable: HUB_PARSE_EN,
    activeProject: "Alpha",
    error: null,
    operations: {},
    lastOperation: null,
  });
});

const rename = () =>
  useApp.getState().updateNode({ type: "node.updateMetadata", nodeId: "a", patch: { title: "B" } });

describe("graph.yaml readable again", () => {
  it("clears the refusals the unreadable file caused", async () => {
    const refused = await rename();
    expect(refused.ok).toBe(false);
    const scope = refused.scope;
    expect(selectOperation(useApp.getState().operations, scope).status).toBe("error");
    expect(useApp.getState().lastOperation?.message).toBe(translate("graph.error.unreadable", "en"));

    await useApp.getState().loadAll();

    expect(useApp.getState().graphUnreadable).toBeNull();
    expect(selectOperation(useApp.getState().operations, scope).status).toBe("idle");
    expect(useApp.getState().lastOperation).toBeNull();
  });

  it("clears a refusal worded in the language the reader used before switching", async () => {
    useApp.setState({ preferences: { ...readPreferences(), language: "zh-TW" } });
    const refused = await rename();
    useApp.setState({ preferences: { ...readPreferences(), language: "en" } });

    await useApp.getState().loadAll();

    expect(selectOperation(useApp.getState().operations, refused.scope).status).toBe("idle");
  });

  it("keeps errors that had nothing to do with the unreadable file", async () => {
    const other = operationScope.document("a");
    useApp.getState().settleOperation(other, {
      status: "error",
      operation: "document.save",
      message: "disk full",
    });
    await rename();

    await useApp.getState().loadAll();

    expect(selectOperation(useApp.getState().operations, other)).toMatchObject({
      status: "error",
      message: "disk full",
    });
  });

  it("leaves errors alone when the graph was readable all along", async () => {
    useApp.setState({ graphUnreadable: null });
    const scope = operationScope.node("a");
    const message = translate("graph.error.unreadable", "en");
    useApp.getState().settleOperation(scope, { status: "error", operation: "x", message });

    await useApp.getState().loadAll();

    expect(selectOperation(useApp.getState().operations, scope).status).toBe("error");
  });
});
