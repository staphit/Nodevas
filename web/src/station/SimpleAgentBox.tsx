// The describe box (single-user edition): say what you want in your own
// words and an agent on this computer does it, in one folder, as a global
// agent with no pull request. Interactive by default inside RunDialog's
// plain-word frame (§8.2), falling back to background when the folder is not
// trusted, and showing sign-in help when the agent is signed out.
//
// On StartScreen (variant "start") and on Station (variant "station"). A team
// hub renders nothing.

import { useEffect, useId, useRef, useState, type ReactNode } from "react";
import { createPortal } from "react-dom";

import {
  BusyError,
  ConflictError,
  GlobalRunError,
  MAX_GLOBAL_TASK_BYTES,
  type BusyResponse,
  type CreateGlobalRunRequest,
  type OnBusy,
  type Run,
  type RunnerRepo,
} from "../api";
import { agentName } from "../game/labels";
import { CastPortrait } from "../game/warroom/castArt";
import { IconComputer } from "../icons";
import { useI18n } from "../i18n";
import { AddRepoDialog } from "../local/AddRepoDialog";
import { AgentScanStep } from "../local/AgentScanStep";
import { FirstFolderStep } from "../local/FirstFolderStep";
import { LocalNotReady } from "../local/LocalNotReady";
import { useEdition, useLocalRunner, useLocalState } from "../runners/edition";
import { useRunners } from "../runners/runnerStore";
import { useBudget } from "../runs/budgetStore";
import { BusyChoice } from "../runs/coord/BusyChoice";
import { useCoordStore } from "../runs/coord/coordStore";
import { RunDialog } from "../runs/dialog/RunDialog";
import { defaultModel, installedAgents } from "../runs/DispatchDialog";
import { defaultDispatchMode } from "../runs/RunModePicker";
import { useRuns } from "../runs/runStore";
import { runSignedOut, signInCommand, signedOutText } from "../runs/signedOut";
import { useSimpleMode } from "../simple/simpleMode";
import { useApp, usePreference } from "../store";
import { startFailure } from "./GlobalAgentForm";
import { useGlobalAgents } from "./globalAgentsStore";
import { openGlobalAgents } from "./openGlobalAgents";
import { AgentCards } from "./pickers/AgentCards";
import { AgentPlainAvatar } from "./pickers/PictureCards";
import { RepoCards } from "./pickers/RepoCards";
import { offerIsStarting } from "./waiting";

const MIN_ROWS = 3;
const MAX_ROWS = 12;

function utf8Length(text: string): number {
  return new TextEncoder().encode(text).length;
}

function minutesSince(iso: string | undefined): number {
  if (!iso) return 1;
  const ms = Date.now() - Date.parse(iso);
  if (!Number.isFinite(ms) || ms <= 0) return 1;
  return Math.max(1, Math.round(ms / 60_000));
}

function poseFor(run: Run): string {
  if (run.status === "running") return run.mode === "interactive" ? "speak" : "write";
  if (run.status === "succeeded") return "cheer";
  if (run.status === "failed") return "frustrated";
  return "idle";
}

/** The refusals the team form says in runner terms (and with a command to
 * type), said plainly for this computer; "" for the rest, which
 * startFailure words. */
function plainStartProblem(t: ReturnType<typeof useI18n>["t"], error: unknown): string {
  if (error instanceof GlobalRunError) {
    if (error.status === 409 && (error.code === "GLOBAL_RUNS_OFF" || error.blocked === "off")) {
      return t("globalAgents.simple.off");
    }
    if (error.status === 409 || error.status === 412 || error.code === "RUNNER_OUTDATED") {
      return t("globalAgents.simple.cannotStart");
    }
    if (error.status === 503 || error.code === "RUNNER_OFFLINE") return t("local.unavailable");
    return "";
  }
  return error instanceof ConflictError ? t("globalAgents.simple.cannotStart") : "";
}

export function SimpleAgentBox({ variant, onStarted }: { variant: "start" | "station"; onStarted?: (run: Run) => void }) {
  const single = useEdition() === "single";
  if (!single) return null;
  return <SimpleAgentBody variant={variant} onStarted={onStarted} />;
}

