import { beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "../api";
import { useApp } from "../store";
import type { ProjectEntry } from "./types";

vi.mock("../api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api")>();
  return {
    ...actual,
    api: {
      ...actual.api,
      getProjects: vi.fn(),
      getProjectOrder: vi.fn(),
    },
  };
});

const MAIN: ProjectEntry = { name: "main", label: "main", depth: 0, path: "/w/main", nodes: 0 } as ProjectEntry;
const ALPHA: ProjectEntry = { name: "alpha", label: "alpha", depth: 0, path: "/w/alpha", nodes: 0 } as ProjectEntry;

function serverLists(...projects: ProjectEntry[]) {
  // A fresh body each time, as off the wire.
  vi.mocked(api.getProjects).mockImplementation(async () => ({
    workspace: "/w",
    workspaces: [{ path: "/w", label: "w", active: true, projects: projects.length }],
    active: "main",
    projects: projects.map((project) => ({ ...project })),
  }));
}

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(api.getProjectOrder).mockResolvedValue({ projectOrder: [] });
  serverLists(MAIN);
  useApp.setState({ projects: [], workspaces: [], activeProject: "main" });
});

describe("project list refresh", () => {
  // Every change to the workspace's projects ends with the list reloaded,
  // whether the change and whatever follows it (switching into the new
  // project) worked or not: a create that landed on disk and then failed to
  // open still made a project the tree has to show.
  it("reloads the list after a project change, even one that fails part way", async () => {
    await useApp.getState().refreshProjects();
    serverLists(ALPHA, MAIN);
    await expect(
      useApp.getState().changeProjects(async () => {
        throw new Error("opened nothing");
      }),
    ).rejects.toThrow("opened nothing");
    expect(useApp.getState().projects.map((project) => project.name)).toEqual(["alpha", "main"]);
  });

  it("hands back what the change returned", async () => {
    await expect(useApp.getState().changeProjects(async () => 42)).resolves.toBe(42);
    expect(api.getProjects).toHaveBeenCalled();
  });

  // The list is now also re-read on focus and on a timer; an answer with
  // nothing new must not hand the tree a fresh array to redraw.
  it("keeps the same list when the server's answer did not change", async () => {
    await useApp.getState().refreshProjects();
    const before = useApp.getState().projects;
    const workspacesBefore = useApp.getState().workspaces;
    await useApp.getState().refreshProjects();
    expect(useApp.getState().projects).toBe(before);
    expect(useApp.getState().workspaces).toBe(workspacesBefore);
  });
});
