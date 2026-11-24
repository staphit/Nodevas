import {
  forwardRef,
  useEffect,
  useImperativeHandle,
  useRef,
  useState,
  type KeyboardEvent,
} from "react";
import {
  runFinal,
  runInteractive,
  type Actor,
  type Run,
  type RunAsk,
  type Runner,
  type TerminalConnection,
} from "../../api";
import { formatLocalizedTime, useI18n } from "../../i18n";
import type { Edition } from "../../runners/edition";
import {
  BRACKETED_PASTE_END,
  BRACKETED_PASTE_START,
  keySequence,
  type TermKey,
} from "../terminalKeys";

/** Delay between bracketed paste text and the separate CR submit write (§3.1, smoke report §6.2). */
export const PASTE_CR_DELAY_MS = 150;

export type ComposerKind =
  | "queued"
  | "waiting_behind"
  | "waiting_owner"
  | "interactive"
  | "screen_asks"
  | "view_only"
  | "not_yours"
  | "ask_question"
  | "ask_buttons"
  | "background_queue"
  | "follow_up"
  | "follow_up_pr"
  | "reset_at"
  | "merging"
  | "new_work"
  | "continued_below"
  | "node_ended"
  /** Interactive, but another mount on this page owns the live screen. */
  | "shown_above";

export interface ComposerState {
  kind: ComposerKind;
  enabled: boolean;
  placeholderKey: string;
  placeholderVars?: Record<string, string | number>;
  actionLabelKey?: string;
  showKeyRow?: boolean;
  showStop?: boolean;
  showJumpLatest?: boolean;
  showRedispatch?: boolean;
  /** Offer a jump to the mount that owns the live screen. */
  showJumpOwner?: boolean;
  askId?: string;
}

export interface DeriveComposerInput {
  run: Run;
  viewer: Actor | null;
  runner?: Runner;
  edition: Edition;
  simple?: boolean;
  terminalReadonly?: boolean;
  screenAsks?: boolean;
  pendingAsks?: readonly RunAsk[];
  now?: number;
}

export interface RunComposerHandle {
  prefill: (text: string) => void;
  focus: () => void;
}

/**
 * Whether `viewer` may click "Done, keep the work" (End) on `run` (§7.1).
 * Limited to the `mayType` set (dispatcher or runner owner; never extra right
 * for admin), and on a `viewOnly` run restricted strictly to the runner owner.
 */
export function canEndRun(
  run: Run,
  viewer: Actor | null,
  runner: Runner | undefined,
  edition: Edition,
): boolean {
  if (run.status !== "running" || !runInteractive(run)) return false;
  if (edition === "single") return true;
  if (!viewer) return false;
  const isOwner = Boolean(runner && runner.ownerId === viewer.id);
  const isDispatcher = run.createdBy === viewer.id;
  if (run.viewOnly) return isOwner;
  return isOwner || isDispatcher;
}

function formatResetTime(resetMs: number): string {
  const formatted = formatLocalizedTime(new Date(resetMs));
  return formatted || new Date(resetMs).toISOString();
}

