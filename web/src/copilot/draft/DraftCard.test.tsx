import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { api, type CopilotDraftData } from "../../api";
import { readPreferences } from "../../preferences";
import { clearUndo, peekUndo } from "../../state/undo";
import { useApp } from "../../store";
import { currentDraft, entryToItem, useCopilot, type DraftItem } from "../copilotStore";
import { DraftCard } from "./DraftCard";
import { DraftToast } from "./DraftToast";
import { useDraftUi } from "./draftUi";

vi.mock("../../api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../../api")>();
  return {
    ...actual,
    api: {
      checkSubgraph: vi.fn(),
      createSubgraph: vi.fn(),
      projectAccess: vi.fn(),
      putCopilotDraftState: vi.fn(),
      getGraph: vi.fn(),
      getState: vi.fn(),
      getTrash: vi.fn(),
    },
  };
});

const base: CopilotDraftData = {
  draft: {
    summary: "Launch",
    questions: ["Which date?"],
    nodes: [
      { key: "docs", title: "Write docs", kind: "task" },
      { key: "ship", title: "Ship", kind: "task", after: ["docs"] },
    ],
  },
  issues: [],
  provider: "anthropic",
  model: "claude-x",
  state: "open",
};

function item(data: CopilotDraftData = base): DraftItem {
  const value = entryToItem({ id: "d1", seq: 1, kind: "draft", text: "Launch", data: JSON.stringify(data), createdAt: 1 });
  return value as DraftItem;
}

function show(data: CopilotDraftData = base, props: { themed?: boolean; prompt?: string } = {}) {
  const draftItem = item(data);
  useCopilot.setState({ items: [draftItem] });
  return render(<DraftCard item={draftItem} active themed={props.themed ?? false} prompt={props.prompt} />);
}

beforeEach(() => {
  clearUndo();
  useApp.setState({
    preferences: { ...readPreferences(), language: "en" },
    activeProject: "main",
    graph: { version: 1, nodes: [{ id: "node-1", title: "Docs page" }], edges: [], ui: {} },
    graphUnreadable: null,
    canvasSelection: { project: "main", ids: [] },
  });
  useCopilot.setState({ items: [], drafts: {}, activeId: "c1", running: false, composerFill: null });
  useDraftUi.setState({ ghost: null, checks: {}, busy: {}, canWrite: { project: "main", value: true }, toast: null });
  vi.mocked(api.projectAccess).mockResolvedValue({ member: "editor", admin: false, canWrite: true, canManage: false } as never);
  vi.mocked(api.checkSubgraph).mockResolvedValue({ ok: true, ids: {}, created: [], rev: "r", issues: [], dependencies: 1, references: 0 });
  vi.mocked(api.getGraph).mockResolvedValue({ graph: { version: 1, nodes: [], edges: [], ui: {} }, rev: "r", statuses: {}, issues: null });
  vi.mocked(api.getState).mockResolvedValue({ state: { nodes: {}, history: [] }, statuses: {} });
  vi.mocked(api.getTrash).mockResolvedValue({ items: [] });
});

const createButton = () => screen.getByRole("button", { name: /^Create \d+ nodes$/ });

