import { describe, expect, it } from "vitest";
import { reorderProjectNames, sortProjectTree } from "./sortProjects";
import type { ProjectEntry } from "../../state/types";

function entry(
  name: string,
  label: string,
  nodes = 0,
  isFolder = false,
): ProjectEntry {
  const slash = name.lastIndexOf("/");
  return {
    name,
    label,
    parent: slash < 0 ? "" : name.slice(0, slash),
    depth: name.split("/").length - 1,
    path: `/w/${name}`,
    nodes,
    ...(isFolder ? { isFolder: true } : {}),
  };
}

const tree: ProjectEntry[] = [
  entry("Story", "Story", 13),
  entry("Story/b", "beta", 2),
  entry("Story/a", "alpha", 9),
  entry("Game", "Game", 12, true),
];

describe("sortProjectTree", () => {
  it("sorts siblings by name and keeps children under their parent", () => {
    expect(sortProjectTree(tree, "name").map((item) => item.name)).toEqual([
      "Game",
      "Story",
      "Story/a",
      "Story/b",
    ]);
  });

  it("reverses only within a level", () => {
    expect(sortProjectTree(tree, "name-desc").map((item) => item.name)).toEqual([
      "Story",
      "Story/b",
      "Story/a",
      "Game",
    ]);
  });

  it("sorts by node count, most first", () => {
    expect(sortProjectTree(tree, "nodes").map((item) => item.name)).toEqual([
      "Story",
      "Story/a",
      "Story/b",
      "Game",
    ]);
  });

  it("puts folders first when asked", () => {
    expect(sortProjectTree(tree, "kind")[0].name).toBe("Game");
  });

  it("keeps an orphan whose parent is not in the list", () => {
    const orphan = entry("Ghost/child", "child");
    const result = sortProjectTree([...tree, orphan], "name");
    expect(result.map((item) => item.name)).toContain("Ghost/child");
  });

  it("follows the stored order at every level", () => {
    const order = ["Story", "Story/b", "Story/a", "Game"];
    expect(sortProjectTree(tree, "manual", order).map((item) => item.name)).toEqual([
      "Story",
      "Story/b",
      "Story/a",
      "Game",
    ]);
  });

  it("appends projects the stored order never mentions, by name", () => {
    const extra = [...tree, entry("Zeta", "zeta"), entry("Alpha", "alpha")];
    expect(
      sortProjectTree(extra, "manual", ["Story", "Game"]).map((item) => item.name),
    ).toEqual(["Story", "Story/a", "Story/b", "Game", "Alpha", "Zeta"]);
  });

  it("ignores a stored name that no longer exists", () => {
    const order = ["Deleted", "Game", "Story"];
    expect(sortProjectTree(tree, "manual", order).map((item) => item.name)).toEqual([
      "Game",
      "Story",
      "Story/a",
      "Story/b",
    ]);
  });
});

describe("reorderProjectNames", () => {
  const ordered = sortProjectTree(tree, "name");

  it("moves a project among its siblings and keeps the rest of the tree", () => {
    expect(reorderProjectNames(ordered, "Story/b", "Story/a", true)).toEqual([
      "Game",
      "Story",
      "Story/b",
      "Story/a",
    ]);
  });

  it("places after the target when asked", () => {
    expect(reorderProjectNames(ordered, "Game", "Story", false)).toEqual([
      "Story",
      "Story/a",
      "Story/b",
      "Game",
    ]);
  });

  it("refuses to move a project next to a row on another level", () => {
    expect(reorderProjectNames(ordered, "Game", "Story/a", true)).toEqual(
      ordered.map((item) => item.name),
    );
  });
});
