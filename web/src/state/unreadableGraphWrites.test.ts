import { beforeEach, describe, expect, it, vi } from "vitest";
import { api, ConflictError, RequestError } from "../api";
import { translate } from "../i18n";
import { readPreferences } from "../preferences";
import { useApp } from "../store";
import type { Graph } from "../types";
import { clearUndo } from "./undo";

// A graph.yaml that does not parse or validate cannot be written over: the
// hub refuses a PUT with 409, and the reload that follows reads 422. Saying
// "changed elsewhere, reloaded; apply again" sent the reader round a loop
// that could never work, and hid the real reason.

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
  vi.mocked(api.getState).mockRejectedValue(new RequestError(422, HUB_PARSE_EN));
  vi.mocked(api.getGraph).mockRejectedValue(new RequestError(422, HUB_PARSE_EN));
  vi.mocked(api.putGraph).mockRejectedValue(new ConflictError("", ""));
  vi.mocked(api.graphOps).mockRejectedValue(new ConflictError("", ""));
  useApp.setState({
    preferences: { ...readPreferences(), language: "en" },
    graph: GRAPH,
    graphRev: "rev-1",
    graphUnreadable: null,
    activeProject: "Alpha",
    error: null,
    operations: {},
    lastOperation: null,
  });
});

const setShape = () =>
  useApp.getState().updateWorkflowDefinition({ type: "workflow.setOfficeStyle", style: "study" });

describe("a settings change refused because graph.yaml became unreadable", () => {
  it("says graph.yaml can't be read, not that it changed elsewhere", async () => {
    const result = await setShape();
    expect(useApp.getState().graphUnreadable).toBe(HUB_PARSE_EN);
    expect(result.ok).toBe(false);
    if (result.ok) return;
    expect(result.message).toBe(translate("graph.error.unreadable", "en"));
    expect(result.status).not.toBe("conflict");
    expect(useApp.getState().lastOperation?.status).toBe("error");
    expect(useApp.getState().lastOperation?.message).toBe(translate("graph.error.unreadable", "en"));
    expect(useApp.getState().error).not.toBe(translate("graph.error.conflict", "en"));
  });

  it("is not sent again once the graph is known to be unreadable", async () => {
    useApp.setState({ graphUnreadable: HUB_PARSE_EN });
    const result = await setShape();
    expect(api.putGraph).not.toHaveBeenCalled();
    expect(api.graphOps).not.toHaveBeenCalled();
    expect(result.ok === false && result.message).toBe(translate("graph.error.unreadable", "en"));
  });
});
