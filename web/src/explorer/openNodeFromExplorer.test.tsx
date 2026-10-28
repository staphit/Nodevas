import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "../api";
import { explorerApi } from "../api/explorer";
import { readPreferences } from "../preferences";
import { useApp } from "../store";
import AgentView from "./AgentView";
import FileView from "./FileView";
import { useExplorer } from "./explorerStore";
import { agentItem, repo } from "./testFixtures";

const openNodeDetail = vi.hoisted(() => vi.fn());
vi.mock("../components/drawer/openNodeDetail", async (original) => ({
  ...(await original<typeof import("../components/drawer/openNodeDetail")>()),
  openNodeDetail,
}));

beforeEach(() => {
  useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
  openNodeDetail.mockReset();
  vi.spyOn(api, "getRunEvents").mockResolvedValue({ events: [], more: false });
  vi.spyOn(api, "listProjectRuns").mockResolvedValue({ runs: [] });
});
afterEach(() => vi.restoreAllMocks());

// The node panel lives on the board, which the Explorer's main view
// replaces: opening a node from the Explorer must bring the board back, or
// the panel opens where nobody can see it.
describe("opening a node from the Explorer", () => {
  it("from a node's file: the board comes back with the node open", async () => {
    const view = { kind: "file" as const, source: "workspace" as const, path: "nodes/node-0004.md" };
    useExplorer.setState({ view });
    vi.spyOn(explorerApi, "workspaceFile").mockResolvedValue({
      path: view.path,
      size: 40,
      kind: "text",
      content: "Send the summary.",
      truncated: false,
      nodeId: "node-0004",
    });
    render(<FileView view={view} />);
    await userEvent.click(await screen.findByRole("button", { name: "Open in node panel" }));
    expect(useExplorer.getState().view).toBeNull();
    expect(openNodeDetail).toHaveBeenCalledWith("node-0004");
  });

  it("from an agent: the board comes back with its node open", async () => {
    useExplorer.setState({ view: { kind: "agent", id: "run:r1" } });
    vi.spyOn(explorerApi, "agents").mockResolvedValue({ live: [], external: [], recent: [agentItem()] });
    vi.spyOn(explorerApi, "repos").mockResolvedValue([repo()]);
    render(<AgentView id="run:r1" />);
    await userEvent.click(await screen.findByRole("button", { name: "Open node" }));
    expect(useExplorer.getState().view).toBeNull();
    expect(openNodeDetail).toHaveBeenCalledWith(agentItem().nodeId);
  });
});

// A node file starts with YAML front matter: it is the node's fields, not
// prose, and must not render as a rule and a run-on paragraph.
describe("a node file's front matter", () => {
  it("shows as fields above the body", async () => {
    const view = { kind: "file" as const, source: "workspace" as const, path: "nodes/node-0004.md" };
    vi.spyOn(explorerApi, "workspaceFile").mockResolvedValue({
      path: view.path,
      size: 120,
      kind: "text",
      content:
        "---\nassignee: alex\nkind: task\npriority: high\ntags:\n  - daily\n  - external\ntitle: Client follow-up\n---\n\nSend the decision summary.\n",
      truncated: false,
      nodeId: "node-0004",
    });
    const { container } = render(<FileView view={view} />);
    expect(await screen.findByText("Send the decision summary.")).toBeInTheDocument();
    expect(container.querySelector(".ex-md hr")).toBeNull();
    expect(screen.queryByText(/assignee: alex/)).not.toBeInTheDocument();
    const fields = container.querySelector(".ex-fields");
    expect(fields).not.toBeNull();
    expect(fields?.textContent).toContain("Assignee");
    expect(fields?.textContent).toContain("alex");
    expect(fields?.textContent).toContain("daily, external");
    expect(fields?.textContent).toContain("Client follow-up");
  });

  it("leaves a file without front matter as it is", async () => {
    const view = { kind: "file" as const, source: "workspace" as const, path: "notes/plain.md" };
    vi.spyOn(explorerApi, "workspaceFile").mockResolvedValue({
      path: view.path,
      size: 20,
      kind: "text",
      content: "Intro\n\n---\n\nAfter a rule.",
      truncated: false,
    });
    const { container } = render(<FileView view={view} />);
    expect(await screen.findByText("After a rule.")).toBeInTheDocument();
    expect(container.querySelector(".ex-fields")).toBeNull();
    expect(container.querySelector(".ex-md hr")).not.toBeNull();
  });
});