/** Pure derivation of the composer's state from every row of §4 (plus Simple variants). */
export function deriveComposerState({
  run,
  viewer,
  runner,
  edition,
  simple = false,
  terminalReadonly = false,
  screenAsks = false,
  pendingAsks = [],
  now,
}: DeriveComposerInput): ComposerState {
  const isSingle = edition === "single";
  const isGlobal = Boolean(run.global);
  const isFinal = runFinal(run.status);
  const ownerName = runner?.ownerName || run.runnerName || "";
  const creatorName = run.createdByName || ownerName || "";

  const viewerMayType =
    isSingle || Boolean(viewer && (run.createdBy === viewer.id || runner?.ownerId === viewer.id));
  const viewerCanReplyGlobal =
    isSingle ||
    Boolean(
      viewer && run.createdBy === viewer.id && (!runner || runner.ownerId === viewer.id),
    );

  // 1. Queued / offered
  if (run.status === "queued" || run.status === "offered") {
    if (run.afterRun) {
      return {
        kind: "waiting_behind",
        enabled: false,
        showStop: true,
        placeholderKey: simple ? "runDialog.simple.ph.waitingBehind" : "busy.waitingBehind",
      };
    }
    if (run.status === "offered" && !isSingle && runner && viewer && runner.ownerId !== viewer.id) {
      return {
        kind: "waiting_owner",
        enabled: false,
        showStop: true,
        placeholderKey: "runDialog.ph.waitingOwner",
        placeholderVars: { who: ownerName },
      };
    }
    return {
      kind: "queued",
      enabled: false,
      showStop: true,
      placeholderKey: simple ? "runDialog.simple.ph.queued" : "runDialog.ph.queued",
    };
  }

  // 2. Running
  if (!isFinal) {
    if (runInteractive(run)) {
      if (run.viewOnly) {
        return {
          kind: "view_only",
          enabled: false,
          placeholderKey: simple ? "runDialog.simple.ph.viewOnly" : "runDialog.ph.viewOnly",
        };
      }
      if (terminalReadonly || !viewerMayType) {
        return {
          kind: "not_yours",
          enabled: false,
          placeholderKey: "runDialog.ph.notYours",
          placeholderVars: { name: creatorName },
        };
      }
      // Verified CLI smoke test finding (§6.2): sending a delayed CR while a
      // trust/permission menu is shown actively confirms the focused option,
      // so block text submit while keeping the key row enabled.
      if (screenAsks) {
        return {
          kind: "screen_asks",
          enabled: false,
          showKeyRow: true,
          placeholderKey: simple ? "runDialog.simple.ph.screenAsks" : "runDialog.ph.screenAsks",
        };
      }
      return {
        kind: "interactive",
        enabled: true,
        showKeyRow: true,
        placeholderKey: "runDialog.ph.type",
      };
    }

    // Running, background
    if (!isGlobal) {
      const openAsks = pendingAsks.filter((ask) => ask.status === "pending");
      const questionAsk = openAsks.find((ask) => ask.kind === "question" && ask.canAnswer);
      if (questionAsk) {
        return {
          kind: "ask_question",
          enabled: true,
          askId: questionAsk.id,
          placeholderKey: "agents.ask.answerPlaceholder",
        };
      }
      if (openAsks.length > 0) {
        return {
          kind: "ask_buttons",
          enabled: false,
          askId: openAsks[0].id,
          placeholderKey: "runDialog.ph.queued",
        };
      }
      return {
        kind: "queued",
        enabled: false,
        showStop: true,
        placeholderKey: "runDialog.ph.queued",
      };
    }

    if (!viewerCanReplyGlobal) {
      return {
        kind: "not_yours",
        enabled: false,
        placeholderKey: "runDialog.ph.notYours",
        placeholderVars: { name: creatorName },
      };
    }
    return {
      kind: "background_queue",
      enabled: true,
      showStop: true,
      placeholderKey: simple ? "runDialog.simple.ph.background" : "runDialog.ph.background",
    };
  }

  // 3. Final (ended)
  if (!isGlobal) {
    return {
      kind: "node_ended",
      enabled: false,
      placeholderKey: "runDialog.ph.nodeRun",
      showRedispatch: true,
    };
  }

  if (run.continuedBy) {
    return {
      kind: "continued_below",
      enabled: false,
      placeholderKey: simple ? "runDialog.simple.continuedBelow" : "runDialog.continuedBelow",
      showJumpLatest: true,
    };
  }

  if (!viewerCanReplyGlobal) {
    return {
      kind: "not_yours",
      enabled: false,
      placeholderKey: "runDialog.ph.notYours",
      placeholderVars: { name: creatorName },
    };
  }

  if (
    run.merge?.status === "pending" ||
    (run.workspaceAction?.action === "merge_local" && run.workspaceAction?.status === "pending")
  ) {
    return {
      kind: "merging",
      enabled: false,
      placeholderKey: simple ? "runDialog.simple.ph.merging" : "runDialog.ph.merging",
    };
  }

  if (
    run.workspace === "merged_local" ||
    run.workspace === "discarded" ||
    run.prState === "merged" ||
    run.prState === "closed"
  ) {
    return {
      kind: "new_work",
      enabled: true,
      placeholderKey: simple ? "runDialog.simple.ph.followUp" : "runDialog.ph.followUp",
      actionLabelKey: "runDialog.newWork",
    };
  }

  const resetMs = run.resetAt ? Date.parse(run.resetAt) : Number.NaN;
  const currentNow = now ?? Date.now();
  if (
    (run.failureKind === "rate_limit" || run.failureKind === "quota") &&
    Number.isFinite(resetMs) &&
    resetMs > currentNow
  ) {
    return {
      kind: "reset_at",
      enabled: true,
      placeholderKey: simple ? "runDialog.simple.ph.resetAt" : "runDialog.ph.resetAt",
      placeholderVars: { time: formatResetTime(resetMs) },
    };
  }

  if (!simple && run.prUrl && run.prState === "open") {
    return {
      kind: "follow_up_pr",
      enabled: true,
      placeholderKey: "runDialog.ph.followUpPr",
    };
  }

  return {
    kind: "follow_up",
    enabled: true,
    placeholderKey: simple ? "runDialog.simple.ph.followUp" : "runDialog.ph.followUp",
  };
}

