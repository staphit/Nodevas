import { act, fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { api, RequestError } from "../api";
import { translate } from "../i18n";
import { readPreferences } from "../preferences";
import { useApp } from "../store";
import type { Graph, RunState } from "../types";
import { LaneView } from "./LaneView";

vi.mock("../api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api")>();
  return {
    ...actual,
    api: {
      getGraph: vi.fn(),
      getState: vi.fn(),
      getNodeFolders: vi.fn(),
      putGraph: vi.fn(),
    },
  };
});

const EMPTY_RUN: RunState = { nodes: {}, history: [] };

// What the hub answers GET /api/graph with while graph.yaml does not parse
// (store/graph_unreadable.go): its own sentence, with the advice, around the
// parser's words.
const HUB_PARSE_EN =
  "graph.yaml: line 3 has an error. Fix the file, then reload. (yaml: line 3: did not find expected node content)";
const HUB_PARSE_ZH =
  "graph.yaml: 第 3 行有錯誤。修正檔案後重新載入。（yaml: line 3: did not find expected node content）";

const VALID: Graph = { version: 1, nodes: [{ id: "a", title: "Design" }], edges: [], ui: {} };

const paneProps = {
  collapsed: false,
  onToggle: () => undefined,
  selectedNodeId: null,
  onSelectedNodeChange: () => undefined,
  keyboardActive: false,
  onActivate: () => undefined,
};

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(api.getState).mockResolvedValue({ state: EMPTY_RUN, statuses: {} });
  vi.mocked(api.getNodeFolders).mockResolvedValue({ folders: [] } as never);
  vi.mocked(api.getGraph).mockRejectedValue(new RequestError(422, HUB_PARSE_EN));
  useApp.setState({
    preferences: { ...readPreferences(), language: "en" },
    graph: null,
    graphRev: "",
    runState: EMPTY_RUN,
    statuses: {},
    issues: [],
    operations: {},
    error: null,
    tabs: [],
    activeTab: null,
  });
  useApp.getState().updateUIPreference("language", "en");
});

async function load() {
  await act(async () => {
    await useApp.getState().loadAll().catch(() => undefined);
  });
}

describe("a board whose graph.yaml cannot be read", () => {
  it("says why and how to recover instead of offering to create the first node", async () => {
    render(<LaneView variant="graph" {...paneProps} />);
    await load();

    const alert = screen.getByRole("alert");
    expect(alert.textContent).toContain("graph.yaml");
    expect(alert.textContent).toContain("line 3 has an error");
    expect(alert.textContent).toContain("did not find expected node content");
    // The advice is said once, not by the hub and again by the board.
    expect(alert.textContent?.match(/Fix the file, then reload/g)).toHaveLength(1);

    expect(screen.queryByText(translate("lane.emptyTitle", "en"))).toBeNull();
    expect(screen.queryByRole("button", { name: translate("nodeCreate.create", "en") })).toBeNull();
    expect(screen.queryByRole("button", { name: translate("board.newNode", "en") })).toBeNull();
  });

  it("does not repeat the reason in the app-wide error bar", async () => {
    render(<LaneView variant="graph" {...paneProps} />);
    // App.tsx hands a rejected load to the error bar; the board says it.
    await act(async () => {
      await expect(useApp.getState().loadAll()).resolves.toBeUndefined();
    });
    expect(screen.getByRole("alert")).toBeTruthy();
  });

  it("covers a board that was open when the file broke", async () => {
    useApp.setState({ graph: VALID, graphRev: "rev-1" });
    render(<LaneView variant="graph" {...paneProps} />);
    expect(screen.getByText("Design")).toBeTruthy();
    await load();
    expect(screen.getByRole("alert")).toBeTruthy();
    expect(screen.queryByText("Design")).toBeNull();
  });

  it("shows the same state on the timeline", async () => {
    render(<LaneView variant="timeline" {...paneProps} />);
    await load();
    expect(screen.getByRole("alert").textContent).toContain("line 3 has an error");
    expect(screen.queryByText(translate("lane.emptyTitle", "en"))).toBeNull();
    expect(screen.queryByRole("button", { name: translate("board.newNode", "en") })).toBeNull();
  });

  it("speaks the reader's language", async () => {
    useApp.setState({ preferences: { ...readPreferences(), language: "zh-TW" } });
    useApp.getState().updateUIPreference("language", "zh-TW");
    vi.mocked(api.getGraph).mockRejectedValue(new RequestError(422, HUB_PARSE_ZH));
    render(<LaneView variant="graph" {...paneProps} />);
    await load();
    const text = screen.getByRole("alert").textContent ?? "";
    expect(text).toContain("第 3 行有錯誤");
    expect(text.match(/修正檔案後重新載入/g)).toHaveLength(1);
    expect(text).not.toMatch(/Fix the file|can't be/);
  });

  it("gives its own advice when the hub's refusal carries none", async () => {
    vi.mocked(api.getGraph).mockRejectedValue(
      new RequestError(422, 'load project "Disk": graph.yaml: node "c" has invalid priority "zzz"'),
    );
    render(<LaneView variant="graph" {...paneProps} />);
    await load();
    const text = screen.getByRole("alert").textContent ?? "";
    expect(text).toContain('node "c" has invalid priority "zzz"');
    expect(text).not.toContain("load project");
    expect(text).toContain(translate("lane.unreadableAdvice", "en"));
  });

  it("comes back by itself once the file reads again", async () => {
    render(<LaneView variant="graph" {...paneProps} />);
    await load();
    expect(screen.getByRole("alert")).toBeTruthy();

    // The watcher's graph-changed reloads the graph (App.tsx).
    vi.mocked(api.getGraph).mockResolvedValue({ graph: VALID, rev: "rev-2", issues: [] });
    await load();
    expect(screen.queryByRole("alert")).toBeNull();
    expect(screen.getByText("Design")).toBeTruthy();
  });

  it("reloads from its own button", async () => {
    render(<LaneView variant="graph" {...paneProps} />);
    await load();
    vi.mocked(api.getGraph).mockResolvedValue({ graph: VALID, rev: "rev-2", issues: [] });
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: translate("lane.unreadableReload", "en") }));
    });
    await act(async () => {
      await Promise.resolve();
    });
    expect(screen.queryByRole("alert")).toBeNull();
    expect(screen.getByText("Design")).toBeTruthy();
  });

  it("leaves any other load failure to the error bar", async () => {
    vi.mocked(api.getGraph).mockRejectedValue(new RequestError(500, "disk on fire"));
    render(<LaneView variant="graph" {...paneProps} />);
    let failure: unknown = null;
    await act(async () => {
      await useApp
        .getState()
        .loadAll()
        .catch((error) => {
          failure = error;
        });
    });
    expect(failure).toBeInstanceOf(RequestError);
    expect(screen.queryByRole("alert")).toBeNull();
  });
});
