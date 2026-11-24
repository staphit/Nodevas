import { act, fireEvent, render, screen } from "@testing-library/react";
import { createRef } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Actor, Run, RunAsk, Runner, TerminalConnection } from "../../api";
import { readPreferences } from "../../preferences";
import { useApp } from "../../store";
import {
  canEndRun,
  deriveComposerState,
  PASTE_CR_DELAY_MS,
  RunComposer,
  type RunComposerHandle,
} from "./RunComposer";

function makeRun(overrides: Partial<Run> = {}): Run {
  return {
    id: "r1",
    project: "",
    nodeId: "",
    global: true,
    runnerId: "runner1",
    runnerName: "my-mac",
    agent: "claude",
    model: "opus",
    repoId: "repo1",
    repoName: "nodevas",
    base: "main",
    branch: "agent/task-r1",
    prompt: "",
    status: "running",
    mode: "interactive",
    exitCode: 0,
    createdBy: "u1",
    createdByName: "alice",
    createdAt: "2026-11-20T08:00:00Z",
    ...overrides,
  };
}

const alice: Actor = { id: "u1", name: "alice", role: "member" };
const bob: Actor = { id: "u2", name: "bob", role: "member" };
const admin: Actor = { id: "u3", name: "root", role: "admin" };
const aliceRunner: Runner = {
  id: "runner1",
  name: "my-mac",
  ownerId: "u1",
  ownerName: "alice",
  createdAt: "2026-11-01T00:00:00Z",
  lastSeenAt: "2026-11-20T08:00:00Z",
  online: true,
};

function makeConn(sendResult = true): TerminalConnection {
  return {
    sendInput: vi.fn(() => sendResult),
    resize: vi.fn(),
    setFinal: vi.fn(),
    setViewOnly: vi.fn(),
    close: vi.fn(),
  };
}

beforeEach(() => {
  useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
});

afterEach(() => {
  vi.useRealTimers();
});

