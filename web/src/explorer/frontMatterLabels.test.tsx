import { render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { explorerApi } from "../api/explorer";
import { readPreferences } from "../preferences";
import { useApp } from "../store";
import FileView from "./FileView";

afterEach(() => vi.restoreAllMocks());

// A node file's field names are the node's own fields: shown in the
// interface language, an unknown one as it is written.
describe("front matter labels", () => {
  it("names known fields in Chinese", async () => {
    useApp.setState({ preferences: { ...readPreferences(), language: "zh-TW" } });
    vi.spyOn(explorerApi, "workspaceFile").mockResolvedValue({
      path: "nodes/node-0004.md",
      size: 80,
      kind: "text",
      content: "---\nassignee: alex\npriority: high\nmood: calm\n---\n\nBody.\n",
      truncated: false,
      nodeId: "node-0004",
    });
    const { container } = render(<FileView view={{ kind: "file", source: "workspace", path: "nodes/node-0004.md" }} />);
    await screen.findByText("Body.");
    const labels = [...container.querySelectorAll(".ex-fields dt")].map((dt) => dt.textContent);
    expect(labels).toEqual(["負責人", "優先度", "mood"]);
  });
});
