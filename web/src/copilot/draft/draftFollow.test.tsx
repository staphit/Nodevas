import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { api, ConflictError, type CopilotDraftData } from "../../api";
import { readPreferences } from "../../preferences";
import { clearUndo, peekUndo, pushUndo, redoDepth, undoDepth } from "../../state/undo";
import { useApp } from "../../store";
import { entryToItem, useCopilot, type DraftItem } from "../copilotStore";
import { projectChanged } from "../session";
import { canCreate, checkNow, createDraft, discardDraft, editDraft, undoDraftCreate } from "./draftApi";
import { DraftCard } from "./DraftCard";
import { renameNode } from "./draftStore";
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
      undoBatch: vi.fn(),
      redoBatch: vi.fn(),
      deleteNode: vi.fn(),
      deleteNodes: vi.fn(),
      getGraph: vi.fn(),
      getState: vi.fn(),
      getTrash: vi.fn(),
      listCopilotConversations: vi.fn(),
      listRunners: vi.fn(),
    },
  };
});

const base: CopilotDraftData = {
  draft: {
    summary: "Launch",
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

const committed: CopilotDraftData = { ...base, state: "committed", batchId: "b1", createdIds: ["n1", "n2"] };

function item(data: CopilotDraftData = base): DraftItem {
  return entryToItem({ id: "d1", seq: 1, kind: "draft", text: "Launch", data: JSON.stringify(data), createdAt: 1 }) as DraftItem;
}

const draftData = () => useCopilot.getState().items.find((entry) => entry.id === "d1") as DraftItem;

const created = {
  ok: true,
  ids: { docs: "n1", ship: "n2" },
  created: [
    { key: "docs", id: "n1", title: "Write docs", kind: "task" },
    { key: "ship", id: "n2", title: "Ship", kind: "task" },
  ],
  rev: "r2",
  issues: [],
  dependencies: 1,
  references: 0,
  batchId: "b1",
  undoable: true,
};

beforeEach(() => {
  vi.clearAllMocks();
  clearUndo();
  useApp.setState({
    preferences: { ...readPreferences(), language: "en" },
    activeProject: "main",
    graph: {
      version: 1,
      nodes: [
        { id: "n1", title: "Write docs" },
        { id: "n2", title: "Ship" },
      ],
      edges: [],
      ui: {},
    },
    graphRev: "r",
    graphUnreadable: null,
    canvasSelection: { project: "main", ids: [] },
    tabs: [],
    activeTab: null,
  });
  useCopilot.setState({ items: [item(committed)], drafts: {}, activeId: "c1", running: false, refining: null });
  useDraftUi.setState({ ghost: null, checks: {}, busy: {}, canWrite: { project: "main", value: true }, toast: null, kept: {} });
  vi.mocked(api.putCopilotDraftState).mockResolvedValue(null as never);
  vi.mocked(api.projectAccess).mockResolvedValue({ member: "editor", admin: false, canWrite: true, canManage: false } as never);
  vi.mocked(api.checkSubgraph).mockResolvedValue({ ok: true, ids: {}, created: [], rev: "r", issues: [], dependencies: 1, references: 0 });
  vi.mocked(api.getGraph).mockResolvedValue({ graph: useApp.getState().graph!, rev: "r", statuses: {}, issues: null });
  vi.mocked(api.getState).mockResolvedValue({ state: { nodes: {}, history: [] }, statuses: {} });
  vi.mocked(api.getTrash).mockResolvedValue({ items: [] });
  vi.mocked(api.listCopilotConversations).mockResolvedValue([]);
  vi.mocked(api.listRunners).mockResolvedValue({ runners: [] });
  vi.mocked(api.deleteNode).mockResolvedValue({ ok: true, trashFile: "t1" } as never);
});

const pushCreate = () =>
  pushUndo({ kind: "batch", batchId: "b1", ids: ["n1", "n2"], entryId: "d1", conversationId: "c1" });

describe("undo of a draft's Create (findings 3, 17, 24)", () => {
  it("reopens the draft only when every created node was trashed", async () => {
    vi.mocked(api.undoBatch).mockResolvedValue({ ok: true, trashed: ["n1", "n2"], refused: [] });
    pushCreate();
    await useApp.getState().undoLast();
    expect(draftData().data?.state).toBe("open");
    expect(api.putCopilotDraftState).toHaveBeenCalledWith("c1", "d1", { state: "open" });
    expect(redoDepth()).toBe(1);
  });

  it("keeps a partly undone draft committed, names the kept nodes and offers Delete anyway", async () => {
    vi.mocked(api.undoBatch).mockResolvedValue({
      ok: true,
      trashed: ["n1"],
      refused: [{ id: "n2", code: "changed", reason: "edited" }],
    });
    pushCreate();
    render(<DraftToast />);
    await act(async () => {
      await useApp.getState().undoLast();
    });
    expect(draftData().data?.state).toBe("committed");
    expect(api.putCopilotDraftState).not.toHaveBeenCalledWith("c1", "d1", expect.objectContaining({ state: "open" }));
    expect(api.putCopilotDraftState).toHaveBeenCalledWith("c1", "d1", { state: "committed", batchId: "b1", ids: ["n2"] });
    // The redo brings back what was trashed.
    expect(redoDepth()).toBe(1);
    expect(screen.getByText("Removed 1 created node; kept 1:")).toBeInTheDocument();
    expect(screen.getByText("Ship")).toBeInTheDocument();
    expect(screen.getByText("changed since")).toBeInTheDocument();

    // The card says it too, and stays folded as created.
    render(<DraftCard item={draftData()} active={false} themed={false} />);
    expect(screen.getByText("Kept after the undo:")).toBeInTheDocument();

    fireEvent.click(screen.getAllByRole("button", { name: "Delete Ship anyway" })[0]);
    await waitFor(() => expect(api.deleteNode).toHaveBeenCalledWith("n2"));
    await waitFor(() => expect(useDraftUi.getState().kept.d1 ?? []).toEqual([]));
  });

  it("when every node is refused: no redo entry, the draft stays committed, and the kept nodes are listed", async () => {
    vi.mocked(api.undoBatch).mockResolvedValue({
      ok: true,
      trashed: [],
      refused: [
        { id: "n1", code: "claimed", reason: "claimed" },
        { id: "n2", code: "busy", reason: "busy" },
      ],
    });
    pushCreate();
    render(<DraftToast />);
    await act(async () => {
      await useApp.getState().undoLast();
    });
    expect(redoDepth()).toBe(0);
    expect(undoDepth()).toBe(0);
    expect(draftData().data?.state).toBe("committed");
    expect(draftData().data?.createdIds).toEqual(["n1", "n2"]);
    expect(api.putCopilotDraftState).not.toHaveBeenCalled();
    expect(screen.getByText("claimed by someone")).toBeInTheDocument();
    expect(screen.getByText("an agent is working on it")).toBeInTheDocument();
  });

  it("a redo that restores nothing leaves the draft as it was", async () => {
    vi.mocked(api.undoBatch).mockResolvedValue({ ok: true, trashed: ["n1", "n2"], refused: [] });
    vi.mocked(api.redoBatch).mockResolvedValue({ ok: true, restored: [], skipped: [], links: 0 });
    pushCreate();
    await useApp.getState().undoLast();
    vi.mocked(api.putCopilotDraftState).mockClear();
    await useApp.getState().redoLast();
    expect(draftData().data?.state).toBe("open");
    expect(api.putCopilotDraftState).not.toHaveBeenCalled();
  });

  it("a redo after a partial undo adds the restored nodes back to the kept ones", async () => {
    vi.mocked(api.undoBatch).mockResolvedValue({
      ok: true,
      trashed: ["n1"],
      refused: [{ id: "n2", code: "changed", reason: "edited" }],
    });
    vi.mocked(api.redoBatch).mockResolvedValue({ ok: true, restored: ["n1"], skipped: [], links: 0 });
    pushCreate();
    await useApp.getState().undoLast();
    await useApp.getState().redoLast();
    expect(draftData().data).toMatchObject({ state: "committed", batchId: "b1" });
    expect([...(draftData().data?.createdIds ?? [])].sort()).toEqual(["n1", "n2"]);
    expect(useDraftUi.getState().kept.d1).toBeUndefined();
  });
});

describe("the toast's Undo (finding 22)", () => {
  it("carries the batch and conversation, and goes when the project changes", async () => {
    useCopilot.setState({ items: [item(base)] });
    vi.mocked(api.createSubgraph).mockResolvedValue(created);
    await createDraft("d1");
    expect(useDraftUi.getState().toast).toMatchObject({ kind: "created", batchId: "b1", conversationId: "c1" });
    projectChanged();
    expect(useDraftUi.getState().toast).toBeNull();
  });

  it("undoes directly when the Create is not the newest step, and takes it off the undo stack", async () => {
    vi.mocked(api.undoBatch).mockResolvedValue({ ok: true, trashed: ["n1", "n2"], refused: [] });
    pushCreate();
    pushUndo({ kind: "create-node", ids: ["n9"] });
    await undoDraftCreate({ kind: "created", entryId: "d1", count: 2, undoable: true, at: 1, batchId: "b1", conversationId: "c1" });
    expect(api.undoBatch).toHaveBeenCalledWith("b1");
    expect(undoDepth()).toBe(1);
    expect(peekUndo()).toMatchObject({ kind: "create-node" });
    expect(draftData().data?.state).toBe("open");
  });

  it("works from the toast alone when the transcript no longer shows the draft", async () => {
    vi.mocked(api.undoBatch).mockResolvedValue({ ok: true, trashed: ["n1", "n2"], refused: [] });
    useCopilot.setState({ items: [], activeId: "c2" });
    await undoDraftCreate({ kind: "created", entryId: "d1", count: 2, undoable: true, at: 1, batchId: "b1", conversationId: "c1" });
    expect(api.undoBatch).toHaveBeenCalledWith("b1");
    expect(api.putCopilotDraftState).toHaveBeenCalledWith("c1", "d1", { state: "open" });
  });

  it("says so when nothing could be undone", async () => {
    vi.mocked(api.undoBatch).mockResolvedValue({ ok: true, trashed: [], refused: [] });
    pushCreate();
    pushUndo({ kind: "create-node", ids: ["n9"] });
    await undoDraftCreate({ kind: "created", entryId: "d1", count: 2, undoable: true, at: 1, batchId: "b1", conversationId: "c1" });
    expect(useDraftUi.getState().toast).toMatchObject({ kind: "error" });
    expect(draftData().data?.state).toBe("committed");
  });
});

describe("the person's final draft is kept (finding 20, C2)", () => {
  it("sends the edited draft with committed, and again when an undo reopens it", async () => {
    useCopilot.setState({ items: [item(base)] });
    useCopilot.getState().editDraft("d1", (draft) => renameNode(draft, "docs", "Write the guide"));
    vi.mocked(api.createSubgraph).mockResolvedValue(created);
    await createDraft("d1");
    const edited = useCopilot.getState().drafts.d1;
    expect(api.putCopilotDraftState).toHaveBeenCalledWith("c1", "d1", {
      state: "committed",
      batchId: "b1",
      ids: ["n1", "n2"],
      draft: edited,
    });
    vi.mocked(api.undoBatch).mockResolvedValue({ ok: true, trashed: ["n1", "n2"], refused: [] });
    await useApp.getState().undoLast();
    expect(api.putCopilotDraftState).toHaveBeenLastCalledWith("c1", "d1", { state: "open", draft: edited });
  });

  it("treats a draft the server stored as edited as edited: its own errors no longer block", () => {
    useCopilot.setState({
      items: [
        item({
          ...base,
          edited: true,
          issues: [{ key: "ship", severity: "error", code: "cycle", message: "x" }],
        }),
      ],
    });
    expect(canCreate("d1")).toBe(true);
  });
});

describe("one live commit per draft (finding 21, C3)", () => {
  it("on BATCH_VIA_TAKEN marks the draft created with that batch, without an undo, and creates nothing", async () => {
    useCopilot.setState({ items: [item(base)] });
    vi.mocked(api.createSubgraph).mockRejectedValue(
      new ConflictError("", "", "already created", "BATCH_VIA_TAKEN", { batchId: "b0" }),
    );
    render(<DraftToast />);
    await act(async () => {
      await createDraft("d1");
    });
    expect(draftData().data).toMatchObject({ state: "committed", batchId: "b0" });
    expect(api.putCopilotDraftState).toHaveBeenCalledWith("c1", "d1", { state: "committed", batchId: "b0" });
    expect(peekUndo()).toBeUndefined();
    expect(screen.getByText("Created 2 nodes")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Undo" })).toBeNull();
  });
});

describe("the model label in the audit trail (C4)", () => {
  it("sends provider · model with the batch", async () => {
    useCopilot.setState({ items: [item(base)] });
    vi.mocked(api.createSubgraph).mockResolvedValue(created);
    await createDraft("d1");
    expect(vi.mocked(api.createSubgraph).mock.calls[0][0].batch).toEqual({
      via: "copilot-draft:d1",
      label: "anthropic · claude-x",
    });
  });

  it("sends agent · model for a plan draft", async () => {
    useCopilot.setState({
      items: [
        item({
          ...base,
          provider: "",
          model: "",
          origin: { runId: "r7", goalId: "n1", agent: "claude", model: "sonnet" },
        }),
      ],
    });
    vi.mocked(api.createSubgraph).mockResolvedValue(created);
    await createDraft("d1");
    expect(vi.mocked(api.createSubgraph).mock.calls[0][0].batch).toEqual({
      via: "copilot-plan:d1",
      label: "claude · sonnet",
    });
  });
});

describe("a draft being refined (finding 19)", () => {
  it("takes no edits from any surface (card or board) while its refine runs", () => {
    useCopilot.setState({ items: [item(base)], running: true, refining: "d1" });
    editDraft("d1", (draft) => renameNode(draft, "docs", "Lost"));
    expect(useCopilot.getState().drafts.d1).toBeUndefined();
    useCopilot.setState({ running: false, refining: null });
    editDraft("d1", (draft) => renameNode(draft, "docs", "Kept"));
    expect(useCopilot.getState().drafts.d1?.nodes[0].title).toBe("Kept");
  });

  it("cannot be discarded while its refine runs", async () => {
    useCopilot.setState({ items: [item(base)], running: true, refining: "d1" });
    await discardDraft("d1");
    expect(draftData().data?.state).toBe("open");
    expect(api.putCopilotDraftState).not.toHaveBeenCalled();
  });
});

describe("edits survive a follow-up (finding 18)", () => {
  it("drops a dry-run answer once its draft's edits are gone", async () => {
    useCopilot.setState({ items: [item(base)] });
    useCopilot.getState().editDraft("d1", (draft) => renameNode(draft, "docs", "Write the guide"));
    useDraftUi.getState().setCheck("d1", { status: "error", message: "no" });
    useCopilot.getState().showConversation(null, []);
    expect(useDraftUi.getState().checks.d1).toBeUndefined();
  });

  it("a dry run that answers after the edits are gone records nothing", async () => {
    useCopilot.setState({ items: [item(base)] });
    useCopilot.getState().editDraft("d1", (draft) => renameNode(draft, "docs", "Write the guide"));
    let answer: (value: unknown) => void = () => undefined;
    vi.mocked(api.checkSubgraph).mockReturnValue(new Promise((resolve) => (answer = resolve)) as never);
    const running = checkNow("d1");
    useCopilot.setState({ drafts: {} });
    answer({ ok: true });
    await running;
    expect(useDraftUi.getState().checks.d1).toBeUndefined();
  });
});
