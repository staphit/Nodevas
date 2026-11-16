// Starts a global agent (agent workspace, phase 2): an agent on one of the
// viewer's own runners, in one of that runner's registered repositories, for
// no project and no node. Runner, then repository, then agent and model, the
// mode, where its branch starts and what it is called, then the task. On a
// team hub nothing starts here: the runner shows the offer (or takes it by
// its own policy). In the single-user edition there is no runner to pick:
// this computer's built-in runner starts it, and repos are added right here.

import { useCallback, useEffect, useId, useMemo, useRef, useState, type ReactNode } from "react";

import {
  AuthError,
  CAP_GLOBAL_RUNS,
  CAP_GLOBAL_RUNS_ON,
  ConflictError,
  GlobalRunError,
  globalTitle,
  MAX_GLOBAL_TASK_BYTES,
  RequestError,
  slugFrom,
  validSlug,
  type CreateGlobalRunRequest,
  type Run,
  type Runner,
  type RunnerLoad,
} from "../api";
import { useViewer } from "../components/SignIn";
import { agentName } from "../game/labels";
import { useI18n } from "../i18n";
import { LocalNotReady } from "../local/LocalNotReady";
import { permissionOf } from "../local/ThisComputerPanel";
import { builtInRunner, localState, useEdition } from "../runners/edition";
import { runnerDisplayName, runnerModelLabel } from "../runners/runnerName";
import { useRunners } from "../runners/runnerStore";
import { AutoAcceptNote } from "../runs/AutoAcceptNote";
import { budgetExceededText } from "../runs/BudgetSettings";
import { useBudget } from "../runs/budgetStore";
import { AddRepoButton, defaultModel, installedAgents, ownRunners, validBranchName } from "../runs/DispatchDialog";
import { effectiveDispatchMode, RunModePicker, type DispatchMode } from "../runs/RunModePicker";
import { openSettings } from "../settings/openSettings";
import { StepUpDialog } from "../stepup/StepUpDialog";
import { useStepUp } from "../stepup/stepUpStore";
import { useGlobalAgents } from "./globalAgentsStore";

type Translate = ReturnType<typeof useI18n>["t"];

/** Why a runner cannot start a global agent now, or "" when it can. */
export type GlobalRunnerBlock = "" | "offline" | "headless" | "outdated" | "off";

const BLOCKS: ReadonlySet<string> = new Set(["offline", "headless", "outdated", "off"]);

/** The command that adds a repository to a runner, run on that machine (a
 * team runner's; the single-user edition keeps it under Advanced). */
export const REPO_ADD_COMMAND = "nodevas runner repo add <path>";

/** The task's size counter is in KB; the hub's cap is MAX_GLOBAL_TASK_BYTES. */
const MAX_TASK_KB = Math.floor(MAX_GLOBAL_TASK_BYTES / 1024);

/** The task box grows with its lines up to this many. */
const MAX_TASK_ROWS = 16;
const MIN_TASK_ROWS = 4;

/** Why `runner` cannot take a global agent: the hub's word (its load on the
 * Global agents list) when there is one, else read from the runner's own
 * hello. */
export function globalRunnerBlock(runner: Runner, load: RunnerLoad | undefined): GlobalRunnerBlock {
  if (load) return BLOCKS.has(load.globalRuns) ? (load.globalRuns as GlobalRunnerBlock) : "";
  if (!runner.online) return "offline";
  const caps = runner.info?.capabilities ?? [];
  if (!caps.includes("headless")) return "headless";
  if (!caps.includes(CAP_GLOBAL_RUNS)) return "outdated";
  if (!caps.includes(CAP_GLOBAL_RUNS_ON)) return "off";
  return "";
}

function utf8Length(text: string): number {
  return new TextEncoder().encode(text).length;
}

/** A refused start in the reader's words: the field the hub refused, and
 * whether a step-up or the budget line should follow. */
