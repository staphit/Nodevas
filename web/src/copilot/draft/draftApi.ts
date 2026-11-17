/**
 * Everything a draft does with the server, after the copilot proposed it:
 * the dry run while it is edited, the Create (as the person, through
 * /api/graph), recording what became of it on the copilot's entry, and the
 * undo and redo of a Create.
 *
 * Components never fetch; they call these (check-api-imports holds the line).
 */

import {
  api,
  AuthError,
  type CopilotDraft,
  type CopilotDraftData,
  type CopilotDraftIssue,
  type SubgraphResult,
  type SubgraphSpec,
} from "../../api";
import { formatList, translate } from "../../i18n";
import { nodeTypesForLayout } from "../../domain/graph/nodeTypes";
import { canvasSelectionIds } from "../../state/documentSlice";
import {
  notifyBatchRevert,
  observeBatchRevert,
  peekUndo,
  pushUndo,
  type BatchRevert,
} from "../../state/undo";
import { useApp } from "../../store";
import type { Graph } from "../../types";
import { currentDraft, draftItemOf, useCopilot } from "../copilotStore";
import { sendTurn } from "../session";
import { layoutDraft, type Cell } from "./draftLayout";
import {
  draftToSpec,
  draftVia,
  existingIdOf,
  isExistingRef,
  MAX_DRAFT_NODES,
  maxNodesOf,
  planDraftToSpec,
  planVia,
  validateDraft,
  type ClientIssue,
  type ClientIssueCode,
} from "./draftStore";
import { useDraftUi } from "./draftUi";

/** At least this long after the last edit before the board is asked. */
export const CHECK_DEBOUNCE_MS = 600;

/** How long the "Created · Undo" toast stays. */
export const TOAST_MS = 10_000;

const errorText = (error: unknown) =>
  error instanceof Error ? error.message : translate("copilot.error.generic");

/** Client issues that are only worth a mark, not a refusal. */
const WARNING_CODES = new Set<ClientIssueCode>(["unknown_ref"]);

export function isBlocking(issue: ClientIssue): boolean {
  return !WARNING_CODES.has(issue.code);
}

/** The board's node ids and type ids, for the client checks. */
function boardFacts(graph: Graph | null): { ids: Set<string>; kinds: Set<string> } {
  return {
    ids: new Set((graph?.nodes ?? []).map((node) => node.id)),
    kinds: new Set(nodeTypesForLayout(graph).map((type) => type.id)),
  };
}

/** Client checks of a draft against the board on screen (`maxNodes`: its cap, maxNodesOf). */
export function checkLocally(
  draft: CopilotDraft,
  graph: Graph | null = useApp.getState().graph,
  maxNodes: number = MAX_DRAFT_NODES,
): ClientIssue[] {
  const { ids, kinds } = boardFacts(graph);
  return validateDraft(draft, graph ? ids : undefined, graph ? kinds : undefined, maxNodes);
}

/**
 * What a draft sends to the board: a coding agent's plan carries what the
 * planner wrote (tags, acceptance, priority) and the goal's links; the
 * copilot's own draft only what the card shows.
 */
export function specOf(
  draft: CopilotDraft,
  data: Pick<CopilotDraftData, "origin" | "extras"> | null | undefined,
  options: { positions?: Record<string, Cell>; via?: string } = {},
  graph: Graph | null = useApp.getState().graph,
): SubgraphSpec {
  if (data?.origin) return planDraftToSpec(draft, data, { ...options, types: nodeTypesForLayout(graph) });
  return draftToSpec(draft, options);
}

/**
 * The server's issues that still describe the draft: its errors only while
 * the person has not edited it (the client checks take over after), its
 * warnings while the node they name is still there.
 */
export function liveServerIssues(
  issues: CopilotDraftIssue[],
  draft: CopilotDraft,
  edited: boolean,
): CopilotDraftIssue[] {
  const keys = new Set(draft.nodes.map((node) => node.key));
  return issues.filter((issue) => {
    if (issue.key && !keys.has(issue.key)) return false;
    if (issue.severity === "error") return !edited;
    // A duplicate the person already linked instead is gone with its node.
    return true;
  });
}

