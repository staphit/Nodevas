import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { CopilotDraftData } from "../api";
import { translate } from "../i18n";
import { readPreferences } from "../preferences";
import { useApp } from "../store";
import { resetBrowserModelCache } from "./browserModel";
import { entryToItem, useCopilot } from "./copilotStore";
import { renameNode } from "./draft/draftStore";
import { sendDraft, sendTurn, turnContext } from "./session";

function ndjson(events: unknown[]): Response {
  const body = events.map((event) => JSON.stringify(event)).join("\n") + "\n";
  return new Response(body, { status: 200, headers: { "Content-Type": "application/x-ndjson" } });
}

const draftData: CopilotDraftData = {
  draft: { summary: "Launch", nodes: [{ key: "a", title: "Docs", kind: "task" }] },
  issues: [],
  provider: "anthropic",
  model: "claude",
  state: "open",
};

let turnBodies: Record<string, unknown>[] = [];

beforeEach(() => {
  turnBodies = [];
  useApp.setState({
    preferences: { ...readPreferences(), language: "zh-TW" },
    activeProject: "main",
    activeTab: "n1",
    paneOpen: { graph: true, timeline: true },
    canvasSelection: { project: "main", ids: [] },
  });
  useCopilot.setState({
    activeId: "c1",
    conversations: [],
    items: [],
    drafts: {},
    pending: [],
    running: false,
    turnId: null,
    mode: "default",
  });
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string, init?: RequestInit) => {
      if (url === "/api/copilot/conversations/c1/turns") {
        turnBodies.push(JSON.parse(init!.body as string));
        return ndjson([
          { type: "turn_started", turnId: "t1", entryId: "u1" },
          { type: "draft_status", code: "reading" },
          { type: "draft_status", code: "drafting" },
          {
            type: "entry",
            entry: { id: "d2", seq: 2, kind: "draft", text: "Launch", data: JSON.stringify(draftData), createdAt: 1 },
          },
          { type: "turn_finished", stop: "end_turn" },
        ]);
      }
      if (url.startsWith("/api/copilot/conversations?project=")) {
        return new Response(JSON.stringify({ conversations: [] }));
      }
      return new Response("{}", { status: 404 });
    }),
  );
});

afterEach(() => {
  vi.unstubAllGlobals();
  resetBrowserModelCache();
});

describe("turn context", () => {
  it("sends the canvas selection, clipped to 50", () => {
    const ids = Array.from({ length: 60 }, (_, index) => `n${index}`);
    useApp.getState().setCanvasSelection(ids);
    expect(turnContext().selectedNodeIds).toEqual(ids.slice(0, 50));
  });

  it("falls back to the open document without a selection, or for another project's", () => {
    useApp.setState({ canvasSelection: { project: "other", ids: ["x1", "x2"] } });
    expect(turnContext().selectedNodeIds).toEqual(["n1"]);
  });

  it("sends the canvas selection on a chat turn", async () => {
    useApp.getState().setCanvasSelection(["n4", "n5"]);
    await sendTurn("what is next", "text");
    expect(turnBodies[0]).toMatchObject({ context: { selectedNodeIds: ["n4", "n5"] } });
    expect(turnBodies[0].intent).toBeUndefined();
  });
});

describe("sendDraft", () => {
  it("asks for a draft and shows it with its steps", async () => {
    const steps: (string | null)[] = [];
    const stop = useCopilot.subscribe((state) => steps.push(state.draftStep));
    await sendDraft("plan a launch", "text");
    stop();
    expect(turnBodies[0]).toMatchObject({ text: "plan a launch", intent: "draft" });
    expect(turnBodies[0].previous).toBeUndefined();
    expect(steps).toContain("reading");
    expect(steps).toContain("drafting");
    expect(useCopilot.getState().draftStep).toBeNull();
    expect(useCopilot.getState().items.at(-1)).toMatchObject({ kind: "draft", id: "d2" });
  });

  it("refines the open draft, sending it as the person edited it", async () => {
    useCopilot.setState({
      items: [
        entryToItem({ id: "d1", seq: 1, kind: "draft", text: "", data: JSON.stringify(draftData), createdAt: 1 }),
      ],
    });
    useCopilot.getState().editDraft("d1", (draft) => renameNode(draft, "a", "Write the guide"));
    const refining: (string | null)[] = [];
    const stop = useCopilot.subscribe((state) => refining.push(state.running ? state.refining : null));
    await sendDraft("add a review before shipping", "voice");
    stop();
    // The card it refines is read-only meanwhile.
    expect(refining).toContain("d1");
    expect(turnBodies[0]).toMatchObject({
      intent: "draft",
      source: "voice",
      refineOf: "d1",
      previous: { nodes: [{ key: "a", title: "Write the guide" }] },
    });
  });

  it("sendDraft does not refine an open plan draft and sets the planOpen notice", async () => {
    const plan: CopilotDraftData = {
      ...draftData,
      provider: "",
      model: "",
      draft: { ...draftData.draft, summary: "" },
      origin: { runId: "run-7", goalId: "n-12", agent: "claude" },
      extras: { a: { acceptance: ["Docs build"] } },
    };
    useCopilot.setState({
      items: [entryToItem({ id: "p1", seq: 1, kind: "draft", text: "", data: JSON.stringify(plan), createdAt: 1 })],
      notice: null,
    });
    await sendDraft("add a review step", "text");
    expect(turnBodies).toEqual([]);
    expect(useCopilot.getState().notice).toBe(translate("copilot.planOpen", "zh-TW"));
    // The words are not lost: they go back into the box.
    expect(useCopilot.getState().composerFill?.text).toBe("add a review step");
    // Ask mode is unaffected.
    await sendTurn("what does the plan do?", "text");
    expect(turnBodies).toHaveLength(1);
    expect(turnBodies[0].intent).toBeUndefined();
  });
});