export interface StartFailure {
  text: string;
  /** The request field the hub named (task, slug, base, mode, agent, model,
   * repoId), "" for none. */
  field: string;
  /** The hub wants a fresh step-up first. */
  stepUp: boolean;
  /** The daily budget refused it: the warning line catches up. */
  budget: boolean;
}

/** The server's own text, unless it only said its status. */
function serverText(error: GlobalRunError): string {
  return error.message && error.message !== String(error.status) ? error.message : "";
}

export function startFailure(
  t: Translate,
  error: unknown,
  context: { runner: string; interactive: boolean },
): StartFailure {
  const failed = (text: string, extra: Partial<StartFailure> = {}): StartFailure => ({
    text,
    field: "",
    stepUp: false,
    budget: false,
    ...extra,
  });
  const cannotRun = t(context.interactive ? "terminal.runnerCannotInteract" : "runs.runnerCannotRun");
  if (error instanceof GlobalRunError) {
    const said = serverText(error);
    if (error.status === 400) return failed(said || error.message, { field: error.field });
    if (error.status === 403 && error.code === "STEP_UP_REQUIRED") return failed(said || error.message, { stepUp: true });
    if (error.status === 403) return failed(t("runs.permissionDenied", { message: said || error.message }));
    if (error.status === 409 && (error.code === "GLOBAL_RUNS_OFF" || error.blocked === "off")) {
      return failed(t("globalAgents.error.off", { runner: context.runner }));
    }
    if (error.status === 409) return failed(said || cannotRun);
    if (error.status === 412 || error.code === "RUNNER_OUTDATED") {
      return failed(t("globalAgents.error.outdated", { runner: context.runner }));
    }
    if (error.status === 429) return failed(t("runs.overBudget", { message: said || error.message }), { budget: true });
    return failed(said || error.message);
  }
  if (error instanceof AuthError && error.status === 403) {
    if (error.code === "STEP_UP_REQUIRED") return failed(error.message, { stepUp: true });
    return failed(t("runs.permissionDenied", { message: error.message }));
  }
  if (error instanceof ConflictError) return failed(error.serverMessage || cannotRun);
  if (error instanceof RequestError && error.status === 429) {
    return failed(t("runs.overBudget", { message: error.message }), { budget: true });
  }
  return failed(error instanceof Error ? error.message : String(error));
}

