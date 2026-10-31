import { beforeEach, describe, expect, it, vi } from "vitest";
import { api, setProjectOverride } from "../api";
import { useApp } from "../store";
import type { Graph, RunState } from "../types";

// Another tab moved the hub's active project, and this one followed it from
// the project list. The drawer stayed open on the old project's node body
// under the new project's node of the same id, and saving wrote that body
// into the other project. Following has to do a switch's browser half: the
// old project's text parked with it, and its tabs closed.

vi.mock("../api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api")>();
  return {
    ...actual,
    api: {
      putDraft: vi.fn(),
      getGraph: vi.fn(),
      getState: vi.fn(),
      getTrash: vi.fn(),
      getProjects: vi.fn(),
      getProjectOrder: vi.fn(),
      getWorkspaceStatuses: vi.fn(),
      getNodeFolders: vi.fn(),
    },
  };
});

const EMPTY_RUN: RunState = { nodes: {}, history: [] };
const ALPHA: Graph = { version: 1, nodes: [{ id: "node-0003", title: "Ship" }], edges: [], ui: {} };

function tab(id: string, content: string, dirty: boolean) {
  return {
    id,
    pinned: false,
    content,
    rev: "rev-1",
    dirty,
    loaded: true,
    diskChanged: false,
    draftPending: null,
    conflict: null,
  };
}

beforeEach(() => {
  vi.clearAllMocks();
  setProjectOverride("");
  vi.mocked(api.putDraft).mockResolvedValue({ ok: true });
  vi.mocked(api.getGraph).mockResolvedValue({ graph: ALPHA, rev: "rev-a", statuses: {}, issues: null });
  vi.mocked(api.getState).mockResolvedValue({ state: EMPTY_RUN, statuses: {} });
  vi.mocked(api.getTrash).mockResolvedValue({ items: [] });
  vi.mocked(api.getProjectOrder).mockResolvedValue({ projectOrder: [] } as never);
  vi.mocked(api.getWorkspaceStatuses).mockResolvedValue({ customStatuses: [] } as never);
  vi.mocked(api.getNodeFolders).mockResolvedValue({ folders: [] } as never);
  vi.mocked(api.getProjects).mockResolvedValue({
    workspace: "/ws",
    workspaces: [],
    active: "Alpha",
    projects: [],
  });
  useApp.setState({
    graph: { version: 1, nodes: [{ id: "node-0003", title: "Team sync 2" }], edges: [], ui: {} },
    graphRev: "rev-1",
    tabs: [tab("node-0003", "Share blockers, decisions, and ownership.", false)],
    pageDocs: {},
    activeTab: "node-0003",
    workspace: "/ws",
    activeProject: "daily-work-schedule",
    operations: {},
  });
});

describe("following another tab's project switch", () => {
  it("closes the old project's node rather than show it under the new one", async () => {
    await useApp.getState().refreshProjects();
    expect(useApp.getState().activeProject).toBe("Alpha");
    expect(useApp.getState().activeTab).toBeNull();
    expect(useApp.getState().tabs).toEqual([]);
  });

  it("parks unsaved text as a draft in the project it was written for", async () => {
    useApp.setState({ tabs: [tab("node-0003", "Share blockers. Y", true)] });
    await useApp.getState().refreshProjects();
    expect(api.putDraft).toHaveBeenCalledWith("node-0003", "Share blockers. Y", "daily-work-schedule");
    expect(useApp.getState().tabs).toEqual([]);
  });

  it("loads the new project's graph", async () => {
    await useApp.getState().refreshProjects();
    await vi.waitFor(() => expect(useApp.getState().graph).toEqual(ALPHA));
    expect(useApp.getState().graphProject).toBe("Alpha");
  });

  it("leaves the tabs alone when the active project did not move", async () => {
    vi.mocked(api.getProjects).mockResolvedValue({
      workspace: "/ws",
      workspaces: [],
      active: "daily-work-schedule",
      projects: [],
    });
    await useApp.getState().refreshProjects();
    expect(useApp.getState().activeTab).toBe("node-0003");
    expect(useApp.getState().tabs).toHaveLength(1);
  });

  it("leaves a member's own selection alone", async () => {
    setProjectOverride("daily-work-schedule");
    await useApp.getState().refreshProjects();
    expect(useApp.getState().activeProject).toBe("daily-work-schedule");
    expect(useApp.getState().activeTab).toBe("node-0003");
  });
});
