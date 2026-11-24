// Starts a global agent (agent workspace, phase 2): an agent on one of the
// viewer's own runners, in one or more of that runner's registered repositories,
// for no project and no node. Multiple repositories fan out as a background
// group (§10.2); one repository defaults to interactive when supported (§8.1).

import { useCallback, useEffect, useId, useMemo, useRef, useState, type ReactNode } from "react";

import {
  api,
  AuthError,
  BusyError,
  CAP_COORD,
  CAP_GLOBAL_RUNS,
  CAP_GLOBAL_RUNS_ON,
  ConflictError,
  GlobalRunError,
  globalTitle,
  MAX_CONTEST_ENTRIES,
  MAX_GLOBAL_TASK_BYTES,
  MAX_GROUP_REPOS,
  RequestError,
  slugFrom,
  validSlug,
  type BusyResponse,
  type CreateGlobalRunRequest,
  type OnBusy,
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
import { runnerDisplayName } from "../runners/runnerName";
import { useRunners } from "../runners/runnerStore";
import { agentCatalog, catalogDefaultModel } from "../runs/AgentModelFields";
import { AutoAcceptNote } from "../runs/AutoAcceptNote";
import { budgetExceededText } from "../runs/BudgetSettings";
import { useBudget } from "../runs/budgetStore";
import { BusyChoice } from "../runs/coord/BusyChoice";
import { ContestForm, type ContestDraft } from "../runs/coord/ContestForm";
import { useCoordStore } from "../runs/coord/coordStore";
import { AddRepoButton, defaultModel, installedAgents, ownRunners, validBranchName } from "../runs/DispatchDialog";
import {
  defaultDispatchMode,
  isInteractiveDisabled,
  resolveDispatchMode,
  RunModePicker,
  type DispatchMode,
} from "../runs/RunModePicker";
import { useRuns } from "../runs/runStore";
import { openSettings } from "../settings/openSettings";
import { useSimpleMode } from "../simple/simpleMode";
import { StepUpDialog } from "../stepup/StepUpDialog";
import { useStepUp } from "../stepup/stepUpStore";
import { usePreference } from "../store";
import { useGlobalAgents } from "./globalAgentsStore";
import { AgentCards } from "./pickers/AgentCards";
import { ModelCards } from "./pickers/ModelCards";
import { RunnerCards } from "./pickers/PictureCards";
import { RepoCards } from "./pickers/RepoCards";

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

function minutesSince(iso: string | undefined): number {
  if (!iso) return 1;
  const ms = Date.now() - Date.parse(iso);
  if (!Number.isFinite(ms) || ms <= 0) return 1;
  return Math.max(1, Math.round(ms / 60_000));
}

function buildInitialContestDraft(runner: Runner | undefined): ContestDraft {
  const runnerId = runner?.id ?? "";
  const catalog = agentCatalog(runner ? [runner] : []);
  const options: { runnerId: string; agent: string; model: string }[] = [];
  for (const item of catalog) {
    const def = catalogDefaultModel(item);
    if (def) options.push({ runnerId, agent: item.kind, model: def });
  }
  for (const item of catalog) {
    for (const model of item.models) {
      if (!options.some((o) => o.agent === item.kind && o.model === model.id)) {
        options.push({ runnerId, agent: item.kind, model: model.id });
      }
    }
  }
  if (options.length === 0) {
    return {
      entries: [
        { runnerId, agent: "claude", model: "default" },
        { runnerId, agent: "codex", model: "default" },
      ],
      reviewer: null,
    };
  }
  return {
    entries: [options[0], options[1] ?? { ...options[0] }],
    reviewer: null,
  };
}

function contestDraftValid(draft: ContestDraft | null, runner: Runner | undefined): boolean {
  if (!draft || !runner?.info?.capabilities?.includes(CAP_COORD)) return false;
  if (draft.entries.length < 2 || draft.entries.length > MAX_CONTEST_ENTRIES) return false;
  const seen = new Set<string>();
  for (const entry of draft.entries) {
    if (!entry.agent.trim() || !entry.model.trim()) return false;
    const key = `${entry.agent.trim().toLowerCase()}::${entry.model.trim().toLowerCase()}`;
    if (seen.has(key)) return false;
    seen.add(key);
  }
  if (draft.reviewer && (!draft.reviewer.agent.trim() || !draft.reviewer.model.trim())) {
    return false;
  }
  return true;
}

/** Normalizes a group 409 BUSY payload (which may store holders per repo) for BusyChoice. */
function normalizeBusy(busy: BusyResponse, isGroup: boolean): BusyResponse {
  if (!busy.repos || busy.repos.length === 0) return busy;
  const holders =
    busy.holders && busy.holders.length > 0
      ? busy.holders
      : busy.repos.flatMap((repo) => repo.holders ?? []);
  const others =
    busy.others > 0
      ? busy.others
      : busy.repos.reduce((sum, repo) => sum + (repo.others || 0), 0);
  const choices =
    busy.choices && busy.choices.length > 0
      ? busy.choices
      : isGroup
        ? ["both", "skip"]
        : [];
  return { ...busy, holders, others, choices };
}

/** A refused start in the reader's words: the field the hub refused, and
 * whether a step-up or the budget line should follow. */
export interface StartFailure {
  text: string;
  /** The request field the hub named (task, slug, base, mode, agent, model,
   * repoId, repoIds), "" for none. */
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
  const edition = useEdition();
  const runners = useRunners((s) => s.runners);
  const runnersLoaded = useRunners((s) => s.loaded);
  const runnersLoading = useRunners((s) => s.loading);
  const runnersError = useRunners((s) => s.error);
  const loads = useGlobalAgents((s) => s.runners);

  const single = edition === "single";
  const simple = useSimpleMode();
  const modePref = usePreference("runMode");
  const mine = useMemo(() => ownRunners(runners, viewer), [runners, viewer]);
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
  const [repoChoiceIds, setRepoIds] = useState<string[] | null>(null);
  const [modeChoice, setModeChoice] = useState<DispatchMode | null>(null);
  const [contestOn, setContestOn] = useState(false);
  const [contestDraft, setContestDraft] = useState<ContestDraft | null>(null);
  const [pendingBusy, setPendingBusy] = useState<BusyResponse | null>(null);
  const [busyChanged, setBusyChanged] = useState(false);
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

  useEffect(() => {
    if (!useRunners.getState().loaded) void useRunners.getState().refresh();
  }, []);

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

  const runner = eligible.find((item) => item.id === runnerId) ?? eligible[0];
  const runnerName = runner ? runnerDisplayName(t, runner.name) : "";
  const agents = installedAgents(runner);
  const agent = agents.find((item) => item.kind === agentKind) ?? agents[0];
  const models = agent?.models ?? [];
  const model = models.some((item) => item.id === modelChoice) ? modelChoice : defaultModel(agent);
  const repos = runner?.info?.repos ?? [];

  const selectedRepoIds = useMemo(() => {
    if (repos.length === 0) return [];
    if (repoChoiceIds === null) return [repos[0].id];
    if (repoChoiceIds.length === 0) return [];
    const valid = repoChoiceIds.filter((rid) => repos.some((repo) => repo.id === rid));
    return valid.length > 0 ? valid : [repos[0].id];
  }, [repos, repoChoiceIds]);

  const repoId = selectedRepoIds[0] ?? "";
  const activeContest = !simple && contestOn;
  const isGroup = !activeContest && selectedRepoIds.length > 1;
  const effectiveRepoIds = useMemo(
    () => (activeContest ? (repoId ? [repoId] : []) : selectedRepoIds),
    [activeContest, repoId, selectedRepoIds],
  );
  const effectiveRepoKey = effectiveRepoIds.join(",");

  // Preflight global busy whenever runner or picked folders change.
  useEffect(() => {
    if (!runner?.id || !effectiveRepoKey) return;
    void useCoordStore.getState().loadGlobalBusy(runner.id, effectiveRepoKey.split(","));
  }, [runner?.id, effectiveRepoKey]);

  const busyByRepo = useCoordStore((s) => (runner?.id ? s.globalBusy[runner.id] : undefined));
  const preflightBusyRepo = useMemo(() => {
    if (!busyByRepo) return undefined;
    return effectiveRepoIds
      .map((rid) => busyByRepo[rid])
      .find((entry) => entry && ((entry.holders?.length ?? 0) > 0 || entry.others > 0));
  }, [busyByRepo, effectiveRepoIds]);

  const activeDraft = useMemo(
    () => (activeContest ? (contestDraft ?? buildInitialContestDraft(runner)) : null),
    [activeContest, contestDraft, runner],
  );

  const modeCtx = useMemo(
    () => ({
      runner,
      agent,
      viewer,
      edition,
      group: isGroup,
      contest: activeContest,
    }),
    [runner, agent, viewer, edition, isGroup, activeContest],
  );
  const defaultModeResult = defaultDispatchMode(modeCtx);
  const mode = resolveDispatchMode(modeChoice, modePref, defaultModeResult, modeCtx);

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
  const derivedSlug = slugFrom(globalTitle(task));
  const slugValue = slugText.trim();
  const sendSlug = slugEdited && slugValue !== "";
  const slug = sendSlug ? slugValue : derivedSlug;
  const slugError = sendSlug && !validSlug(slugValue) ? t("globalAgents.form.slugInvalid") : "";
  const taskRows = Math.min(MAX_TASK_ROWS, Math.max(MIN_TASK_ROWS, task.split("\n").length + 1));

  const ready = activeContest
    ? Boolean(runner && repoId && contestDraftValid(activeDraft, runner)) &&
      !baseError &&
      !slugError &&
      taskBytes <= MAX_GLOBAL_TASK_BYTES &&
      !submitting
    : Boolean(runner && agent && model && selectedRepoIds.length > 0 && selectedRepoIds.length <= MAX_GROUP_REPOS) &&
      !baseError &&
      !slugError &&
      taskBytes <= MAX_GLOBAL_TASK_BYTES &&
      !submitting;

  const clearAfterStart = () => {
    setTask("");
    setSlugText("");
    setSlugEdited(false);
    setAttempted(false);
    setPendingBusy(null);
    setBusyChanged(false);
  };

  const submit = async (onBusy?: OnBusy, holderIds?: string[]) => {
    if (submitting) return;
    setAttempted(true);
    if (!taskValue) {
      setFailure(null);
      taskRef.current?.focus();
      return;
    }
    if (!ready || !runner) return;
    if (!activeContest && !agent) return;
    setSubmitting(true);
    setFailure(null);
    try {
      if (activeContest && activeDraft) {
        const res = await api.createGlobalContest({
          runnerId: runner.id,
          repoId,
          ...(baseValue ? { base: baseValue } : {}),
          task: taskValue,
          ...(sendSlug ? { slug: slugValue } : {}),
          entries: activeDraft.entries.map((entry) => ({ agent: entry.agent, model: entry.model })),
          ...(activeDraft.reviewer
            ? { reviewer: { agent: activeDraft.reviewer.agent, model: activeDraft.reviewer.model } }
            : {}),
          ...(onBusy ? { onBusy } : {}),
          ...(holderIds && holderIds.length > 0 ? { holderIds } : {}),
        });
        const runs = res.runs ?? [];
        useRuns.getState().putRuns(runs);
        if (runs[0]) useGlobalAgents.getState().setFocus(runs[0].id);
        clearAfterStart();
        if (runs[0]) onStarted?.(runs[0]);
        return;
      }

      if (isGroup) {
        const groupOnBusy = onBusy === "both" || onBusy === "skip" ? onBusy : undefined;
        const res = await api.createGlobalGroup({
          runnerId: runner.id,
          repoIds: selectedRepoIds,
          agent: agent!.kind,
          model,
          ...(baseValue ? { base: baseValue } : {}),
          task: taskValue,
          ...(sendSlug ? { slug: slugValue } : {}),
          openPR: simple ? false : openPR,
          ...(simple ? { simple: true } : {}),
          ...(groupOnBusy ? { onBusy: groupOnBusy } : {}),
          ...(groupOnBusy && holderIds && holderIds.length > 0 ? { holderIds } : {}),
        });
        const runs = res.runs ?? [];
        useRuns.getState().putRuns(runs);
        if (runs[0]) useGlobalAgents.getState().setFocus(runs[0].id);
        clearAfterStart();
        if (runs[0]) onStarted?.(runs[0]);
        return;
      }

      const body: CreateGlobalRunRequest = {
        runnerId: runner.id,
        repoId,
        agent: agent!.kind,
        model,
        ...(mode === "interactive" ? { mode: "interactive" as const } : {}),
        ...(baseValue ? { base: baseValue } : {}),
        task: taskValue,
        ...(sendSlug ? { slug: slugValue } : {}),
        openPR: simple ? false : openPR,
        ...(simple ? { simple: true } : {}),
        ...(onBusy ? { onBusy } : {}),
        ...(holderIds && holderIds.length > 0 ? { holderIds } : {}),
      };
      const run = await useGlobalAgents.getState().create(body);
      clearAfterStart();
      onStarted?.(run);
    } catch (caught) {
      if (caught instanceof BusyError) {
        setPendingBusy(normalizeBusy(caught.busy, isGroup));
        setBusyChanged(Boolean(onBusy));
        return;
      }
      if (caught instanceof GlobalRunError && caught.status === 429 && caught.room > 0 && isGroup) {
        setRepoIds(selectedRepoIds.slice(0, caught.room));
        setFailure({
          text: t("globalAgents.group.room", { count: caught.room }),
          field: "repoIds",
          stepUp: false,
          budget: true,
        });
        void useBudget.getState().load();
        return;
      }
      const mapped = startFailure(t, caught, { runner: runnerName, interactive: mode === "interactive" });
      setFailure(mapped);
      if (mapped.stepUp) setStepUpOpen(true);
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
  const runnerDisabledReason = (item: Runner) => {
    const block = blockOf(item);
    return block ? t(`globalAgents.runner.${block}`, { runner: runnerDisplayName(t, item.name) }) : "";
  };
  const loadingRunners = !runnersLoaded || (runnersLoading && runners.length === 0);
  const fieldId = (name: string) => `${id}-${name}`;

  const onRepoAdded = (repo: { id: string; name: string }) => {
    setRepoIds([repo.id]);
    setAddedNote(t("repoAdd.added", { name: repo.name }));
  };

  const fullAccess = Boolean(
    !activeContest &&
      localRunner &&
      agent &&
      runner?.id === localRunner.id &&
      permissionOf(runner, agent.kind) === "full",
  );

  const emptyRepoNode = localRunner ? (
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
  ) : (
    <div className="run-empty run-empty-left global-agent-form-norepo" id={fieldId("repo-help")}>
      <p>{t("globalAgents.form.noRepos", { runner: runnerName })}</p>
      <div className="global-agent-form-command">
        <code>{REPO_ADD_COMMAND}</code>
        <button type="button" className="run-link-button" onClick={() => void copyCommand()}>
          {copied ? t("globalAgents.form.copied") : t("globalAgents.form.copy")}
        </button>
      </div>
    </div>
  );

  let fields: ReactNode;
  if (loadingRunners) {
    fields = <p className="run-empty">{t("common.loading")}</p>;
  } else if (single && eligible.length === 0) {
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
          <RunnerCards
            runners={mine}
            value={runner?.id ?? ""}
            onChange={setRunnerId}
            disabled={eligible.length === 0}
            disabledReason={runnerDisabledReason}
          />
        )}
        {eligible.length === 0 ? (
          <p className="run-empty">{t("globalAgents.form.noRunnerAvailable")}</p>
        ) : (
          <>
            {preflightBusyRepo && !pendingBusy && (
              <p className="run-stage-warning" role="status" data-testid="global-busy-preflight">
                {simple
                  ? t("busy.simple.title", { minutes: minutesSince(preflightBusyRepo.holders?.[0]?.since) })
                  : t("busy.title")}
              </p>
            )}

            <div className="run-field">
              {activeContest ? (
                <RepoCards
                  mode="one"
                  repos={repos}
                  value={repoId}
                  onChange={(picked) => setRepoIds([picked])}
                  simple={simple}
                  invalid={Boolean(invalid("repoId") || invalid("repoIds"))}
                  describedBy={fieldId("repo-help")}
                  headerAction={runner && repos.length > 0 ? <AddRepoButton runner={runner} onAdded={onRepoAdded} /> : undefined}
                  empty={emptyRepoNode}
                />
              ) : (
                <RepoCards
                  mode="many"
                  repos={repos}
                  values={selectedRepoIds}
                  onChange={setRepoIds}
                  simple={simple}
                  invalid={Boolean(invalid("repoId") || invalid("repoIds"))}
                  describedBy={fieldId("repo-help")}
                  headerAction={runner && repos.length > 0 ? <AddRepoButton runner={runner} onAdded={onRepoAdded} /> : undefined}
                  empty={emptyRepoNode}
                />
              )}
              {repos.length > 0 && (
                <small className="run-hint" id={fieldId("repo-help")}>
                  {addedNote || t(localRunner ? "globalAgents.form.repoHelpLocal" : "globalAgents.form.repoHelp")}
                </small>
              )}
            </div>

            {!simple && (
              <label className="route-toggle">
                <input
                  type="checkbox"
                  checked={contestOn}
                  onChange={(event) => {
                    const on = event.target.checked;
                    setContestOn(on);
                    if (on && !contestDraft) {
                      setContestDraft(buildInitialContestDraft(runner));
                    }
                  }}
                />
                <span>{t("contest.toggle")}</span>
              </label>
            )}

            {activeContest ? (
              <ContestForm
                runners={runner ? [runner] : []}
                value={activeDraft ?? undefined}
                disabled={submitting}
                onChange={setContestDraft}
              />
            ) : (
              <>
                <AgentCards
                  agents={runner?.info?.agents ?? []}
                  value={agent?.kind ?? ""}
                  onChange={setAgentKind}
                  simple={simple}
                  invalid={Boolean(invalid("agent"))}
                />

                {models.length > 0 && (
                  <ModelCards
                    agent={agent?.kind ?? ""}
                    models={models}
                    value={model}
                    onChange={setModel}
                    simple={simple}
                    invalid={Boolean(invalid("model"))}
                  />
                )}

                <RunModePicker
                  runner={runner}
                  agent={agent}
                  model={model}
                  value={mode}
                  onChange={setModeChoice}
                  fallbackReason={defaultModeResult.reason}
                  interactiveDisabled={isInteractiveDisabled(modeCtx)}
                  simple={simple}
                  invalid={Boolean(invalid("mode"))}
                />
              </>
            )}

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

            {!simple && !activeContest && (
              <div className="global-agent-form-pr">
                <label className="route-toggle">
                  <input type="checkbox" checked={openPR} onChange={(event) => setOpenPR(event.target.checked)} />
                  <span>{t("globalAgents.form.openPR")}</span>
                </label>
                <small className="run-hint">
                  {t(localRunner ? "globalAgents.form.openPRHelpLocal" : "globalAgents.form.openPRHelp")}
                </small>
              </div>
            )}

            <p className="run-hint global-agent-form-noboard">{t("globalAgents.form.noBoard")}</p>

            {runner && !activeContest && (
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
        {pendingBusy && (
          <>
            {busyChanged && (
              <p className="notify-message error" role="status">
                {t("busy.changed")}
              </p>
            )}
            <BusyChoice
              busy={pendingBusy}
              simple={simple}
              maxConcurrent={runner?.info?.maxConcurrent}
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
