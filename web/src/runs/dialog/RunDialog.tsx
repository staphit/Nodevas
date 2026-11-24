import {
  Suspense,
  useCallback,
  useMemo,
  useRef,
  useState,
  type CSSProperties,
  type MutableRefObject,
  type ReactNode,
  type Ref,
} from "react";
import {
  runFinal,
  runInteractive,
  type Run,
  type RunAsk,
  type TerminalConnection,
} from "../../api";
import { AskCard } from "../../agents/AskCard";
import { canAnswerAsk, useAgents } from "../../agents/agentStore";
import { useViewer } from "../../components/SignIn";
import { GameDialog } from "../../game/GameActions";
import { agentName, agentWho } from "../../game/labels";
import { useI18n } from "../../i18n";
import { lazyWithRetry } from "../../lazyWithRetry";
import { BusyChoice } from "../coord/BusyChoice";
import { SimpleRunSummary } from "../SimpleRunSummary";
import type { TerminalModeReader } from "../RunTerminal";
import { readStoryCssVars } from "../terminalKeys";
import { useLiveTerminalClaim } from "./liveTerminals";
import { RunComposer, type ComposerState, type RunComposerHandle } from "./RunComposer";
import { RunTranscript } from "./RunTranscript";
import { useRunDialog } from "./useRunDialog";

// Keep xterm out of the main bundle: only interactive runs load RunTerminal.
const RunTerminal = lazyWithRetry(() => import("../RunTerminal"));

/** Mirrors runnerwire.DefaultTermKeep (10 minutes). */
export const DEFAULT_TERM_KEEP_MS = 10 * 60 * 1000;

export interface RunDialogProps {
  runId: string;
  variant: "story" | "plain";
  simple?: boolean;
  /** Caller-supplied header (e.g. character plate + flavour line). */
  header?: ReactNode;
  /** Caller-supplied empty-state flavour line shown before the first event. */
  emptyLine?: ReactNode;
  /** Whether this dialog watches live events (default true). */
  subscribed?: boolean;
  /** Internal: true when rendered inside the "Open large" modal. */
  expanded?: boolean;
  /** Imperative composer ref (for §C "Tell it" prefill). */
  composerRef?: Ref<RunComposerHandle>;
  /** Called when "Dispatch" is pressed on an ended node run. */
  onRedispatch?: (nodeId: string) => void;
  /** Called when a follow-up or new-work run is created. */
  onRunCreated?: (run: Run) => void;
}

function isScreenExpired(run: Run, now = Date.now()): boolean {
  if (!runFinal(run.status)) return false;
  if (!run.finishedAt) return false;
  const finishedMs = Date.parse(run.finishedAt);
  if (!Number.isFinite(finishedMs)) return false;
  return now - finishedMs >= DEFAULT_TERM_KEEP_MS;
}

/**
 * The one dialog component (§3.1, §8.2) used in StoryTalk, SimpleAgentBox, and
 * GlobalAgentList. Renders a live themed xterm for interactive runs (lazily
 * loaded, single owner per page) or a structured transcript for background
 * runs, plus the §4 state-driven composer and "Open large" modal.
 */