describe("DraftCard", () => {
  it("lists the nodes with the header, the model and what each waits for", () => {
    show();
    expect(screen.getByText("Draft: 2 nodes, 1 links")).toBeInTheDocument();
    expect(screen.getByText("claude-x")).toBeInTheDocument();
    expect(screen.getByText("Write docs")).toBeInTheDocument();
    expect(screen.getByText("After Write docs")).toBeInTheDocument();
    expect(createButton()).toBeEnabled();
  });

  it("says issues by code, never the model's English, and offers link-instead for a duplicate", () => {
    show({
      ...base,
      issues: [
        {
          key: "docs",
          severity: "warning",
          code: "duplicate_existing",
          message: "MODEL-ONLY TEXT",
          existingId: "node-1",
          title: "Docs page",
        },
      ],
    });
    expect(screen.queryByText(/MODEL-ONLY/)).toBeNull();
    expect(screen.getByText('Maybe the same as "Docs page". Link to it instead?')).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Link instead" }));
    const draft = currentDraft(useCopilot.getState(), "d1")!;
    expect(draft.nodes.map((node) => node.key)).toEqual(["ship"]);
    expect(draft.nodes[0].after).toEqual(["existing:node-1"]);
  });

  it("disables Create on an error, and again once an edit makes a cycle", () => {
    const { unmount } = show({
      ...base,
      issues: [{ key: "ship", severity: "error", code: "cycle", message: "x" }],
    });
    expect(createButton()).toBeDisabled();
    expect(screen.getAllByText("These steps wait for each other").length).toBeGreaterThan(0);
    unmount();
    show();
    act(() => {
      useCopilot.getState().editDraft("d1", (draft) => ({
        ...draft,
        nodes: draft.nodes.map((node) => (node.key === "docs" ? { ...node, after: ["ship"] } : node)),
      }));
    });
    expect(createButton()).toBeDisabled();
  });

  it("disables Create without write access, and says so", () => {
    useDraftUi.setState({ canWrite: { project: "main", value: false } });
    show();
    expect(createButton()).toBeDisabled();
    expect(screen.getByText("You can't write to this project")).toBeInTheDocument();
  });

  it("puts a question into the composer", () => {
    show();
    fireEvent.click(screen.getByRole("button", { name: "Which date?" }));
    expect(useCopilot.getState().composerFill?.text).toBe("Which date?");
  });

  it("removes a node, bridging its dependents", () => {
    show();
    fireEvent.click(screen.getByRole("button", { name: "Remove Write docs" }));
    expect(currentDraft(useCopilot.getState(), "d1")!.nodes).toEqual([
      { key: "ship", title: "Ship", kind: "task", after: [] },
    ]);
  });

  it("suggests the coding agent for a request that names code", () => {
    show(base, { prompt: "split web/src/App.tsx" });
    expect(screen.getByRole("button", { name: /Plan with my coding agent/ })).toHaveClass("is-suggested");
  });

  it("folds to a row of kind glyphs with a theme pack, and opens the list on demand", () => {
    const { container } = show(base, { themed: true });
    expect(container.querySelectorAll(".draft-glyph-row .draft-kind-glyph")).toHaveLength(2);
    expect(screen.queryByText("Write docs")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Show list" }));
    expect(screen.getByText("Write docs")).toBeInTheDocument();
  });

  it("creates the nodes where the ghosts were, records it and offers an undo", async () => {
    useDraftUi.setState({ ghost: { entryId: "d1", cells: { docs: { col: 4, row: 0 }, ship: { col: 5, row: 0 } } } });
    vi.mocked(api.createSubgraph).mockResolvedValue({
      ok: true,
      ids: { docs: "node-2", ship: "node-3" },
      created: [
        { key: "docs", id: "node-2", title: "Write docs", kind: "task" },
        { key: "ship", id: "node-3", title: "Ship", kind: "task" },
      ],
      rev: "r2",
      issues: [],
      dependencies: 1,
      references: 0,
      batchId: "b1",
      undoable: true,
    });
    vi.mocked(api.putCopilotDraftState).mockResolvedValue({
      id: "d1",
      seq: 1,
      kind: "draft",
      text: "Launch",
      data: JSON.stringify({ ...base, state: "committed", batchId: "b1", createdIds: ["node-2", "node-3"] }),
      createdAt: 1,
    });
    show();
    render(<DraftToast />);
    fireEvent.click(createButton());
    await waitFor(() => expect(api.putCopilotDraftState).toHaveBeenCalled());
    expect(api.createSubgraph).toHaveBeenCalledWith({
      nodes: [
        { key: "docs", title: "Write docs", kind: "task" },
        { key: "ship", title: "Ship", kind: "task", dependsOn: ["docs"] },
      ],
      positions: { docs: { col: 4, row: 0 }, ship: { col: 5, row: 0 } },
      batch: { via: "copilot-draft:d1" },
    });
    expect(api.putCopilotDraftState).toHaveBeenCalledWith("c1", "d1", {
      state: "committed",
      batchId: "b1",
      ids: ["node-2", "node-3"],
    });
    expect(peekUndo()).toMatchObject({ kind: "batch", batchId: "b1", ids: ["node-2", "node-3"], entryId: "d1" });
    expect(useApp.getState().canvasSelectRequest?.ids).toEqual(["node-2", "node-3"]);
    expect(screen.getByText("Created 2 nodes")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Undo" })).toBeInTheDocument();
  });
});
