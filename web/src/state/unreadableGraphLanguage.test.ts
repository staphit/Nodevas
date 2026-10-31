import { beforeEach, describe, expect, it, vi } from "vitest";
import { api, RequestError } from "../api";
import { readPreferences } from "../preferences";
import { useApp } from "../store";

// The unreadable-graph reason is the hub's own sentence, worded for the
// language the request asked in (store/graph_unreadable.go). Switching the UI
// language left it in the old one until Reload was clicked.

vi.mock("../api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api")>();
  return {
    ...actual,
    api: {
      getGraph: vi.fn(),
      getState: vi.fn(),
      getNodeFolders: vi.fn(),
    },
  };
});

const HUB_PARSE_ZH =
  "graph.yaml: 第 3 行有錯誤。修正檔案後重新載入。（yaml: line 3: did not find expected node content）";
const HUB_PARSE_EN =
  "graph.yaml: line 3 has an error. Fix the file, then reload. (yaml: line 3: did not find expected node content)";

beforeEach(() => {
  // The language is set apart from the rest: setting it is itself a change.
  useApp.setState({
    preferences: { ...readPreferences(), language: "zh-TW" },
    graphUnreadable: null,
  });
  vi.clearAllMocks();
  vi.mocked(api.getState).mockRejectedValue(new RequestError(422, HUB_PARSE_EN));
  vi.mocked(api.getGraph).mockRejectedValue(new RequestError(422, HUB_PARSE_EN));
  useApp.setState({
    graph: null,
    graphUnreadable: HUB_PARSE_ZH,
    activeProject: "Alpha",
    error: null,
  });
});

describe("an unreadable graph.yaml after the UI language changes", () => {
  it("asks the hub again, so its reason reads in the new language", async () => {
    useApp.getState().updateUIPreference("language", "en");
    await vi.waitFor(() => expect(useApp.getState().graphUnreadable).toBe(HUB_PARSE_EN));
    expect(api.getGraph).toHaveBeenCalledTimes(1);
  });

  it("does not reload a graph that reads", async () => {
    useApp.setState({ graphUnreadable: null });
    useApp.getState().updateUIPreference("language", "en");
    await new Promise((resolve) => setTimeout(resolve, 60));
    expect(api.getGraph).not.toHaveBeenCalled();
  });
});