export function RunDialog({
  runId,
  variant,
  simple = false,
  header,
  emptyLine,
  subscribed = true,
  expanded = false,
  composerRef,
  onRedispatch,
  onRunCreated,
}: RunDialogProps) {
  const { t } = useI18n();
  const viewer = useViewer();
  const rootRef = useRef<HTMLDivElement>(null);

  const [terminalConn, setTerminalConn] = useState<TerminalConnection | null>(null);
  const [terminalReadonly, setTerminalReadonly] = useState(false);
  const [screenAsks, setScreenAsks] = useState(false);
  const modesRef = useRef<TerminalModeReader | null>(null);
  const [tab, setTab] = useState<"screen" | "steps">("screen");
  const [largeOpen, setLargeOpen] = useState(false);
  const [portalVars, setPortalVars] = useState<Record<string, string>>({});

  const dialog = useRunDialog({
    runId,
    simple,
    subscribed,
    terminalReadonly,
    screenAsks,
    onRunCreated,
  });

  const run = dialog.run;
  const interactive = Boolean(run && runInteractive(run));
  const screenExpired = Boolean(run && interactive && isScreenExpired(run));
  const wantTerminal = Boolean(run && interactive && !screenExpired);

  const { isOwner, ref: claimRef, focusOwner } = useLiveTerminalClaim(
    run?.id ?? "",
    wantTerminal,
    { takeOver: expanded },
  );

  // Show Screen / Steps tabs when an interactive run also has structured steps.
  const hasSteps = useMemo(
    () =>
      dialog.lines.some(
        (line) => line.kind === "text" || line.kind === "tool" || line.kind === "user",
      ),
    [dialog.lines],
  );
  const showTabs = wantTerminal && hasSteps;

  const isBracketedPaste = useCallback(
    () => Boolean(modesRef.current?.bracketedPaste()),
    [],
  );
  const isApplicationCursor = useCallback(
    () => Boolean(modesRef.current?.applicationCursor()),
    [],
  );
  const handleModes = useCallback((reader: TerminalModeReader | null) => {
    modesRef.current = reader;
  }, []);

  // Inline AskCard renderer for pending node-run asks (§3.5, §4).
  const agentAsks = useAgents((s) => s.asks);
  const submittingAsks = useAgents((s) => s.submittingAskIds);
  const answerAsk = useAgents((s) => s.answerAsk);

  const renderInlineAsk = useCallback(
    (ask: RunAsk) => {
      const fullAsk = agentAsks.find((item) => item.id === ask.id);
      if (!fullAsk) return null;
      return (
        <AskCard
          ask={fullAsk}
          submitting={Boolean(submittingAsks[fullAsk.id])}
          canAnswer={canAnswerAsk(fullAsk, viewer)}
          onAnswer={(payload) => {
            void answerAsk(fullAsk.id, payload);
          }}
        />
      );
    },
    [agentAsks, submittingAsks, viewer, answerAsk],
  );

  const openLarge = () => {
    setPortalVars(readStoryCssVars(rootRef.current));
    setLargeOpen(true);
  };
  // The composer handle is the caller's (Tell it, Start new work) and this
  // dialog's own (clearing the text once a busy choice started the work).
  const ownComposerRef = useRef<RunComposerHandle | null>(null);
  const setComposerRef = useCallback(
    (handle: RunComposerHandle | null) => {
      ownComposerRef.current = handle;
      if (typeof composerRef === "function") composerRef(handle);
      else if (composerRef) (composerRef as MutableRefObject<RunComposerHandle | null>).current = handle;
    },
    [composerRef],
  );

  // Stable, so the modal's effects never re-run on this dialog's re-renders.
  const closeLarge = useCallback(() => setLargeOpen(false), []);

  // Another mount owns this run's live screen (one xterm per run per page):
  // typing here could not reach it, so the composer says where it is.
  const composerState = useMemo<ComposerState | null>(() => {
    const state = dialog.composerState;
    if (!state || !wantTerminal || isOwner) return state;
    if (state.kind !== "interactive" && state.kind !== "screen_asks") return state;
    return {
      kind: "shown_above",
      enabled: false,
      showStop: true,
      showJumpOwner: true,
      placeholderKey: "runDialog.ph.shownAbove",
    };
  }, [dialog.composerState, wantTerminal, isOwner]);

  if (!run) return null;

  const who = agentWho(run.agent, run.model);
  const label = t("runDialog.label", { who });
  const classes = [
    "run-dialog",
    variant === "story" ? "run-dialog-story" : "run-dialog-plain",
    simple ? "is-simple" : "",
    expanded ? "is-expanded" : "",
  ]
    .filter(Boolean)
    .join(" ");

  const renderInteractiveNotice = () => {
    if (run.endReason === "quiet") {
      return (
        <p className="run-dlg-notice" role="status">
          {t(simple ? "runDialog.simple.end.quiet" : "runDialog.end.quiet")}
        </p>
      );
    }
    if (run.endReason === "quiet_waiting") {
      return (
        <p className="run-dlg-notice" role="status">
          {t(simple ? "runDialog.simple.end.quietWaiting" : "runDialog.end.quietWaiting")}
        </p>
      );
    }
    return null;
  };

  const renderBody = () => {
    const isLive = !runFinal(run.status);
    // Expired interactive screen (>10 minutes after finish): show summary / steps.
    if (interactive && screenExpired) {
      if (simple) {
        return (
          <SimpleRunSummary run={run}>
            <RunTranscript
              lines={dialog.lines}
              simple
              live={false}
              emptyLine={emptyLine}
              onWithdraw={(id) => void dialog.withdrawRun(id)}
            />
          </SimpleRunSummary>
        );
      }
      return (
        <div className="run-dlg-expired">
          {renderInteractiveNotice()}
          <p className="run-dlg-screen-gone">{t("runDialog.screenGone")}</p>
          {run.summary && <pre className="run-dlg-summary-lines">{run.summary}</pre>}
          {hasSteps && (
            <RunTranscript
              lines={dialog.lines}
              live={false}
              onWithdraw={(id) => void dialog.withdrawRun(id)}
            />
          )}
        </div>
      );
    }

    // Active (or recently ended) interactive run: lazy RunTerminal with single-owner guard.
    if (wantTerminal) {
      return (
        <div className="run-dlg-interactive" ref={claimRef}>
          {simple && (
            <p className="run-dlg-simple-frame">
              {t("runDialog.simple.frame", { agent: agentName(run.agent) })}
            </p>
          )}
          {showTabs && (
            <div className="run-dlg-tabs" role="tablist">
              <button
                type="button"
                role="tab"
                aria-selected={tab === "screen"}
                className={tab === "screen" ? "active" : undefined}
                onClick={() => setTab("screen")}
              >
                {t("runDialog.screen")}
              </button>
              <button
                type="button"
                role="tab"
                aria-selected={tab === "steps"}
                className={tab === "steps" ? "active" : undefined}
                onClick={() => setTab("steps")}
              >
                {t("runDialog.steps")}
              </button>
            </div>
          )}
          <div hidden={showTabs && tab !== "screen"}>
            {isOwner ? (
              <Suspense fallback={<p className="run-empty">{t("terminal.loading")}</p>}>
                <RunTerminal
                  run={run}
                  hideKeys
                  onConnection={setTerminalConn}
                  onReadonlyChange={setTerminalReadonly}
                  onScreenAsksChange={setScreenAsks}
                  onModes={handleModes}
                />
              </Suspense>
            ) : (
              <div className="run-dlg-shown-above">
                <button type="button" onClick={focusOwner}>
                  {t("runDialog.shownAbove")}
                </button>
              </div>
            )}
            {renderInteractiveNotice()}
          </div>
          {showTabs && (
            <div hidden={tab !== "steps"}>
              <RunTranscript
                lines={dialog.lines}
                simple={simple}
                live={isLive}
                emptyLine={emptyLine}
                onWithdraw={(id) => void dialog.withdrawRun(id)}
                renderAsk={renderInlineAsk}
              />
            </div>
          )}
        </div>
      );
    }

    // Background run: structured transcript.
    return (
      <RunTranscript
        lines={dialog.lines}
        simple={simple}
        live={isLive}
        emptyLine={emptyLine}
        onWithdraw={(id) => void dialog.withdrawRun(id)}
        renderAsk={renderInlineAsk}
      />
    );
  };

  return (
    <>
      <section ref={rootRef} className={classes} aria-label={label}>
        <div className="run-dlg-head">
          {header && <div className="run-dlg-header-slot">{header}</div>}
          {!expanded && (
            <button type="button" className="run-dlg-expand" onClick={openLarge}>
              {t("runDialog.expand")}
            </button>
          )}
        </div>

        <div className="run-dlg-body">{renderBody()}</div>

        {dialog.busy && (
          <BusyChoice
            busy={dialog.busy}
            simple={simple}
            maxConcurrent={dialog.runner?.info?.maxConcurrent}
            onChoose={(choice) => {
              void dialog.chooseBusy(choice).then((started) => {
                if (started) ownComposerRef.current?.prefill("");
              });
            }}
            onCancel={dialog.cancelBusy}
          />
        )}

        {composerState && (
          <RunComposer
            ref={setComposerRef}
            state={composerState}
            simple={simple}
            busy={dialog.submitting}
            terminalConn={wantTerminal && isOwner ? terminalConn : null}
            isBracketedPaste={isBracketedPaste}
            isApplicationCursor={isApplicationCursor}
            onSubmitText={dialog.submitText}
            canEnd={dialog.canEnd}
            ending={dialog.ending}
            onEnd={() => void dialog.endRun()}
            stopping={dialog.stopping}
            onStop={() => void dialog.stopRun()}
            onJumpLatest={dialog.focusLatestTurn}
            onJumpOwner={focusOwner}
            onRedispatch={
              run.nodeId && onRedispatch ? () => onRedispatch(run.nodeId) : undefined
            }
            error={dialog.error}
          />
        )}
      </section>

      {largeOpen && !expanded && (
        <GameDialog title={label} onClose={closeLarge}>
          <div className="run-dialog-portal-theme" style={portalVars as CSSProperties}>
            <RunDialog
              runId={run.id}
              variant={variant}
              simple={simple}
              header={header}
              emptyLine={emptyLine}
              subscribed={subscribed}
              expanded
              onRedispatch={onRedispatch}
              onRunCreated={onRunCreated}
            />
          </div>
        </GameDialog>
      )}
    </>
  );
}