// ---------------------------------------------------------------------------
// Dry run
// ---------------------------------------------------------------------------

const timers = new Map<string, ReturnType<typeof setTimeout>>();
const sequence = new Map<string, number>();

/** Asks the board about a draft once the person stops editing it. */
export function scheduleCheck(entryId: string): void {
  const pending = timers.get(entryId);
  if (pending) clearTimeout(pending);
  timers.set(
    entryId,
    setTimeout(() => {
      timers.delete(entryId);
      void checkNow(entryId);
    }, CHECK_DEBOUNCE_MS),
  );
}

export async function checkNow(entryId: string): Promise<void> {
  const copilot = useCopilot.getState();
  const draft = currentDraft(copilot, entryId);
  const data = draftItemOf(copilot, entryId)?.data;
  const ui = useDraftUi.getState();
  if (!draft) return;
  // The client already knows it would be refused: no round trip.
  if (checkLocally(draft, undefined, data ? maxNodesOf(data) : MAX_DRAFT_NODES).some(isBlocking)) {
    ui.setCheck(entryId, null);
    return;
  }
  const mine = (sequence.get(entryId) ?? 0) + 1;
  sequence.set(entryId, mine);
  ui.setCheck(entryId, { status: "checking" });
  try {
    await api.checkSubgraph(specOf(draft, data));
    if (sequence.get(entryId) === mine) useDraftUi.getState().setCheck(entryId, { status: "ok" });
  } catch (error) {
    if (sequence.get(entryId) !== mine) return;
    // 403 is "no access to this board": nothing to say about the draft.
    if (error instanceof AuthError) {
      useDraftUi.getState().setCheck(entryId, null);
      return;
    }
    useDraftUi.getState().setCheck(entryId, { status: "error", message: errorText(error) });
  }
}

/** Applies an edit to a draft and has the board check the result. */
export function editDraft(
  entryId: string,
  update: (draft: CopilotDraft) => CopilotDraft,
): void {
  const before = useCopilot.getState().drafts[entryId];
  useCopilot.getState().editDraft(entryId, update);
  if (useCopilot.getState().drafts[entryId] !== before) scheduleCheck(entryId);
}

// ---------------------------------------------------------------------------
// Write access
// ---------------------------------------------------------------------------

/** Asks once per project whether this person may write to it. */
export async function refreshCanWrite(project: string): Promise<void> {
  if (!project) return;
  try {
    const access = await api.projectAccess(project);
    useDraftUi.getState().setCanWrite(project, access.canWrite);
  } catch {
    // Unknown: the Create says so itself if it is refused.
  }
}

// ---------------------------------------------------------------------------
// Create, discard, undo
// ---------------------------------------------------------------------------

/** Cells the board's stored positions take, when the overlay has not drawn. */
function storedCells(graph: Graph | null): Map<string, Cell[]> {
  const cells = new Map<string, Cell[]>();
  for (const [id, point] of Object.entries(graph?.ui?.positions ?? {})) {
    if (Number.isFinite(point.x) && Number.isFinite(point.y)) {
      cells.set(id, [{ col: Math.round(point.x), row: Math.round(point.y) }]);
    }
  }
  return cells;
}

/** Where the Create puts the nodes: where the ghosts were drawn. */
export function createPositions(entryId: string, draft: CopilotDraft): Record<string, Cell> {
  const ghost = useDraftUi.getState().ghost;
  if (ghost?.entryId === entryId && draft.nodes.every((node) => ghost.cells[node.key])) {
    return Object.fromEntries(draft.nodes.map((node) => [node.key, ghost.cells[node.key]]));
  }
  const app = useApp.getState();
  return layoutDraft(draft, { occupied: storedCells(app.graph), selection: canvasSelectionIds(app) });
}

function createdIds(result: SubgraphResult): string[] {
  if (Array.isArray(result.created) && result.created.length) return result.created.map((item) => item.id);
  return Object.values(result.ids ?? {});
}