export function GlobalAgentForm({
  onStarted,
  onClose,
}: {
  onStarted?: (run: Run) => void;
  onClose?: () => void;
}) {
  const { t } = useI18n();
  const id = useId();
  const viewer = useViewer();
  const runners = useRunners((s) => s.runners);
  const runnersLoaded = useRunners((s) => s.loaded);
  const runnersLoading = useRunners((s) => s.loading);
  const runnersError = useRunners((s) => s.error);
  const loads = useGlobalAgents((s) => s.runners);

  const single = useEdition() === "single";
  const mine = useMemo(() => ownRunners(runners, viewer), [runners, viewer]);
  // Single-user: this computer's built-in runner, and no choice.
  const localRunner = single ? builtInRunner(mine) : undefined;
  const blockOf = useCallback(
    (runner: Runner) => globalRunnerBlock(runner, loads.find((load) => load.runnerId === runner.id)),
    [loads],
  );
  const eligible = useMemo(
    () => (localRunner ? [localRunner] : mine).filter((runner) => blockOf(runner) === ""),
    [localRunner, mine, blockOf],
  );
  const [addedNote, setAddedNote] = useState("");

  const [runnerId, setRunnerId] = useState("");
  const [agentKind, setAgentKind] = useState("");
  const [modelChoice, setModel] = useState("");
  const [repoChoice, setRepoId] = useState("");
  const [modeChoice, setMode] = useState<DispatchMode>("headless");
  const [base, setBase] = useState("");
  const [slugText, setSlugText] = useState("");
  const [slugEdited, setSlugEdited] = useState(false);
  const [task, setTask] = useState("");
  const [openPR, setOpenPR] = useState(false);
  const [attempted, setAttempted] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [failure, setFailure] = useState<StartFailure | null>(null);
  const [copied, setCopied] = useState(false);
  const taskRef = useRef<HTMLTextAreaElement>(null);

  // The runner list itself is the panel's to load; a form opened on its own
  // still asks once.
  useEffect(() => {
    if (!useRunners.getState().loaded) void useRunners.getState().refresh();
  }, []);

  // Whether the viewer is stepped up decides if a runner that asks for it
  // auto-accepts; asked afresh whenever the form opens.
  const [stepUpOpen, setStepUpOpen] = useState(false);
  const closeStepUp = useCallback(() => setStepUpOpen(false), []);
  useEffect(() => {
    void useStepUp.getState().load();
  }, []);

  const budgetStatus = useBudget((s) => s.status);
  useEffect(() => {
    void useBudget.getState().load();
  }, []);
  const budgetWarning = budgetExceededText(t, budgetStatus);

  // Each choice stays valid as the one above it changes (or a push replaces
  // the runner list): derived on every render, as in the dispatch dialog.
  const runner = eligible.find((item) => item.id === runnerId) ?? eligible[0];
  const runnerName = runner ? runnerDisplayName(t, runner.name) : "";
  const agents = installedAgents(runner);
  const agent = agents.find((item) => item.kind === agentKind) ?? agents[0];
  const models = agent?.models ?? [];
  const model = models.some((item) => item.id === modelChoice) ? modelChoice : defaultModel(agent);
  const repos = runner?.info?.repos ?? [];
  const repoId = repos.some((repo) => repo.id === repoChoice) ? repoChoice : (repos[0]?.id ?? "");
  const mode = effectiveDispatchMode(modeChoice, runner);

  const baseValue = base.trim();
  const baseError = baseValue && !validBranchName(baseValue) ? t("runs.baseInvalid") : "";
  const taskValue = task.trim();
  const taskBytes = utf8Length(taskValue);
  const taskError =
    taskBytes > MAX_GLOBAL_TASK_BYTES
      ? t("globalAgents.form.taskTooLong", { max: MAX_TASK_KB })
      : attempted && !taskValue
        ? t("globalAgents.form.taskRequired")
        : "";
  // The branch name follows the task's title until the person writes one.
  const derivedSlug = slugFrom(globalTitle(task));
  const slugValue = slugText.trim();
  const sendSlug = slugEdited && slugValue !== "";
  const slug = sendSlug ? slugValue : derivedSlug;
  const slugError = sendSlug && !validSlug(slugValue) ? t("globalAgents.form.slugInvalid") : "";
  const taskRows = Math.min(MAX_TASK_ROWS, Math.max(MIN_TASK_ROWS, task.split("\n").length + 1));

  const ready =
    Boolean(runner && agent && model && repoId) &&
    !baseError &&
    !slugError &&
    taskBytes <= MAX_GLOBAL_TASK_BYTES &&
    !submitting;

  const submit = async () => {
    if (submitting) return;
    setAttempted(true);
    if (!taskValue) {
      setFailure(null);
      taskRef.current?.focus();
      return;
    }
    if (!ready || !runner || !agent) return;
    setSubmitting(true);
    setFailure(null);
    const body: CreateGlobalRunRequest = {
      runnerId: runner.id,
      repoId,
      agent: agent.kind,
      model,
      ...(mode === "interactive" ? { mode: "interactive" as const } : {}),
      ...(baseValue ? { base: baseValue } : {}),
      task: taskValue,
      ...(sendSlug ? { slug: slugValue } : {}),
      openPR,
    };
    try {
      const run = await useGlobalAgents.getState().create(body);
      // The next one likely goes to the same place: only what was this
      // task's is cleared.
      setTask("");
      setSlugText("");
      setSlugEdited(false);
      setAttempted(false);
      onStarted?.(run);
    } catch (caught) {
      const mapped = startFailure(t, caught, { runner: runnerName, interactive: mode === "interactive" });
      setFailure(mapped);
      if (mapped.stepUp) setStepUpOpen(true);
      // The refusal names the limit; the warning line catches up with it.
      if (mapped.budget) void useBudget.getState().load();
    } finally {
      setSubmitting(false);
    }
  };

  const copyCommand = async () => {
    try {
      await navigator.clipboard?.writeText(REPO_ADD_COMMAND);
      setCopied(true);
    } catch {
      /* the command stays on screen to copy by hand */
    }
  };

  const invalid = (field: string, local = false) => (local || failure?.field === field ? true : undefined);
  const runnerLabel = (item: Runner) => {
    const block = blockOf(item);
    const name = runnerDisplayName(t, item.name);
    return block ? t(`globalAgents.runner.${block}`, { runner: name }) : name;
  };
  const loadingRunners = !runnersLoaded || (runnersLoading && runners.length === 0);
  const fieldId = (name: string) => `${id}-${name}`;

  const onRepoAdded = (repo: { id: string; name: string }) => {
    setRepoId(repo.id);
    setAddedNote(t("repoAdd.added", { name: repo.name }));
  };
  // The agent may run any command on this computer: said beside Start.
  const fullAccess = Boolean(localRunner && agent && runner?.id === localRunner.id && permissionOf(runner, agent.kind) === "full");

  let fields: ReactNode;
  if (loadingRunners) {
    fields = <p className="run-empty">{t("common.loading")}</p>;
  } else if (single && eligible.length === 0) {
    // Single-user with nothing that can start one: this computer is not
    // ready, in its own words, as in the dispatch dialog.
    fields = (
      <LocalNotReady starting={localState(mine) === "starting"} onOpenSettings={() => openSettings("runners")} />
    );
  } else if (mine.length === 0) {
    fields = (
      <div className="run-empty">
        <p>{t("globalAgents.runner.none")}</p>
        <button type="button" className="run-link-button" onClick={() => openSettings("runners", { add: true })}>
          {t("station.runner.add")}
        </button>
      </div>
    );
  } else {
    fields = (
      <>
        {!localRunner && (
          <label className="run-field">
            <span>{t("globalAgents.form.runner")}</span>
            <select
              value={runner?.id ?? ""}
              onChange={(event) => setRunnerId(event.target.value)}
              disabled={eligible.length === 0}
            >
              {mine.map((item) => (
                <option key={item.id} value={item.id} disabled={blockOf(item) !== ""}>
                  {runnerLabel(item)}
                </option>
              ))}
            </select>
          </label>
        )}
        {eligible.length === 0 ? (
          <p className="run-empty">{t("globalAgents.form.noRunnerAvailable")}</p>
        ) : (
          <>
            <div className="run-field">
              <span className="run-field-label-row">
                <label className="run-field-label" htmlFor={fieldId("repo")}>
                  {t("globalAgents.form.repo")}
                </label>
                {runner && repos.length > 0 && <AddRepoButton runner={runner} onAdded={onRepoAdded} />}
              </span>
              <select
                id={fieldId("repo")}
                value={repoId}
                onChange={(event) => setRepoId(event.target.value)}
                disabled={repos.length === 0}
                aria-invalid={invalid("repoId")}
                aria-describedby={fieldId("repo-help")}
              >
                {repos.length === 0 && <option value="">{t("runs.noReposOption")}</option>}
                {repos.map((repo) => (
                  <option key={repo.id} value={repo.id}>
                    {repo.path ? `${repo.name} — ${repo.path}` : repo.name}
                  </option>
                ))}
              </select>
              {repos.length === 0 && localRunner ? (
                <div className="run-empty run-empty-left global-agent-form-norepo" id={fieldId("repo-help")}>
                  <p>{t("runs.noReposLocal")}</p>
                  {runner && <AddRepoButton runner={runner} onAdded={onRepoAdded} />}
                  <details className="run-advanced">
                    <summary>{t("repoAdd.advancedCli")}</summary>
                    <div className="global-agent-form-command">
                      <code>{REPO_ADD_COMMAND}</code>
                      <button type="button" className="run-link-button" onClick={() => void copyCommand()}>
                        {copied ? t("globalAgents.form.copied") : t("globalAgents.form.copy")}
                      </button>
                    </div>
                  </details>
                </div>
              ) : repos.length === 0 ? (
                <div className="run-empty run-empty-left global-agent-form-norepo" id={fieldId("repo-help")}>
                  <p>{t("globalAgents.form.noRepos", { runner: runnerName })}</p>
                  <div className="global-agent-form-command">
                    <code>{REPO_ADD_COMMAND}</code>
                    <button type="button" className="run-link-button" onClick={() => void copyCommand()}>
                      {copied ? t("globalAgents.form.copied") : t("globalAgents.form.copy")}
                    </button>
                  </div>
                </div>
              ) : (
                <small className="run-hint" id={fieldId("repo-help")}>
                  {addedNote || t(localRunner ? "globalAgents.form.repoHelpLocal" : "globalAgents.form.repoHelp")}
                </small>
              )}
            </div>

            <div className="run-field-row">
              <label className="run-field">
                <span>{t("globalAgents.form.agent")}</span>
                <select
                  value={agent?.kind ?? ""}
                  onChange={(event) => setAgentKind(event.target.value)}
                  disabled={agents.length === 0}
                  aria-invalid={invalid("agent")}
                >
                  {agents.length === 0 && <option value="">{t("runs.noAgentsOption")}</option>}
                  {agents.map((item) => (
                    <option key={item.kind} value={item.kind}>
                      {item.version ? `${item.kind} · ${item.version}` : item.kind}
                    </option>
                  ))}
                </select>
              </label>
              <label className="run-field">
                <span>{t("globalAgents.form.model")}</span>
                <select
                  value={model}
                  onChange={(event) => setModel(event.target.value)}
                  disabled={models.length === 0}
                  aria-invalid={invalid("model")}
                >
                  {models.length === 0 && <option value="">{t("runs.noModelsOption")}</option>}
                  {models.map((item) => (
                    <option key={item.id} value={item.id}>
                      {runnerModelLabel(t, item) + (item.default ? t("runs.defaultSuffix") : "")}
                    </option>
                  ))}
                </select>
              </label>
            </div>

            <RunModePicker runner={runner} value={mode} onChange={setMode} />

            <div className="run-field-row global-agent-form-branch">
              <div className="run-field">
                <label className="run-field-label" htmlFor={fieldId("base")}>
                  {t("globalAgents.form.base")}
                </label>
                <input
                  id={fieldId("base")}
                  value={base}
                  maxLength={200}
                  placeholder={t("globalAgents.form.basePlaceholder")}
                  onChange={(event) => setBase(event.target.value)}
                  spellCheck={false}
                  autoCapitalize="off"
                  autoCorrect="off"
                  aria-invalid={invalid("base", Boolean(baseError))}
                  aria-describedby={baseError ? fieldId("base-error") : undefined}
                />
                {baseError && (
                  <small id={fieldId("base-error")} className="run-field-error">
                    {baseError}
                  </small>
                )}
              </div>
              <div className="run-field">
                <label className="run-field-label" htmlFor={fieldId("slug")}>
                  {t("globalAgents.form.slug")}
                </label>
                <input
                  id={fieldId("slug")}
                  value={slugEdited ? slugText : derivedSlug}
                  maxLength={80}
                  placeholder={derivedSlug}
                  onChange={(event) => {
                    setSlugEdited(true);
                    setSlugText(event.target.value);
                  }}
                  spellCheck={false}
                  autoCapitalize="off"
                  autoCorrect="off"
                  aria-invalid={invalid("slug", Boolean(slugError))}
                  aria-describedby={fieldId("slug-help")}
                />
                {slugError ? (
                  <small id={fieldId("slug-help")} className="run-field-error">
                    {slugError}
                  </small>
                ) : (
                  <small id={fieldId("slug-help")} className="run-hint global-agent-form-slug">
                    {t(localRunner ? "globalAgents.form.slugPreviewLocal" : "globalAgents.form.slugPreview", {
                      branch: `agent/${slug}-xxxxxxxx`,
                    })}
                  </small>
                )}
              </div>
            </div>

            <div className="run-field global-agent-form-task">
              <span className="global-agent-form-task-head">
                <label className="run-field-label" htmlFor={fieldId("task")}>
                  {t("globalAgents.form.task")}
                </label>
                <small className="run-count" id={fieldId("task-count")}>
                  {t("globalAgents.form.taskBytes", { used: (taskBytes / 1024).toFixed(1), max: MAX_TASK_KB })}
                </small>
              </span>
              <textarea
                ref={taskRef}
                id={fieldId("task")}
                value={task}
                rows={taskRows}
                placeholder={t("globalAgents.form.taskPlaceholder")}
                onChange={(event) => setTask(event.target.value)}
                onKeyDown={(event) => {
                  if (event.key === "Enter" && (event.ctrlKey || event.metaKey)) {
                    event.preventDefault();
                    void submit();
                  }
                }}
                aria-invalid={invalid("task", Boolean(taskError))}
                aria-describedby={`${fieldId("task-count")} ${fieldId("task-help")}`}
              />
              {taskError ? (
                <small id={fieldId("task-help")} className="run-field-error">
                  {taskError}
                </small>
              ) : (
                <small id={fieldId("task-help")} className="run-hint">
                  {t("globalAgents.form.shortcut")}
                </small>
              )}
            </div>

            <div className="global-agent-form-pr">
              <label className="route-toggle">
                <input type="checkbox" checked={openPR} onChange={(event) => setOpenPR(event.target.checked)} />
                <span>{t("globalAgents.form.openPR")}</span>
              </label>
              <small className="run-hint">
                {t(localRunner ? "globalAgents.form.openPRHelpLocal" : "globalAgents.form.openPRHelp")}
              </small>
            </div>

            <p className="run-hint global-agent-form-noboard">{t("globalAgents.form.noBoard")}</p>

            {runner && (
              <AutoAcceptNote
                runner={runner}
                repoId={repoId}
                agent={agent?.kind ?? ""}
                mode={mode === "interactive" ? "interactive" : ""}
                onVerify={() => setStepUpOpen(true)}
              />
            )}
          </>
        )}
      </>
    );
  }

  return (
    <>
      <form
        className="global-agent-form"
        aria-label={t("globalAgents.new")}
        onSubmit={(event) => {
          event.preventDefault();
          void submit();
        }}
      >
        {runnersError && (
          <div className="notify-message error" role="status">
            {runnersError}
          </div>
        )}
        {budgetWarning && (
          <p className="run-budget-warning" role="status">
            {budgetWarning}{" "}
            <button
              type="button"
              className="run-inline-link"
              onClick={() => window.dispatchEvent(new CustomEvent("nodevas-open-budget"))}
            >
              {t("budget.adjust")}
            </button>
          </p>
        )}
        {fields}
        {failure && (
          <div className="notify-message error" role="alert">
            {failure.text}
          </div>
        )}
        <div className="global-agent-form-actions">
          {fullAccess && agent && (
            <span className="run-full-access" role="note">
              {t("dispatch.fullAccessNote", { agent: agentName(agent.kind) })}
            </span>
          )}
          {onClose && (
            <button type="button" onClick={onClose}>
              {t("common.close")}
            </button>
          )}
          <button type="submit" className="ap-btn is-primary" disabled={!ready}>
            {submitting ? t("globalAgents.form.starting") : t("globalAgents.form.start")}
          </button>
        </div>
      </form>
      {/* Outside the form: a portal's submit still bubbles through React
          to its ancestors, and must not start the agent. */}
      {stepUpOpen && <StepUpDialog onClose={closeStepUp} />}
    </>
  );
}
