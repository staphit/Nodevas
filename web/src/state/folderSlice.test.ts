import { beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "../api";
import { useApp } from "../store";
import { childFolders, isInsideFolder } from "./folderSlice";

vi.mock("../api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api")>();
  return {
    ...actual,
    api: {
      getNodeFolders: vi.fn(),
      createNodeFolder: vi.fn(),
      renameNodeFolder: vi.fn(),
      moveNodeFolder: vi.fn(),
      deleteNodeFolder: vi.fn(),
      moveNodesToFolder: vi.fn(),
    },
  };
});

beforeEach(() => {
  useApp.setState({ nodeFolders: [], nodeFolderOf: {} });
  vi.mocked(api.getNodeFolders).mockResolvedValue({
    folders: ["docs", "docs/specs"],
    nodes: { a: "docs" },
  });
});

describe("refreshNodeFolders", () => {
  it("shares one read between callers that ask at the same moment", async () => {
    await Promise.all([
      useApp.getState().refreshNodeFolders(),
      useApp.getState().refreshNodeFolders(),
      useApp.getState().refreshNodeFolders(),
    ]);

    expect(api.getNodeFolders).toHaveBeenCalledTimes(1);
    expect(useApp.getState().nodeFolders).toEqual(["docs", "docs/specs"]);
    expect(useApp.getState().nodeFolderOf).toEqual({ a: "docs" });
  });
});

describe("mutations re-read the layout instead of patching a local copy", () => {
  it("createNodeFolder writes then refreshes", async () => {
    vi.mocked(api.createNodeFolder).mockResolvedValue({ ok: true, path: "new" });

    await useApp.getState().createNodeFolder("new");

    expect(api.createNodeFolder).toHaveBeenCalledWith("new");
    expect(api.getNodeFolders).toHaveBeenCalled();
    expect(useApp.getState().nodeFolders).toEqual(["docs", "docs/specs"]);
  });

  it("renameNodeFolder writes then refreshes", async () => {
    vi.mocked(api.renameNodeFolder).mockResolvedValue({ ok: true, path: "docs2" });

    await useApp.getState().renameNodeFolder("docs", "docs2");

    expect(api.renameNodeFolder).toHaveBeenCalledWith("docs", "docs2");
    expect(api.getNodeFolders).toHaveBeenCalled();
  });

  it("moveNodeFolder writes then refreshes", async () => {
    vi.mocked(api.moveNodeFolder).mockResolvedValue({ ok: true, path: "docs/specs" });

    await useApp.getState().moveNodeFolder("docs/specs", "");

    expect(api.moveNodeFolder).toHaveBeenCalledWith("docs/specs", "");
    expect(api.getNodeFolders).toHaveBeenCalled();
  });

  it("deleteNodeFolder writes then refreshes", async () => {
    vi.mocked(api.deleteNodeFolder).mockResolvedValue({ ok: true });

    await useApp.getState().deleteNodeFolder("docs/specs");

    expect(api.deleteNodeFolder).toHaveBeenCalledWith("docs/specs");
    expect(api.getNodeFolders).toHaveBeenCalled();
  });

  it("moveNodesToFolder writes then refreshes", async () => {
    vi.mocked(api.moveNodesToFolder).mockResolvedValue({ ok: true });

    await useApp.getState().moveNodesToFolder(["a", "b"], "docs");

    expect(api.moveNodesToFolder).toHaveBeenCalledWith(["a", "b"], "docs");
    expect(api.getNodeFolders).toHaveBeenCalled();
  });
});

describe("childFolders", () => {
  it("lists only direct children of the root", () => {
    expect(childFolders(["docs", "docs/specs", "docs/specs/v1", "media"], "")).toEqual([
      "docs",
      "media",
    ]);
  });

  it("lists only direct children of a nested parent", () => {
    expect(childFolders(["docs", "docs/specs", "docs/specs/v1", "media"], "docs")).toEqual([
      "docs/specs",
    ]);
  });
});

describe("isInsideFolder", () => {
  it("is true for the folder itself", () => {
    expect(isInsideFolder("docs", "docs")).toBe(true);
  });

  it("is true for a descendant", () => {
    expect(isInsideFolder("docs/specs/v1", "docs")).toBe(true);
  });

  it("is false for a sibling with a shared prefix", () => {
    // "docs2" starts with "docs" as a string but is not inside it — the
    // implementation must check the "/" boundary, not just startsWith.
    expect(isInsideFolder("docs2", "docs")).toBe(false);
  });
});