async function recordState(
  conversationId: string,
  entryId: string,
  body: { state: "open" | "committed" | "discarded"; batchId?: string; ids?: string[] },
): Promise<void> {
  try {
    const entry = await api.putCopilotDraftState(conversationId, entryId, body);
    // The answer is the stored entry; on screen only while that conversation is.
    if (entry && entry.id === entryId && useCopilot.getState().activeId === conversationId) {
      useCopilot.getState().applyEvent({ type: "entry", entry });
    }
  } catch (error) {
    useCopilot.getState().setNotice(errorText(error));
  }
}

/**
 * Whether the draft can be created now: open, nothing the checks refuse
 * (client checks, the server's own while unedited, the newest dry run), this
 * person may write, and no Create or Discard is on its way. The card's
 * primary button and Enter in an empty composer both ask this.
 */
export function canCreate(entryId: string): boolean {
  const copilot = useCopilot.getState();
  const item = draftItemOf(copilot, entryId);
  const draft = currentDraft(copilot, entryId);
  if (!item?.data || item.data.state !== "open" || !draft) return false;
  const ui = useDraftUi.getState();
  const app = useApp.getState();
  if (ui.busy[entryId]) return false;
  if (app.graphUnreadable !== null) return false;
  if (ui.canWrite?.project === app.activeProject && !ui.canWrite.value) return false;
  if (ui.checks[entryId]?.status === "error") return false;
  if (checkLocally(draft, app.graph, maxNodesOf(item.data)).some(isBlocking)) return false;
  const edited = copilot.drafts[entryId] !== undefined;
  return !liveServerIssues(item.data.issues ?? [], draft, edited).some((issue) => issue.severity === "error");
}

/**
 * Creates the draft's nodes where the ghosts were, as the person, then
 * records it on the draft, selects the new nodes and offers an undo.
 */
export async function createDraft(entryId: string): Promise<boolean> {
  const copilot = useCopilot.getState();
  const item = draftItemOf(copilot, entryId);
  const draft = currentDraft(copilot, entryId);
  const conversationId = copilot.activeId;
  const ui = useDraftUi.getState();
  if (!item?.data || !draft || !conversationId || !canCreate(entryId)) return false;
  ui.setBusy(entryId, "creating");
  let result: SubgraphResult;
  try {
    const positions = createPositions(entryId, draft);
    const via = item.data.origin ? planVia(entryId) : draftVia(entryId);
    result = await api.createSubgraph(specOf(draft, item.data, { positions, via }));
  } catch (error) {
    const project = useApp.getState().activeProject;
    if (error instanceof AuthError && error.status === 403) {
      useDraftUi.getState().setCanWrite(project, false);
    }
    useDraftUi.getState().showToast({
      kind: "error",
      text: translate("draft.createFailed", undefined, { message: errorText(error) }),
      at: Date.now(),
    });
    return false;
  } finally {
    useDraftUi.getState().setBusy(entryId, null);
  }
  const ids = createdIds(result);
  const undoable = Boolean(result.undoable && result.batchId);
  useCopilot.getState().setDraftData(entryId, {
    state: "committed",
    batchId: result.batchId,
    createdIds: ids,
  });
  if (undoable) {
    pushUndo({ kind: "batch", batchId: result.batchId!, ids, entryId, conversationId });
  }
  const app = useApp.getState();
  app.selectOnCanvas(ids);
  void app.loadAll().catch(() => undefined);
  useDraftUi.getState().showToast({ kind: "created", entryId, count: ids.length, undoable, at: Date.now() });
  await recordState(conversationId, entryId, {
    state: "committed",
    ...(result.batchId ? { batchId: result.batchId } : {}),
    ids,
  });
  return true;
}

export async function discardDraft(entryId: string): Promise<void> {
  const copilot = useCopilot.getState();
  const conversationId = copilot.activeId;
  if (!conversationId || useDraftUi.getState().busy[entryId]) return;
  useDraftUi.getState().setBusy(entryId, "discarding");
  copilot.setDraftData(entryId, { state: "discarded" });
  try {
    await recordState(conversationId, entryId, { state: "discarded" });
  } finally {
    useDraftUi.getState().setBusy(entryId, null);
  }
}