describe("deriveComposerState — every §4 row + Simple variants", () => {
  it("handles queued / offered / waiting behind / waiting on runner owner", () => {
    const queued = deriveComposerState({ run: makeRun({ status: "queued" }), viewer: alice, runner: aliceRunner, edition: "single" });
    expect(queued.enabled).toBe(false);
    expect(queued.placeholderKey).toBe("runDialog.ph.queued");

    const queuedSimple = deriveComposerState({ run: makeRun({ status: "queued" }), viewer: null, runner: aliceRunner, edition: "single", simple: true });
    expect(queuedSimple.placeholderKey).toBe("runDialog.simple.ph.queued");

    const waitingBehind = deriveComposerState({ run: makeRun({ status: "queued", afterRun: "r0" }), viewer: alice, runner: aliceRunner, edition: "single" });
    expect(waitingBehind.enabled).toBe(false);
    expect(waitingBehind.placeholderKey).toBe("busy.waitingBehind");

    const waitingBehindSimple = deriveComposerState({ run: makeRun({ status: "queued", afterRun: "r0" }), viewer: null, runner: aliceRunner, edition: "single", simple: true });
    expect(waitingBehindSimple.placeholderKey).toBe("runDialog.simple.ph.waitingBehind");

    const waitingOwner = deriveComposerState({ run: makeRun({ status: "offered", createdBy: "u2", createdByName: "bob" }), viewer: bob, runner: aliceRunner, edition: "team" });
    expect(waitingOwner.enabled).toBe(false);
    expect(waitingOwner.placeholderKey).toBe("runDialog.ph.waitingOwner");
    expect(waitingOwner.placeholderVars).toEqual({ who: "alice" });
  });

  it("handles running interactive: editable, screenAsks blocked, viewOnly, and hub read-only", () => {
    const live = deriveComposerState({ run: makeRun({ status: "running", mode: "interactive" }), viewer: alice, runner: aliceRunner, edition: "team" });
    expect(live.kind).toBe("interactive");
    expect(live.enabled).toBe(true);
    expect(live.placeholderKey).toBe("runDialog.ph.type");
    expect(live.showKeyRow).toBe(true);

    const screenAsks = deriveComposerState({ run: makeRun({ status: "running", mode: "interactive" }), viewer: alice, runner: aliceRunner, edition: "team", screenAsks: true });
    expect(screenAsks.kind).toBe("screen_asks");
    expect(screenAsks.enabled).toBe(false);
    expect(screenAsks.placeholderKey).toBe("runDialog.ph.screenAsks");
    expect(screenAsks.showKeyRow).toBe(true);

    const screenAsksSimple = deriveComposerState({ run: makeRun({ status: "running", mode: "interactive" }), viewer: null, runner: aliceRunner, edition: "single", simple: true, screenAsks: true });
    expect(screenAsksSimple.enabled).toBe(false);
    expect(screenAsksSimple.placeholderKey).toBe("runDialog.simple.ph.screenAsks");

    const viewOnly = deriveComposerState({ run: makeRun({ status: "running", mode: "interactive", viewOnly: true }), viewer: alice, runner: aliceRunner, edition: "team" });
    expect(viewOnly.enabled).toBe(false);
    expect(viewOnly.placeholderKey).toBe("runDialog.ph.viewOnly");

    const viewOnlySimple = deriveComposerState({ run: makeRun({ status: "running", mode: "interactive", viewOnly: true }), viewer: null, runner: aliceRunner, edition: "single", simple: true });
    expect(viewOnlySimple.placeholderKey).toBe("runDialog.simple.ph.viewOnly");

    const notYours = deriveComposerState({ run: makeRun({ status: "running", mode: "interactive" }), viewer: bob, runner: aliceRunner, edition: "team", terminalReadonly: true });
    expect(notYours.enabled).toBe(false);
    expect(notYours.placeholderKey).toBe("runDialog.ph.notYours");
    expect(notYours.placeholderVars).toEqual({ name: "alice" });
  });

  it("handles running background: node pending question, node pending permission, global queued reply", () => {
    const nodeRun = makeRun({ global: false, project: "p", nodeId: "n1", mode: "" });
    const qAsk: RunAsk = { id: "a1", kind: "question", status: "pending", question: "Pick?", createdAt: 1, canAnswer: true };
    const pAsk: RunAsk = { id: "a2", kind: "permission", status: "pending", tool: "Bash", createdAt: 1, canAnswer: true };

    const withQ = deriveComposerState({ run: nodeRun, viewer: alice, runner: aliceRunner, edition: "team", pendingAsks: [qAsk] });
    expect(withQ.kind).toBe("ask_question");
    expect(withQ.enabled).toBe(true);
    expect(withQ.askId).toBe("a1");

    const withPerm = deriveComposerState({ run: nodeRun, viewer: alice, runner: aliceRunner, edition: "team", pendingAsks: [pAsk] });
    expect(withPerm.kind).toBe("ask_buttons");
    expect(withPerm.enabled).toBe(false);

    const globalBg = deriveComposerState({ run: makeRun({ mode: "" }), viewer: alice, runner: aliceRunner, edition: "team" });
    expect(globalBg.kind).toBe("background_queue");
    expect(globalBg.enabled).toBe(true);
    expect(globalBg.placeholderKey).toBe("runDialog.ph.background");

    const globalBgSimple = deriveComposerState({ run: makeRun({ mode: "" }), viewer: null, runner: aliceRunner, edition: "single", simple: true });
    expect(globalBgSimple.placeholderKey).toBe("runDialog.simple.ph.background");
  });

  it("handles ended runs: follow-up, open PR, usage limit resetAt, merging, gone/newWork, continuedBelow, notYours, node ended", () => {
    const now = Date.parse("2026-11-20T09:00:00Z");

    const ended = deriveComposerState({ run: makeRun({ status: "succeeded" }), viewer: alice, runner: aliceRunner, edition: "single", now });
    expect(ended.kind).toBe("follow_up");
    expect(ended.enabled).toBe(true);
    expect(ended.placeholderKey).toBe("runDialog.ph.followUp");

    const endedSimple = deriveComposerState({ run: makeRun({ status: "succeeded" }), viewer: null, runner: aliceRunner, edition: "single", simple: true, now });
    expect(endedSimple.placeholderKey).toBe("runDialog.simple.ph.followUp");

    const openPr = deriveComposerState({ run: makeRun({ status: "succeeded", prUrl: "https://github.com/a/b/pull/1", prState: "open" }), viewer: alice, runner: aliceRunner, edition: "team", now });
    expect(openPr.kind).toBe("follow_up_pr");
    expect(openPr.placeholderKey).toBe("runDialog.ph.followUpPr");

    // Simple never mentions PR
    const openPrSimple = deriveComposerState({ run: makeRun({ status: "succeeded", prUrl: "https://github.com/a/b/pull/1", prState: "open" }), viewer: null, runner: aliceRunner, edition: "single", simple: true, now });
    expect(openPrSimple.placeholderKey).toBe("runDialog.simple.ph.followUp");

    const reset = deriveComposerState({
      run: makeRun({ status: "failed", failureKind: "rate_limit", resetAt: "2026-11-20T10:00:00Z" }),
      viewer: alice,
      runner: aliceRunner,
      edition: "team",
      now,
    });
    expect(reset.kind).toBe("reset_at");
    expect(reset.enabled).toBe(true);
    expect(reset.placeholderKey).toBe("runDialog.ph.resetAt");

    const resetSimple = deriveComposerState({
      run: makeRun({ status: "failed", failureKind: "quota", resetAt: "2026-11-20T10:00:00Z" }),
      viewer: null,
      runner: aliceRunner,
      edition: "single",
      simple: true,
      now,
    });
    expect(resetSimple.placeholderKey).toBe("runDialog.simple.ph.resetAt");

    const merging = deriveComposerState({
      run: makeRun({ status: "succeeded", merge: { id: "m1", runId: "r1", headSha: "abc", status: "pending", requestedBy: "u1", createdAt: "2026-11-20T08:50:00Z" } }),
      viewer: alice,
      runner: aliceRunner,
      edition: "team",
      now,
    });
    expect(merging.kind).toBe("merging");
    expect(merging.enabled).toBe(false);
    expect(merging.placeholderKey).toBe("runDialog.ph.merging");

    const mergingSimple = deriveComposerState({
      run: makeRun({ status: "succeeded", workspaceAction: { id: "a1", runId: "r1", action: "merge_local", status: "pending", requestedBy: "local", createdAt: "2026-11-20T08:50:00Z" } }),
      viewer: null,
      runner: aliceRunner,
      edition: "single",
      simple: true,
      now,
    });
    expect(mergingSimple.placeholderKey).toBe("runDialog.simple.ph.merging");

    for (const gonePatch of [
      { workspace: "merged_local" },
      { workspace: "discarded" },
      { prState: "merged" },
      { prState: "closed" },
    ]) {
      const gone = deriveComposerState({ run: makeRun({ status: "succeeded", ...gonePatch }), viewer: alice, runner: aliceRunner, edition: "team", now });
      expect(gone.kind).toBe("new_work");
      expect(gone.enabled).toBe(true);
      expect(gone.actionLabelKey).toBe("runDialog.newWork");
    }

    const continued = deriveComposerState({ run: makeRun({ status: "succeeded", continuedBy: "r2" }), viewer: alice, runner: aliceRunner, edition: "team", now });
    expect(continued.kind).toBe("continued_below");
    expect(continued.enabled).toBe(false);
    expect(continued.placeholderKey).toBe("runDialog.continuedBelow");
    expect(continued.showJumpLatest).toBe(true);

    const continuedSimple = deriveComposerState({ run: makeRun({ status: "succeeded", continuedBy: "r2" }), viewer: null, runner: aliceRunner, edition: "single", simple: true, now });
    expect(continuedSimple.placeholderKey).toBe("runDialog.simple.continuedBelow");

    const otherViewer = deriveComposerState({ run: makeRun({ status: "succeeded" }), viewer: bob, runner: aliceRunner, edition: "team", now });
    expect(otherViewer.kind).toBe("not_yours");
    expect(otherViewer.enabled).toBe(false);
    expect(otherViewer.placeholderKey).toBe("runDialog.ph.notYours");

    const nodeEnded = deriveComposerState({ run: makeRun({ global: false, project: "p", nodeId: "n1", status: "succeeded" }), viewer: alice, runner: aliceRunner, edition: "team", now });
    expect(nodeEnded.kind).toBe("node_ended");
    expect(nodeEnded.enabled).toBe(false);
    expect(nodeEnded.placeholderKey).toBe("runDialog.ph.nodeRun");
    expect(nodeEnded.showRedispatch).toBe(true);
  });
});