function SimpleAgentBody({ variant, onStarted }: { variant: "start" | "station"; onStarted?: (run: Run) => void }) {
  const { t } = useI18n();
  const titleId = useId();
  const fieldId = useId();
  const simple = useSimpleMode();
  const runner = useLocalRunner();
  const state = useLocalState();
  const loaded = useRunners((s) => s.loaded);
  const kindPref = usePreference("simpleAgentKind");
  const repoPref = usePreference("simpleAgentRepoId");
  const [task, setTask] = useState("");
  const [problem, setProblem] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [started, setStarted] = useState<Run | null>(null);
  const [pendingBusy, setPendingBusy] = useState<BusyResponse | null>(null);
  const [busyChanged, setBusyChanged] = useState(false);
  const [changing, setChanging] = useState(false);
  const [scanOpen, setScanOpen] = useState(false);
  const [picking, setPicking] = useState(false);
  const taskRef = useRef<HTMLTextAreaElement>(null);

  useEffect(() => {
    if (!useRunners.getState().loaded) void useRunners.getState().refresh();
  }, []);

  const agents = installedAgents(runner);
  const agent = agents.find((item) => item.kind === kindPref) ?? agents[0];
  const repos = runner?.info?.repos ?? [];
  const repo = repos.find((item) => item.id === repoPref) ?? repos[0];

  const defaultMode = defaultDispatchMode({
    runner,
    agent,
    viewer: null,
    edition: "single",
  });
  const mode = defaultMode.mode;

  // Preflight global busy for the picked folder.
  useEffect(() => {
    if (!runner?.online || !repo?.id) return;
    void useCoordStore.getState().loadGlobalBusy(runner.id, [repo.id]);
  }, [runner?.id, runner?.online, repo?.id]);

  const preflightBusy = useCoordStore((s) =>
    runner?.id && repo?.id ? s.globalBusy[runner.id]?.[repo.id] : undefined,
  );
  const hasPreflightBusy = Boolean(
    preflightBusy && ((preflightBusy.holders?.length ?? 0) > 0 || preflightBusy.others > 0),
  );

  const pickRepo = (picked: RunnerRepo) => useApp.getState().updateUIPreference("simpleAgentRepoId", picked.id);
  const pickAgent = (kind: string) =>
    useApp.getState().updateUIPreference("simpleAgentKind", kind as typeof kindPref);

  // The run last started here, as the hub now tells it: the run store's
  // copy first (a push lands there), then the global list's.
  const startedId = started?.id ?? "";
  const stored = useRuns((s) => (startedId ? s.globalById[startedId] : undefined));
  const listed = useGlobalAgents((s) => (startedId ? s.runs.find((run) => run.id === startedId) : undefined));
  const current = started ? (stored ?? listed ?? started) : null;

  const submit = async (onBusy?: OnBusy, holderIds?: string[]) => {
    if (submitting || !runner || !agent || !repo) return;
    const value = task.trim();
    if (!value) {
      setProblem(t("globalAgents.simple.empty"));
      taskRef.current?.focus();
      return;
    }
    if (utf8Length(value) > MAX_GLOBAL_TASK_BYTES) {
      setProblem(t("globalAgents.simple.tooLong"));
      return;
    }
    setSubmitting(true);
    setProblem("");
    if (!onBusy) setStarted(null);
    const body: CreateGlobalRunRequest = {
      runnerId: runner.id,
      repoId: repo.id,
      agent: agent.kind,
      model: defaultModel(agent),
      ...(mode === "interactive" ? { mode: "interactive" as const } : {}),
      task: value,
      openPR: false,
      simple,
      ...(onBusy ? { onBusy } : {}),
      ...(holderIds && holderIds.length > 0 ? { holderIds } : {}),
    };
    try {
      const run = await useGlobalAgents.getState().create(body);
      setTask("");
      setPendingBusy(null);
      setBusyChanged(false);
      setStarted(run);
      onStarted?.(run);
    } catch (caught) {
      if (caught instanceof BusyError) {
        setPendingBusy(caught.busy);
        setBusyChanged(Boolean(onBusy));
        return;
      }
      const plain = plainStartProblem(t, caught);
      const failure = startFailure(t, caught, { runner: "", interactive: mode === "interactive" });
      setProblem(plain || failure.text);
      if (failure.budget) void useBudget.getState().load();
    } finally {
      setSubmitting(false);
    }
  };

  let content: ReactNode;
  if (!runner?.online) {
    content = loaded ? (
      <LocalNotReady starting={state === "starting"} />
    ) : (
      <p className="run-empty" role="status">
        {t("common.loading")}
      </p>
    );
  } else if (agents.length === 0) {
    // Signed-out agent: show #17's sign-in help instead of "no agent".
    const signedOut = runner.info?.agents?.find((item) => item.signedOut);
    content = (
      <div className="run-empty run-empty-left simple-agent-need">
        <p>
          {signedOut
            ? t("result.signedOut", { agent: agentName(signedOut.kind), command: signInCommand(signedOut.kind) })
            : t("globalAgents.simple.needAgent")}
        </p>
        <button type="button" className="ap-btn is-primary" onClick={() => setScanOpen(true)}>
          {t("globalAgents.simple.setUpAgent")}
        </button>
      </div>
    );
  } else if (!repo) {
    content = <FirstFolderStep runnerId={runner.id} onAdded={pickRepo} />;
  } else {
    const rows = Math.min(MAX_ROWS, Math.max(MIN_ROWS, task.split("\n").length + 1));
    content = (
      <>
        <form
          className="simple-agent-form"
          onSubmit={(event) => {
            event.preventDefault();
            void submit();
          }}
        >
          {hasPreflightBusy && !pendingBusy && (
            <p className="run-hint simple-busy-preflight" role="status" data-testid="simple-busy-preflight">
              {t("busy.simple.title", { minutes: minutesSince(preflightBusy?.holders?.[0]?.since) })}
            </p>
          )}
          {defaultMode.reason === "untrusted" && (
            <p className="run-hint simple-agent-untrusted" role="note">
              {t("runDialog.simple.background.untrusted", { agent: agentName(agent.kind) })}
            </p>
          )}
          <textarea
            ref={taskRef}
            id={fieldId}
            value={task}
            rows={rows}
            aria-labelledby={titleId}
            placeholder={t("globalAgents.simple.placeholder")}
            aria-invalid={problem ? true : undefined}
            onChange={(event) => {
              setTask(event.target.value);
              setProblem("");
            }}
            onKeyDown={(event) => {
              if (event.key === "Enter" && (event.ctrlKey || event.metaKey)) {
                event.preventDefault();
                void submit();
              }
            }}
          />
          {problem && (
            <p className="notify-message error" role="alert">
              {problem}
            </p>
          )}
          {pendingBusy && (
            <>
              {busyChanged && (
                <p className="notify-message error" role="status">
                  {t("busy.changed")}
                </p>
              )}
              <BusyChoice
                busy={pendingBusy}
                simple
                maxConcurrent={runner.info?.maxConcurrent}
                onChoose={(choice) => {
                  const holderIds = (pendingBusy.holders ?? []).map((h) => h.runId);
                  void submit(choice as OnBusy, holderIds);
                }}
                onCancel={() => {
                  setPendingBusy(null);
                  setBusyChanged(false);
                }}
              />
            </>
          )}
          {agents.length > 1 && (
            <AgentCards
              agents={runner.info?.agents ?? []}
              value={agent.kind}
              onChange={pickAgent}
              simple
            />
          )}
          <div className="simple-agent-foot">
            <span className="simple-agent-using">
              {t("globalAgents.simple.using", { agent: agentName(agent.kind), folder: repo.name })}{" "}
              <button
                type="button"
                className="run-link-button"
                aria-expanded={changing}
                onClick={() => setChanging((open) => !open)}
              >
                {t("globalAgents.simple.change")}
              </button>
            </span>
            <button type="submit" className="ap-btn is-primary" disabled={submitting}>
              {submitting ? t("globalAgents.form.starting") : t("globalAgents.form.start")}
            </button>
          </div>
          {changing && (
            <div className="simple-agent-change">
              <RepoCards
                mode="one"
                repos={repos}
                value={repo.id}
                onChange={(id) => useApp.getState().updateUIPreference("simpleAgentRepoId", id)}
                simple
              />
              <span className="simple-agent-change-links">
                <button type="button" className="run-link-button" onClick={() => setPicking(true)}>
                  {t("local.firstFolder.pickExisting")}
                </button>
                <button type="button" className="run-link-button" onClick={() => setScanOpen(true)}>
                  {t("local.scan.title")}
                </button>
              </span>
            </div>
          )}
        </form>
        {current && (
          <div className="simple-agent-dialog">
            <RunDialog
              runId={current.id}
              variant="plain"
              simple
              header={<StartedHeader run={current} builtInRunnerId={runner.id} />}
              onRunCreated={(next) => {
                setStarted(next);
                onStarted?.(next);
              }}
            />
          </div>
        )}
      </>
    );
  }

  const title =
    variant === "station" ? (
      <div className="station-panel-head">
        <h2 id={titleId}>{t("globalAgents.simple.title")}</h2>
      </div>
    ) : (
      <p className="simple-agent-title" id={titleId}>
        {t("globalAgents.simple.title")}
      </p>
    );

  return (
    <section
      className={`simple-agent-box is-${variant}${variant === "station" ? " station-panel" : ""}`}
      aria-labelledby={titleId}
    >
      {title}
      {content}
      {scanOpen && <AgentScanDialog onClose={() => setScanOpen(false)} />}
      {picking && runner && (
        <AddRepoDialog
          runnerId={runner.id}
          plain
          onClose={() => setPicking(false)}
          onAdded={(picked) => {
            setPicking(false);
            pickRepo(picked);
          }}
        />
      )}
    </section>
  );
}