/**
 * The toast's Undo. Through the undo stack while the Create is its newest
 * step, so Ctrl+Z and the button stay one history; directly otherwise.
 */
export async function undoDraftCreate(entryId: string): Promise<void> {
  useDraftUi.getState().showToast(null);
  const top = peekUndo();
  if (top?.kind === "batch" && top.entryId === entryId && !top.undone) {
    await useApp.getState().undoLast().catch(() => undefined);
    return;
  }
  const item = draftItemOf(useCopilot.getState(), entryId);
  const batchId = item?.data?.batchId;
  if (!batchId) return;
  try {
    const result = await api.undoBatch(batchId);
    notifyBatchRevert({
      direction: "undo",
      entry: {
        kind: "batch",
        batchId,
        ids: item.data?.createdIds ?? [],
        entryId,
        conversationId: useCopilot.getState().activeId ?? undefined,
      },
      trashed: result.trashed ?? [],
      refused: result.refused ?? [],
    });
    void useApp.getState().loadAll().catch(() => undefined);
  } catch (error) {
    useDraftUi.getState().showToast({ kind: "error", text: errorText(error), at: Date.now() });
  }
}

/** A Create undone or redone (Ctrl+Z, the toast): the draft follows. */
function followBatch(revert: BatchRevert): void {
  const { entry } = revert;
  if (!entry.entryId || !entry.conversationId) return;
  const copilot = useCopilot.getState();
  if (revert.direction === "undo") {
    if (copilot.activeId === entry.conversationId) {
      copilot.setDraftData(entry.entryId, { state: "open", batchId: undefined, createdIds: undefined });
    }
    useDraftUi.getState().showToast({
      kind: "undone",
      count: revert.trashed.length,
      refused: revert.refused.length,
      at: Date.now(),
    });
    void recordState(entry.conversationId, entry.entryId, { state: "open" });
  } else {
    if (copilot.activeId === entry.conversationId) {
      copilot.setDraftData(entry.entryId, {
        state: "committed",
        batchId: entry.batchId,
        createdIds: revert.restored,
      });
    }
    void recordState(entry.conversationId, entry.entryId, {
      state: "committed",
      batchId: entry.batchId,
      ids: revert.restored,
    });
  }
}

let unobserve: (() => void) | null = null;

/** Starts following batch undos; the panel calls it once. */
export function followDraftUndo(): void {
  unobserve ??= observeBatchRevert(followBatch);
}

followDraftUndo();

// ---------------------------------------------------------------------------
// The deeper path: the coding agent plans it
// ---------------------------------------------------------------------------

/** One line per node, for the coding agent's brief. */
export function draftOutline(draft: CopilotDraft, titleOf: (id: string) => string): string {
  const titles = new Map(draft.nodes.map((node) => [node.key, node.title]));
  const name = (ref: string) =>
    isExistingRef(ref) ? titleOf(existingIdOf(ref)) || existingIdOf(ref) : (titles.get(ref) ?? ref);
  return draft.nodes
    .map((node, index) => {
      const after = (node.after ?? []).map(name);
      return `${index + 1}. ${node.title}${after.length ? ` ← ${formatList(after)}` : ""}`;
    })
    .join("\n");
}

/**
 * Hands the draft to the coding agent: an Ask turn asking the copilot to
 * start a breakdown with it (the chat model confirms before it starts).
 */
export function planWithAgent(entryId: string): Promise<void> {
  const draft = currentDraft(useCopilot.getState(), entryId);
  if (!draft) return Promise.resolve();
  const nodes = useApp.getState().graph?.nodes ?? [];
  const titleOf = (id: string) => nodes.find((node) => node.id === id)?.title ?? "";
  const text = translate("draft.deeperPrompt", undefined, {
    summary: draft.summary || draftItemOf(useCopilot.getState(), entryId)?.text || "",
    list: draftOutline(draft, titleOf),
  });
  return sendTurn(text, "text");
}