type KeyButtonSpec =
  | { id: string; kind: "term"; key: TermKey; label: string; simpleKey: string; simpleVars?: Record<string, string | number> }
  | { id: string; kind: "literal"; text: string; label: string; simpleKey: string; simpleVars?: Record<string, string | number> };

const COMPOSER_KEYS: readonly KeyButtonSpec[] = [
  { id: "enter", kind: "term", key: "enter", label: "Enter", simpleKey: "runDialog.simple.keys.enter" },
  { id: "esc", kind: "term", key: "esc", label: "Esc", simpleKey: "runDialog.simple.keys.esc" },
  { id: "1", kind: "literal", text: "1", label: "1", simpleKey: "runDialog.simple.keys.pick", simpleVars: { n: 1 } },
  { id: "2", kind: "literal", text: "2", label: "2", simpleKey: "runDialog.simple.keys.pick", simpleVars: { n: 2 } },
  { id: "3", kind: "literal", text: "3", label: "3", simpleKey: "runDialog.simple.keys.pick", simpleVars: { n: 3 } },
  { id: "up", kind: "term", key: "up", label: "↑", simpleKey: "runDialog.simple.keys.up" },
  { id: "down", kind: "term", key: "down", label: "↓", simpleKey: "runDialog.simple.keys.down" },
];

export interface RunComposerProps {
  state: ComposerState;
  simple?: boolean;
  busy?: boolean;
  /** Shared live terminal socket for interactive runs. */
  terminalConn?: TerminalConnection | null;
  /** Reads whether xterm currently has bracketed paste enabled. */
  isBracketedPaste?: () => boolean;
  /** Reads whether xterm currently has application cursor keys mode enabled. */
  isApplicationCursor?: () => boolean;
  /** Called for follow-up, queued reply, new work, or ask question submits. */
  onSubmitText?: (text: string) => void | boolean | Promise<void | boolean>;
  canEnd?: boolean;
  ending?: boolean;
  onEnd?: () => void;
  stopping?: boolean;
  onStop?: () => void;
  onJumpLatest?: () => void;
  /** Moves focus to the mount that owns the live screen. */
  onJumpOwner?: () => void;
  onRedispatch?: () => void;
  /** External error message (e.g. from follow-up/end API refusal). */
  error?: string | null;
}

