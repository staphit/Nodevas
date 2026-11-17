import { beforeEach, describe, expect, it } from "vitest";
import type { CopilotDraftData, CopilotEntry } from "../api";
import {
  activeDraftId,
  currentDraft,
  entryToItem,
  makePlaceholderKey,
  parseDraftData,
  reduceTurnEvent,
  refinableDraftId,
  useCopilot,
  type TurnState,
} from "./copilotStore";
import { renameNode } from "./draft/draftStore";

const data = (state: CopilotDraftData["state"] = "open"): CopilotDraftData => ({
  draft: {
    summary: "Launch",
    nodes: [
      { key: "a", title: "Docs", kind: "task" },
      { key: "b", title: "Ship", kind: "task", after: ["a"] },
    ],
  },
  issues: [],
  provider: "anthropic",
  model: "claude",
  state,
});

const entry = (id: string, state: CopilotDraftData["state"] = "open"): CopilotEntry => ({
  id,
  seq: 2,
  kind: "draft",
  text: "Launch",
  data: JSON.stringify(data(state)),
  createdAt: 1,
});

const empty: TurnState = { items: [], pending: [], running: true, turnId: "t", lastReply: null, draftStep: null };

beforeEach(() => {
  useCopilot.setState({ items: [], drafts: {}, draftStep: null, activeId: "c1", running: false });
});

describe("draft entries", () => {
  it("become draft items with their data parsed", () => {
    const item = entryToItem(entry("e1"));
    expect(item).toMatchObject({ kind: "draft", id: "e1", text: "Launch" });
    expect(item.kind === "draft" && item.data?.draft.nodes).toHaveLength(2);
    expect(entryToItem({ ...entry("e2"), data: "{oops" })).toMatchObject({ kind: "draft", data: null });
  });

  it("follows the draft_status steps and clears them when the turn ends", () => {
    let state = { ...empty, ...reduceTurnEvent(empty, { type: "draft_status", code: "reading" }) };
    expect(state.draftStep).toBe("reading");
    state = { ...state, ...reduceTurnEvent(state, { type: "draft_status", code: "drafting" }) };
    state = { ...state, ...reduceTurnEvent(state, { type: "entry", entry: entry("e1") }) };
    expect(state.items.map((item) => item.kind)).toEqual(["draft"]);
    state = { ...state, ...reduceTurnEvent(state, { type: "turn_finished", stop: "end_turn" }) };
    expect(state.draftStep).toBeNull();
  });

  it("says a draft error by its code, not the English message", () => {
    const patch = reduceTurnEvent(empty, { type: "error", code: "draft_unreadable", message: "json: bad" });
    const notice = patch.items?.at(-1);
    expect(notice).toMatchObject({ kind: "notice", tone: "error" });
    expect(notice && "text" in notice && notice.text).not.toContain("json");
  });

  it("replaces a draft item when its entry comes again with a new state", () => {
    let state = { ...empty, ...reduceTurnEvent(empty, { type: "entry", entry: entry("e1") }) };
    state = { ...state, ...reduceTurnEvent(state, { type: "entry", entry: entry("e1", "committed") }) };
    expect(state.items).toHaveLength(1);
    expect(state.items[0].kind === "draft" && state.items[0].data?.state).toBe("committed");
  });

  it("the active draft is the newest one, while it is open", () => {
    const items = [entryToItem(entry("e1")), entryToItem(entry("e2"))];
    expect(activeDraftId({ items })).toBe("e2");
    expect(makePlaceholderKey({ items })).toBe("copilot.placeholder.refine");
    expect(activeDraftId({ items: [items[0], entryToItem(entry("e3", "committed"))] })).toBeNull();
    expect(makePlaceholderKey({ items: [] })).toBe("copilot.placeholder.make");
  });

  it("keeps edits outside items, so editing does not touch the transcript", () => {
    useCopilot.setState({ items: [entryToItem(entry("e1"))] });
    const before = useCopilot.getState().items;
    useCopilot.getState().editDraft("e1", (draft) => renameNode(draft, "a", "Write docs"));
    expect(useCopilot.getState().items).toBe(before);
    expect(currentDraft(useCopilot.getState(), "e1")?.nodes[0].title).toBe("Write docs");
    // A new conversation forgets them.
    useCopilot.getState().showConversation(null, []);
    expect(useCopilot.getState().drafts).toEqual({});
  });

  it("records a new state on the item", () => {
    useCopilot.setState({ items: [entryToItem(entry("e1"))] });
    useCopilot.getState().setDraftData("e1", { state: "committed", batchId: "b1", createdIds: ["n1"] });
    const item = useCopilot.getState().items[0];
    expect(item.kind === "draft" && item.data).toMatchObject({ state: "committed", batchId: "b1" });
  });
});

describe("plan drafts (a coding agent's plan)", () => {
  const planEntry = (id: string, value: unknown): CopilotEntry => ({
    id,
    seq: 3,
    kind: "draft",
    text: "Plan ready: 2 tasks (draft)",
    data: JSON.stringify(value),
    createdAt: 1,
  });
  const plan = {
    ...data(),
    provider: "",
    model: "",
    draft: { ...data().draft, summary: "" },
    origin: { runId: "run-7", goalId: "n-12", goalTitle: "Login page", agent: "claude", model: "sonnet" },
    extras: {
      a: { tags: ["goal:n-12", "db"], acceptance: ["Migration runs"], priority: "high" },
      b: { acceptance: ["The form logs in"] },
    },
  };

  it("parseDraftData keeps origin and extras", () => {
    const parsed = parseDraftData(JSON.stringify(plan));
    expect(parsed?.origin).toEqual(plan.origin);
    expect(parsed?.extras).toEqual(plan.extras);
    // A copilot draft has neither.
    const own = parseDraftData(JSON.stringify(data()));
    expect(own?.origin).toBeUndefined();
    expect(own?.extras).toBeUndefined();
  });

  it("drops a malformed extras value without failing", () => {
    const parsed = parseDraftData(
      JSON.stringify({
        ...plan,
        origin: { ...plan.origin, agent: 7 },
        extras: {
          a: { tags: ["db", 3], acceptance: "not a list", priority: "high" },
          b: "nope",
          c: null,
          d: { acceptance: ["Works"] },
        },
      }),
    );
    expect(parsed).not.toBeNull();
    expect(parsed?.origin).toEqual({ runId: "run-7", goalId: "n-12", goalTitle: "Login page", model: "sonnet" });
    expect(parsed?.extras).toEqual({ a: { priority: "high" }, d: { acceptance: ["Works"] } });
    expect(parseDraftData(JSON.stringify({ ...plan, extras: ["x"] }))?.extras).toBeUndefined();
    expect(parseDraftData(JSON.stringify({ ...plan, origin: { goalId: "n-12" } }))?.origin).toBeUndefined();
  });

  it("refinableDraftId skips a plan draft", () => {
    const own = entryToItem(entry("e1"));
    const planned = entryToItem(planEntry("e2", plan));
    expect(refinableDraftId({ items: [own] })).toBe("e1");
    expect(activeDraftId({ items: [own, planned] })).toBe("e2");
    expect(refinableDraftId({ items: [own, planned] })).toBeNull();
    expect(makePlaceholderKey({ items: [own, planned] })).toBe("copilot.placeholder.planOpen");
    expect(makePlaceholderKey({ items: [own] })).toBe("copilot.placeholder.refine");
  });
});
