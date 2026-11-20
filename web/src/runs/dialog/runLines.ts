import type { Run, RunAsk, RunEvent } from "../../api";
import { summarizeTool, type ToolSummary } from "../toolLine";

/** Mirrors runnerwire.MaxEventText (16 KiB). */
export const MAX_EVENT_TEXT = 16 << 10;

const HUB_PREFIX = "hub: ";
const EXIT_CODE_RE = /\bexited with code (-?\d+)\b/i;
const encoder = new TextEncoder();

export interface RunTurnInput {
  run: Run;
  events: readonly RunEvent[];
  asks?: readonly RunAsk[];
  worktree?: string;
}

export type RunNoticeKind = "branch_moved" | "quiet" | "quiet_waiting" | "resume_lost";

export type RunDialogLine =
  | { kind: "turn"; key: string; run: Run; index: number }
  | { kind: "user"; key: string; runId: string; text: string; pending: boolean; withdrawRunId?: string }
  | { kind: "text"; key: string; runId: string; seq: number; text: string; truncated: boolean }
  | {
      kind: "tool";
      key: string;
      runId: string;
      seq: number;
      call: RunEvent;
      result?: RunEvent;
      summary: ToolSummary;
    }
  | { kind: "thinking"; key: string; runId: string; seq: number; text: string }
  | { kind: "error"; key: string; runId: string; seq?: number; text: string }
  | {
      kind: "log";
      key: string;
      runId: string;
      seq: number;
      text: string;
      hidden: boolean;
      hubNote: boolean;
      exitNote: boolean;
    }
  | { kind: "ask"; key: string; runId: string; ask: RunAsk }
  | { kind: "notice"; key: string; runId: string; notice: RunNoticeKind };

function isPendingQueuedFollowUp(run: Run): boolean {
  return Boolean(
    run.continueOf &&
      (run.status === "queued" || run.status === "offered") &&
      !run.startedAt,
  );
}

function isTextTruncated(text: string): boolean {
  if (text.length >= MAX_EVENT_TEXT) return true;
  if (text.length * 4 < MAX_EVENT_TEXT - 3) return false;
  return encoder.encode(text).length >= MAX_EVENT_TEXT - 3;
}

/**
 * Pure transcript builder (§3.1, §3.6).
 * - Emits turn markers when a chain has more than one turn.
 * - Merges `tool_call` and `tool_result` by `toolId`.
 * - Interleaves settled asks by timestamp and pins pending asks to the bottom
 *   of their turn (runner and hub clocks can disagree).
 * - Hides `usage` events and ordinary `log` lines, keeps `hub:` notes and
 *   non-zero exit notes visible, and suppresses an End's `-1` exit line.
 */
