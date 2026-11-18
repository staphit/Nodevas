// The describe box (single-user edition): say what you want in your own
// words and an agent on this computer does it, in one folder, as a global
// agent with no pull request. Whatever stands in the way comes first, one
// thing at a time: this computer still starting, no agent ready ("Set one
// up" opens the agent scan), no folder (the first folder step). Which agent
// and which folder are remembered (simpleAgentKind, simpleAgentRepoId): the
// agent is picked right in the box (with more than one ready), the folder
// behind "Change". In Simple the hub adds its plain-words instruction to the
// task (`simple: true`); it lifts no gate. Once started, a line follows the
// run (at work, done, ended without a result) while the box is ready for
// the next request.
//
// On StartScreen (variant "start") and on Station (variant "station"). A team
// hub renders nothing.

import { useEffect, useId, useRef, useState, type ReactNode } from "react";
import { createPortal } from "react-dom";

import {
  ConflictError,
  GlobalRunError,
  MAX_GLOBAL_TASK_BYTES,
  type CreateGlobalRunRequest,
  type Run,
  type RunnerRepo,
} from "../api";
import { agentName } from "../game/labels";
import { IconComputer } from "../icons";
import { useI18n } from "../i18n";
import { AddRepoDialog } from "../local/AddRepoDialog";
import { AgentScanStep } from "../local/AgentScanStep";
import { FirstFolderStep } from "../local/FirstFolderStep";
import { LocalNotReady } from "../local/LocalNotReady";
import { useEdition, useLocalRunner, useLocalState } from "../runners/edition";
import { useRunners } from "../runners/runnerStore";
import { useBudget } from "../runs/budgetStore";
import { useRuns } from "../runs/runStore";
import { defaultModel, installedAgents } from "../runs/DispatchDialog";
import { useSimpleMode } from "../simple/simpleMode";
import { useApp, usePreference } from "../store";
import { startFailure } from "./GlobalAgentForm";
import { useGlobalAgents } from "./globalAgentsStore";
import { openGlobalAgents } from "./openGlobalAgents";
import { offerIsStarting } from "./waiting";

const MIN_ROWS = 3;
const MAX_ROWS = 12;

function utf8Length(text: string): number {
  return new TextEncoder().encode(text).length;
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

  const pickRepo = (picked: RunnerRepo) => useApp.getState().updateUIPreference("simpleAgentRepoId", picked.id);
  const pickAgent = (kind: string) =>
    useApp.getState().updateUIPreference("simpleAgentKind", kind as typeof kindPref);

  // The run last started here, as the hub now tells it: the run store's
  // copy first (a push lands there), then the global list's.
  const startedId = started?.id ?? "";
  const stored = useRuns((s) => (startedId ? s.globalById[startedId] : undefined));
  const listed = useGlobalAgents((s) => (startedId ? s.runs.find((run) => run.id === startedId) : undefined));
  const current = started ? (stored ?? listed ?? started) : null;

  const submit = async () => {
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
    setStarted(null);
    const body: CreateGlobalRunRequest = {
      runnerId: runner.id,
      repoId: repo.id,
      agent: agent.kind,
      model: defaultModel(agent),
      task: value,
      openPR: false,
      simple,
    };
    try {
      const run = await useGlobalAgents.getState().create(body);
      setTask("");
      setStarted(run);
      onStarted?.(run);
    } catch (caught) {
      const plain = plainStartProblem(t, caught);
      const failure = startFailure(t, caught, { runner: "", interactive: false });
      setProblem(plain || failure.text);
      if (failure.budget) void useBudget.getState().load();
    } finally {
      setSubmitting(false);
    }
  };

  let content;
  if (!runner?.online) {
    content = loaded ? (
      <LocalNotReady starting={state === "starting"} />
    ) : (
      <p className="run-empty" role="status">
        {t("common.loading")}
      </p>
    );
  } else if (agents.length === 0) {
    content = (
      <div className="run-empty run-empty-left simple-agent-need">
        <p>{t("globalAgents.simple.needAgent")}</p>
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
      <form
        className="simple-agent-form"
        onSubmit={(event) => {
          event.preventDefault();
          void submit();
        }}
      >
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
        {current && <StartedLine run={current} builtInRunnerId={runner.id} />}
        <div className="simple-agent-foot">
          {agents.length > 1 && (
            <label className="simple-agent-who">
              <span>{t("globalAgents.simple.agent")}</span>
              <select value={agent.kind} onChange={(event) => pickAgent(event.target.value)}>
                {agents.map((item) => (
                  <option key={item.kind} value={item.kind}>
                    {agentName(item.kind)}
                  </option>
                ))}
              </select>
            </label>
          )}
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
            <label className="run-field">
              <span>{t("globalAgents.simple.folder")}</span>
              <select
                value={repo.id}
                onChange={(event) => useApp.getState().updateUIPreference("simpleAgentRepoId", event.target.value)}
              >
                {repos.map((item) => (
                  <option key={item.id} value={item.id}>
                    {item.name}
                  </option>
                ))}
              </select>
            </label>
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

/** How the run started here stands, in plain words, following it: at work
 * (or waiting for the person's OK), done (pointing to the result buttons),
 * done with nothing changed, applied, or ended without a result. Each with
 * the way to see it. An offer waits for an OK only when it asks: the
 * built-in runner's offer from the person's click is just starting (the
 * rule waiting.ts uses). */
function StartedLine({ run, builtInRunnerId }: { run: Run; builtInRunnerId: string }) {
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
      text = t("simple.box.failed");
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
    <p className={`simple-agent-started${tone}`} role="status">
      {text}
      {link && " "}
      {link}
    </p>
  );
}

/** "Set one up": the agent scan in a dialog of its own. */
function AgentScanDialog({ onClose }: { onClose: () => void }) {
  const { t } = useI18n();
  const titleId = useId();
  const ref = useRef<HTMLDivElement>(null);

  // Escape closes this one only, and only while it is on top: the file
  // picker opened over it closes first.
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
