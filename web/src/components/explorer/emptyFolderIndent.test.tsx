import { render, screen } from "@testing-library/react";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { beforeEach, expect, it, vi } from "vitest";
import { readPreferences } from "../../preferences/storage";
import { useApp } from "../../store";
import { NodeFolderTree } from "./NodeFolderTree";

// In a new, empty project the "Empty folder" line sat at the list's left edge,
// 40px left of the Nodes folder's name above it, so it read as belonging to
// the project. Its 27px indent was lost to `.tree-node-list.node-list li`
// (padding: 0), which outranks `.tree-empty`. It lines up with node titles
// (row padding, indent mark and icon: 36px), and in a subfolder sits one
// folder step further in, under its own folder.
const css = readFileSync(resolve(process.cwd(), "src/styles/03-explorer.css"), "utf8").replace(
  /\/\*[\s\S]*?\*\//g,
  "",
);

beforeEach(() => {
  useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
});

function tree(folders: string[]) {
  render(
    <NodeFolderTree
      folders={folders}
      folderOf={{}}
      visibleNodes={[]}
      searching={false}
      renderNode={(node) => <li key={node.id}>{node.title}</li>}
      createFolder={vi.fn()}
      renameFolder={vi.fn()}
      deleteFolder={vi.fn()}
      moveFolder={vi.fn()}
      moveNodes={vi.fn()}
    />,
  );
}

it("indents the placeholder past a node row's padding rule", () => {
  const rule = /\.tree-node-list\.node-list li\.tree-empty \{([^}]*)\}/.exec(css);
  expect(rule).not.toBeNull();
  expect(rule?.[1]).toMatch(/padding:\s*7px 8px 7px 36px/);
});

it("puts an empty subfolder's placeholder under that folder", () => {
  tree(["Drafts", "Drafts/Old"]);
  const placeholders = screen.getAllByText("Empty folder");
  // "Old" sits at depth 1, so its line is two folder steps in.
  expect(placeholders).toHaveLength(1);
  expect(placeholders[0]).toHaveStyle({ paddingInlineStart: "60px" });
});

it("leaves the root list's placeholder to the stylesheet", () => {
  tree([]);
  expect(screen.getByText("Empty folder").getAttribute("style")).toBeNull();
});