export function buildRunLines(turns: readonly RunTurnInput[]): RunDialogLine[] {
  const out: RunDialogLine[] = [];
  const multiTurn = turns.length > 1;

  for (let turnIdx = 0; turnIdx < turns.length; turnIdx++) {
    const { run, events, asks = [], worktree } = turns[turnIdx];
    const runId = run.id;
    const pendingFollowUp = isPendingQueuedFollowUp(run);
    const suppressKillExit =
      run.endReason === "ended" || run.endReason === "quiet" || run.endReason === "quiet_waiting";

    if (multiTurn) {
      out.push({ kind: "turn", key: `turn-${runId}`, run, index: turnIdx + 1 });
    }

    const settledAsks = asks
      .filter((ask) => ask.status !== "pending")
      .sort((a, b) => a.createdAt - b.createdAt);
    const pendingAsks = asks
      .filter((ask) => ask.status === "pending")
      .sort((a, b) => a.createdAt - b.createdAt);

    let askCursor = 0;
    const toolIndexById = new Map<string, number>();
    let sawUserEvent = false;
    const errorTexts = new Set<string>();

    const flushSettledAsksBefore = (eventAt: string) => {
      const at = Date.parse(eventAt);
      if (!Number.isFinite(at)) return;
      while (askCursor < settledAsks.length && settledAsks[askCursor].createdAt < at) {
        const ask = settledAsks[askCursor++];
        out.push({ kind: "ask", key: `ask-${runId}-${ask.id}`, runId, ask });
      }
    };

    for (const event of events) {
      flushSettledAsksBefore(event.at);
      const text = event.text ?? "";

      switch (event.type) {
        case "user": {
          sawUserEvent = true;
          out.push({
            kind: "user",
            key: `user-${runId}-${event.seq}`,
            runId,
            text,
            pending: pendingFollowUp,
            withdrawRunId: pendingFollowUp ? runId : undefined,
          });
          break;
        }
        case "text": {
          out.push({
            kind: "text",
            key: `text-${runId}-${event.seq}`,
            runId,
            seq: event.seq,
            text,
            truncated: isTextTruncated(text),
          });
          break;
        }
        case "tool_call": {
          const summary = summarizeTool(event, undefined, { worktree });
          const idx = out.length;
          out.push({
            kind: "tool",
            key: `tool-${runId}-${event.toolId || event.seq}`,
            runId,
            seq: event.seq,
            call: event,
            summary,
          });
          if (event.toolId) toolIndexById.set(event.toolId, idx);
          break;
        }
        case "tool_result": {
          const existingIdx = event.toolId ? toolIndexById.get(event.toolId) : undefined;
          if (existingIdx !== undefined) {
            const existing = out[existingIdx];
            if (existing && existing.kind === "tool") {
              out[existingIdx] = {
                ...existing,
                result: event,
                summary: summarizeTool(existing.call, event, { worktree }),
              };
              break;
            }
          }
          // Orphan tool_result without a matching call: show as dimmed log line.
          out.push({
            kind: "log",
            key: `log-${runId}-${event.seq}`,
            runId,
            seq: event.seq,
            text: text || event.type,
            hidden: true,
            hubNote: false,
            exitNote: false,
          });
          break;
        }
        case "thinking": {
          out.push({
            kind: "thinking",
            key: `thinking-${runId}-${event.seq}`,
            runId,
            seq: event.seq,
            text,
          });
          break;
        }
        case "error": {
          if (text.trim()) errorTexts.add(text.trim());
          out.push({
            kind: "error",
            key: `error-${runId}-${event.seq}`,
            runId,
            seq: event.seq,
            text,
          });
          break;
        }
        case "usage":
          // Hidden: tokens appear in the header/energy bar (§3.6, §11).
          break;
        case "log": {
          const exitMatch = EXIT_CODE_RE.exec(text);
          if (exitMatch) {
            const code = Number(exitMatch[1]);
            if (suppressKillExit && code === -1) break;
            if (code !== 0) {
              out.push({
                kind: "log",
                key: `log-${runId}-${event.seq}`,
                runId,
                seq: event.seq,
                text,
                hidden: false,
                hubNote: false,
                exitNote: true,
              });
              break;
            }
          }
          if (text.startsWith(HUB_PREFIX)) {
            out.push({
              kind: "log",
              key: `log-${runId}-${event.seq}`,
              runId,
              seq: event.seq,
              text: text.slice(HUB_PREFIX.length),
              hidden: false,
              hubNote: true,
              exitNote: false,
            });
            break;
          }
          out.push({
            kind: "log",
            key: `log-${runId}-${event.seq}`,
            runId,
            seq: event.seq,
            text,
            hidden: true,
            hubNote: false,
            exitNote: false,
          });
          break;
        }
        default: {
          // Unknown event kind from a newer runner: dimmed log line.
          out.push({
            kind: "log",
            key: `log-${runId}-${event.seq}`,
            runId,
            seq: event.seq,
            text: text || event.type,
            hidden: false,
            hubNote: false,
            exitNote: false,
          });
          break;
        }
      }
    }

    // Synthesize a pending user line if a queued follow-up hasn't loaded its seq-0 event yet.
    if (!sawUserEvent && pendingFollowUp && run.title) {
      out.push({
        kind: "user",
        key: `user-${runId}-pending`,
        runId,
        text: run.title,
        pending: true,
        withdrawRunId: runId,
      });
    }

    // Remaining settled asks newer than every event.
    while (askCursor < settledAsks.length) {
      const ask = settledAsks[askCursor++];
      out.push({ kind: "ask", key: `ask-${runId}-${ask.id}`, runId, ask });
    }

    // Pending asks always pin to the bottom of their turn.
    for (const ask of pendingAsks) {
      out.push({ kind: "ask", key: `ask-${runId}-${ask.id}`, runId, ask });
    }

    // Run-level failure error row (if not already emitted as an error event).
    if (run.status === "failed" && run.error?.trim() && !errorTexts.has(run.error.trim())) {
      out.push({
        kind: "error",
        key: `run-error-${runId}`,
        runId,
        text: run.error.trim(),
      });
    }

    // Turn-level notices: refused PR push & quiet endings.
    if (run.pushRefused) {
      out.push({ kind: "notice", key: `notice-branch-${runId}`, runId, notice: "branch_moved" });
    }
    if (run.endReason === "quiet") {
      out.push({ kind: "notice", key: `notice-quiet-${runId}`, runId, notice: "quiet" });
    } else if (run.endReason === "quiet_waiting") {
      out.push({
        kind: "notice",
        key: `notice-quiet-waiting-${runId}`,
        runId,
        notice: "quiet_waiting",
      });
    }
  }

  return out;
}