/** Header inside the inline RunDialog: character pose portrait plus plain-word
 * run status and jump link to Global agents (§11 item 15). */
function StartedHeader({ run, builtInRunnerId }: { run: Run; builtInRunnerId: string }) {
  const { t } = useI18n();
  const open = (label: string) => (
    <button type="button" className="run-link-button" onClick={() => openGlobalAgents(run.id)}>
      {label}
    </button>
  );
  let text: string;
  let link: ReactNode = null;
  let tone = "";
  const justStarting = run.status === "offered" && offerIsStarting(run, builtInRunnerId);
  switch (justStarting ? "queued" : run.status) {
    case "succeeded":
      if (run.workspace === "merged_local") {
        text = t("simple.box.applied");
        tone = " is-done";
      } else if (run.commit) {
        text = t("simple.box.done");
        link = open(t("simple.box.seeResult"));
        tone = " is-done";
      } else {
        text = t("simple.box.noChange");
        link = open(t("simple.box.seeWhat"));
      }
      break;
    case "failed":
      text = runSignedOut(run) ? signedOutText(t, run, "local") : t("simple.box.failed");
      link = open(t("simple.box.seeWhat"));
      tone = " is-failed";
      break;
    case "cancelled":
      text = t("simple.box.stopped");
      break;
    case "declined":
      text = t("simple.box.declined");
      break;
    case "offered":
      text = t("simple.box.waitingOk");
      link = open(t("globalAgents.simple.watch"));
      break;
    default:
      text = t("globalAgents.simple.started");
      link = open(t("globalAgents.simple.watch"));
  }
  return (
    <div className={`simple-agent-started${tone}`} role="status" data-testid="simple-agent-status">
      <CastPortrait
        agent={run.agent}
        model={run.model || "default"}
        scene={poseFor(run)}
        className="simple-agent-face"
        fallback={<AgentPlainAvatar agent={run.agent} />}
      />
      <span>
        {text}
        {link && " "}
        {link}
      </span>
    </div>
  );
}