/**
 * Sticky composer for RunDialog (§3.1, §4).
 * - Uses a real `<textarea>` so StoryRoom's keyboard shortcuts skip it.
 * - Guards Enter against zh-TW IME (`isComposing` and `keyCode === 229`).
 * - Wraps interactive writes in bracketed paste when active and sends CR 150ms
 *   later as its own write, keeping text on any failure.
 */
const SIMPLE_PLACEHOLDER_KEYS = new Set([
  "runDialog.simple.ph.queued",
  "runDialog.simple.ph.background",
  "runDialog.simple.ph.followUp",
  "runDialog.simple.ph.resetAt",
  "runDialog.simple.ph.viewOnly",
  "runDialog.simple.ph.screenAsks",
  "runDialog.simple.ph.sendFailed",
  "runDialog.simple.ph.merging",
  "runDialog.simple.ph.waitingBehind",
  "runDialog.simple.ph.shownAbove",
]);

function resolvePlaceholderKey(key: string, simple: boolean): string {
  if (!simple || key.startsWith("runDialog.simple.")) return key;
  if (key === "runDialog.continuedBelow") return "runDialog.simple.continuedBelow";
  if (key === "busy.waitingBehind") return "runDialog.simple.ph.waitingBehind";
  if (key === "runDialog.ph.followUpPr") return "runDialog.simple.ph.followUp";
  if (key.startsWith("runDialog.ph.")) {
    const candidate = `runDialog.simple.ph.${key.slice("runDialog.ph.".length)}`;
    if (SIMPLE_PLACEHOLDER_KEYS.has(candidate)) return candidate;
  }
  return key;
}