describe("canEndRun", () => {
  it("allows only typists, never grants admin extra right, and restricts viewOnly to runner owner", () => {
    const run = makeRun({ status: "running", mode: "interactive", createdBy: "u2" });
    expect(canEndRun(run, alice, aliceRunner, "team")).toBe(true); // runner owner
    expect(canEndRun(run, bob, aliceRunner, "team")).toBe(true); // dispatcher
    expect(canEndRun(run, admin, aliceRunner, "team")).toBe(false); // admin has no extra right

    const viewOnlyRun = makeRun({ status: "running", mode: "interactive", createdBy: "u2", viewOnly: true });
    expect(canEndRun(viewOnlyRun, alice, aliceRunner, "team")).toBe(true); // owner may End
    expect(canEndRun(viewOnlyRun, bob, aliceRunner, "team")).toBe(false); // dispatcher blocked on viewOnly
    expect(canEndRun(viewOnlyRun, admin, aliceRunner, "team")).toBe(false);

    expect(canEndRun(run, null, aliceRunner, "single")).toBe(true);
  });
});

describe("RunComposer interaction", () => {
  it("ignores Enter while composing zh-TW IME (isComposing or keyCode 229)", () => {
    const onSubmit = vi.fn();
    const state = deriveComposerState({ run: makeRun({ status: "succeeded" }), viewer: alice, runner: aliceRunner, edition: "single" });
    render(<RunComposer state={state} onSubmitText={onSubmit} />);

    const box = screen.getByRole("textbox");
    fireEvent.change(box, { target: { value: "測試" } });

    fireEvent.compositionStart(box);
    fireEvent.keyDown(box, { key: "Enter" });
    expect(onSubmit).not.toHaveBeenCalled();

    fireEvent.compositionEnd(box);
    fireEvent.keyDown(box, { key: "Enter", keyCode: 229 });
    expect(onSubmit).not.toHaveBeenCalled();

    // Shift+Enter does not submit
    fireEvent.keyDown(box, { key: "Enter", shiftKey: true });
    expect(onSubmit).not.toHaveBeenCalled();

    // Regular Enter submits
    fireEvent.keyDown(box, { key: "Enter" });
    expect(onSubmit).toHaveBeenCalledWith("測試");
  });

  it("wraps interactive send in bracketed paste when enabled and sends CR 150ms later", () => {
    vi.useFakeTimers();
    const conn = makeConn(true);
    const state = deriveComposerState({ run: makeRun({ status: "running", mode: "interactive" }), viewer: alice, runner: aliceRunner, edition: "single" });
    render(
      <RunComposer
        state={state}
        terminalConn={conn}
        isBracketedPaste={() => true}
      />,
    );

    const box = screen.getByRole("textbox") as HTMLTextAreaElement;
    fireEvent.change(box, { target: { value: "line1\nline2" } });
    fireEvent.keyDown(box, { key: "Enter" });

    expect(conn.sendInput).toHaveBeenCalledTimes(1);
    expect(conn.sendInput).toHaveBeenNthCalledWith(1, "\x1b[200~line1\nline2\x1b[201~");

    act(() => vi.advanceTimersByTime(PASTE_CR_DELAY_MS - 1));
    expect(conn.sendInput).toHaveBeenCalledTimes(1);

    act(() => vi.advanceTimersByTime(1));
    expect(conn.sendInput).toHaveBeenCalledTimes(2);
    expect(conn.sendInput).toHaveBeenNthCalledWith(2, "\r");
    expect(box.value).toBe("");
  });

  it("keeps text and shows error when interactive send fails (on paste or delayed CR)", () => {
    vi.useFakeTimers();
    const conn = makeConn(false);
    const state = deriveComposerState({ run: makeRun({ status: "running", mode: "interactive" }), viewer: alice, runner: aliceRunner, edition: "single" });
    render(<RunComposer state={state} terminalConn={conn} isBracketedPaste={() => false} />);

    const box = screen.getByRole("textbox") as HTMLTextAreaElement;
    fireEvent.change(box, { target: { value: "hello" } });
    fireEvent.keyDown(box, { key: "Enter" });

    expect(box.value).toBe("hello");
    expect(screen.getByRole("alert")).toHaveTextContent(/Not sent/);

    // Now first write succeeds, delayed CR fails: text must still be kept!
    let calls = 0;
    (conn.sendInput as ReturnType<typeof vi.fn>).mockImplementation(() => ++calls === 1);
    fireEvent.keyDown(box, { key: "Enter" });
    act(() => vi.advanceTimersByTime(PASTE_CR_DELAY_MS));
    expect(box.value).toBe("hello");
    expect(screen.getByRole("alert")).toHaveTextContent(/Not sent/);
  });

  it("blocks text submission when screenAsks is true while keeping key row usable", () => {
    const conn = makeConn(true);
    const state = deriveComposerState({
      run: makeRun({ status: "running", mode: "interactive" }),
      viewer: alice,
      runner: aliceRunner,
      edition: "single",
      screenAsks: true,
    });
    render(<RunComposer state={state} terminalConn={conn} simple />);

    const box = screen.getByRole("textbox");
    expect(box).toBeDisabled();
    expect(box).toHaveAttribute("placeholder", "It is asking you something on the screen");

    // Simple key row labels are visible and enabled so user can answer the menu safely
    const choose1 = screen.getByRole("button", { name: "Choose 1" });
    expect(choose1).not.toBeDisabled();
    fireEvent.click(choose1);
    expect(conn.sendInput).toHaveBeenCalledWith("1");
  });

  it("supports prefill(text) via imperative ref handle", () => {
    const ref = createRef<RunComposerHandle>();
    const state = deriveComposerState({ run: makeRun({ status: "succeeded" }), viewer: alice, runner: aliceRunner, edition: "single" });
    render(<RunComposer ref={ref} state={state} onSubmitText={vi.fn()} />);

    act(() => ref.current?.prefill("Please check the overlap"));
    const box = screen.getByRole("textbox") as HTMLTextAreaElement;
    expect(box.value).toBe("Please check the overlap");
    expect(document.activeElement).toBe(box);
  });
});