/** "Set one up": the agent scan in a dialog of its own. */
function AgentScanDialog({ onClose }: { onClose: () => void }) {
  const { t } = useI18n();
  const titleId = useId();
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return;
      if (document.querySelector('[role="alertdialog"]')) return;
      const open = document.querySelectorAll('[role="dialog"][aria-modal="true"]');
      if (open[open.length - 1] !== ref.current) return;
      event.stopPropagation();
      onClose();
    };
    window.addEventListener("keydown", onKey, true);
    return () => window.removeEventListener("keydown", onKey, true);
  }, [onClose]);

  return createPortal(
    <div
      className="confirm-backdrop repo-add-backdrop"
      onMouseDown={(event) => {
        if (event.target === event.currentTarget) onClose();
      }}
      onClick={(event) => event.stopPropagation()}
    >
      <div
        ref={ref}
        className="confirm-dialog repo-add-dialog agent-scan-dialog"
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
      >
        <header>
          <span className="confirm-dialog-icon">
            <IconComputer size={17} />
          </span>
          <div>
            <h2 id={titleId}>{t("local.scan.title")}</h2>
            <p>{t("local.scan.lead")}</p>
          </div>
        </header>
        <div className="repo-add-body">
          <AgentScanStep variant="step" />
        </div>
        <footer>
          <button type="button" onClick={onClose}>
            {t("common.close")}
          </button>
        </footer>
      </div>
    </div>,
    document.body,
  );
}