export const RunComposer = forwardRef<RunComposerHandle, RunComposerProps>(function RunComposer(
  {
    state,
    simple = false,
    busy = false,
    terminalConn = null,
    isBracketedPaste,
    isApplicationCursor,
    onSubmitText,
    canEnd = false,
    ending = false,
    onEnd,
    stopping = false,
    onStop,
    onJumpLatest,
    onJumpOwner,
    onRedispatch,
    error: externalError = null,
  },
  ref,
) {
  const { t } = useI18n();
  const [text, setText] = useState("");
  const [sendError, setSendError] = useState<string | null>(null);
  const [sendingCr, setSendingCr] = useState(false);
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const composingRef = useRef(false);
  const crTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  useImperativeHandle(
    ref,
    () => ({
      prefill(next: string) {
        setText(next);
        setSendError(null);
        textareaRef.current?.focus();
      },
      focus() {
        textareaRef.current?.focus();
      },
    }),
    [],
  );

  useEffect(
    () => () => {
      if (crTimerRef.current) clearTimeout(crTimerRef.current);
    },
    [],
  );

  const sendFailedText = (reason: string) =>
    simple
      ? t("runDialog.simple.ph.sendFailed")
      : t("runDialog.ph.sendFailed", { reason });

  const submit = async () => {
    if (!state.enabled || busy || sendingCr) return;
    if (!text.trim()) return;

    setSendError(null);

    if (state.kind === "interactive") {
      if (!terminalConn) {
        setSendError(sendFailedText(t("terminal.state.closed")));
        return;
      }
      const payload = isBracketedPaste?.()
        ? `${BRACKETED_PASTE_START}${text}${BRACKETED_PASTE_END}`
        : text;
      const wroteText = terminalConn.sendInput(payload);
      if (!wroteText) {
        setSendError(sendFailedText(t("terminal.state.closed")));
        return;
      }
      setSendingCr(true);
      crTimerRef.current = setTimeout(() => {
        crTimerRef.current = null;
        setSendingCr(false);
        const wroteCr = terminalConn.sendInput("\r");
        if (!wroteCr) {
          setSendError(sendFailedText(t("terminal.state.closed")));
          return;
        }
        setText("");
        setSendError(null);
      }, PASTE_CR_DELAY_MS);
      return;
    }

    if (!onSubmitText) return;
    try {
      const result = await onSubmitText(text.trim());
      if (result !== false) {
        setText("");
      }
    } catch (err) {
      const reason = err instanceof Error ? err.message : String(err);
      setSendError(sendFailedText(reason));
    }
  };

  const onKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    if (event.key !== "Enter") return;
    if (event.shiftKey) return;
    // zh-TW IME composition guard (§3.1)
    if (composingRef.current || event.nativeEvent.isComposing || event.keyCode === 229) return;
    event.preventDefault();
    void submit();
  };

  const sendKey = (spec: KeyButtonSpec) => {
    if (!terminalConn) return;
    const bytes =
      spec.kind === "literal"
        ? spec.text
        : keySequence(spec.key, Boolean(isApplicationCursor?.()));
    if (!terminalConn.sendInput(bytes)) {
      setSendError(sendFailedText(t("terminal.state.closed")));
    }
  };

  const placeholder = t(resolvePlaceholderKey(state.placeholderKey, simple), state.placeholderVars);
  const shownError = sendError || externalError;
  const actionLabel = t(state.actionLabelKey || "runDialog.send");
  const showStop = Boolean(state.showStop || state.kind === "interactive" || state.kind === "screen_asks") && Boolean(onStop);

  return (
    <div className="run-dlg-composer" data-composer-kind={state.kind}>
      {state.showKeyRow && (
        <div className="run-dlg-keys" role="toolbar" aria-label={t("terminal.keys")}>
          {COMPOSER_KEYS.map((spec) => {
            const label = simple ? t(spec.simpleKey, spec.simpleVars) : spec.label;
            return (
              <button
                key={spec.id}
                type="button"
                className="run-dlg-key"
                disabled={!terminalConn}
                onPointerDown={(event) => event.preventDefault()}
                onClick={() => sendKey(spec)}
              >
                {label}
              </button>
            );
          })}
        </div>
      )}

      {shownError && (
        <p className="run-dlg-composer-error" role="alert">
          {shownError}
        </p>
      )}

      <div className="run-dlg-composer-row">
        <textarea
          ref={textareaRef}
          className="run-dlg-textarea"
          rows={2}
          value={text}
          disabled={!state.enabled || busy}
          placeholder={placeholder}
          aria-label={placeholder}
          onChange={(event) => setText(event.target.value)}
          onCompositionStart={() => {
            composingRef.current = true;
          }}
          onCompositionEnd={() => {
            composingRef.current = false;
          }}
          onKeyDown={onKeyDown}
        />

        <div className="run-dlg-composer-actions">
          {state.enabled && (
            <button
              type="button"
              className="primary run-dlg-send"
              disabled={busy || sendingCr || !text.trim()}
              onClick={() => void submit()}
            >
              {actionLabel}
            </button>
          )}
          {state.showJumpLatest && onJumpLatest && (
            <button type="button" className="run-dlg-jump-latest" onClick={onJumpLatest}>
              {t(simple ? "runDialog.simple.continuedBelow" : "runDialog.continuedBelow")}
            </button>
          )}
          {state.showJumpOwner && onJumpOwner && (
            <button type="button" className="run-dlg-jump-owner" onClick={onJumpOwner}>
              {t(simple ? "runDialog.simple.shownAbove" : "runDialog.shownAbove")}
            </button>
          )}
          {state.showRedispatch && onRedispatch && (
            <button type="button" className="run-dlg-redispatch" onClick={onRedispatch}>
              {t("runs.dispatch")}
            </button>
          )}
          {canEnd && onEnd && (
            <button type="button" className="run-dlg-done" disabled={ending} onClick={onEnd}>
              {t(simple ? "runDialog.simple.done" : "runDialog.done")}
            </button>
          )}
          {showStop && (
            <button type="button" className="danger run-dlg-stop" disabled={stopping} onClick={onStop}>
              {t("runDialog.stop")}
            </button>
          )}
        </div>
      </div>
    </div>
  );
});
