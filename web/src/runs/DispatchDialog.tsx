import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  api,
  AuthError,
  BusyError,
  CAP_COORD,
  CAP_REPO_ADMIN,
  ConflictError,
  MAX_CONTEST_ENTRIES,
  MAX_PIPELINE_ROUNDS,
  MAX_RUN_PROMPT_BYTES,
  MAX_RUN_PROMPT_CHARS,
  RequestError,
  ROUTE_AUTO,
  type Actor,
  type AgentModel,
  type BusyResponse,
  type OnBusy,
  type RouteSuggestion,
  type Run,
  type Runner,
  type RunnerAgent,
  type RunnerRepo,
} from "../api";
import { useViewer } from "../components/SignIn";
import { IconClose, IconSend } from "../icons";
import { useI18n } from "../i18n";
import { runnerDisplayName } from "../runners/runnerName";
import { useApp, usePreference } from "../store";
import { useRunners } from "../runners/runnerStore";
import { builtInRunner, localState, useEdition } from "../runners/edition";
import { AddRepoDialog, repoAddCommand } from "../local/AddRepoDialog";
import { LocalNotReady } from "../local/LocalNotReady";
import { permissionOf } from "../local/ThisComputerPanel";
import { openSettings } from "../settings/openSettings";
import { agentName } from "../game/labels";
import {
  agentCatalog,
  agentModelProblem,
  agentModelText,
  catalogDefaultModel,
  defaultPipeline,
  FallbackChainEditor,
  PipelineFields,
  trimAgentModel,
} from "./AgentModelFields";
import { budgetExceededText } from "./BudgetSettings";
import { matchTakesValue, routeLabel, useRouteKinds } from "./RoutingSettings";
import { nodeTypeErrorMessage } from "../nodeTypeErrors";
import { fetchRouteSuggestion, useRouting } from "./routingStore";
import { useBudget } from "./budgetStore";
import { useRuns } from "./runStore";
import { AutoAcceptNote } from "./AutoAcceptNote";
import { StepUpDialog } from "../stepup/StepUpDialog";
import { useStepUp } from "../stepup/stepUpStore";
import {
  defaultDispatchMode,
  isInteractiveDisabled,
  resolveDispatchMode,
  RunModePicker,
  type DispatchMode,
} from "./RunModePicker";
import { BusyChoice } from "./coord/BusyChoice";
import { ContestForm, type ContestDraft } from "./coord/ContestForm";
import { nodeBusyKey, useCoordStore } from "./coord/coordStore";
import { AgentCards } from "../station/pickers/AgentCards";
import { ModelCards } from "../station/pickers/ModelCards";
import { RunnerCards } from "../station/pickers/PictureCards";
import { RepoCards } from "../station/pickers/RepoCards";
import { openNodeDetail } from "../components/drawer/openNodeDetail";
import { requiresAcceptance, workflowForNode } from "../domain/graph/stages";
import { FOCUS_ACCEPTANCE_EVENT } from "../roles/AcceptanceCriteria";
import { StageChain, stagedNeedsTests } from "../roles/StageChain";
import { SIMPLE_OVERRIDE_REASON, useSimpleMode } from "../simple/simpleMode";

/** Longest reason a person gives for skipping enabled stages. */
export const MAX_OVERRIDE_CHARS = 200;

const HEADLESS = "headless";

/** Why a runner cannot take a run now, or "" when it can. */
export function runnerUnavailableReason(runner: Runner): "" | "offline" | "headless" {
  if (!runner.online) return "offline";
  if (!runner.info?.capabilities?.includes(HEADLESS)) return "headless";
  return "";
}

/** Runners a run may be dispatched to: the viewer's own. An admin's list also
 * holds other people's runners, which the hub refuses to dispatch to. With no
 * viewer (a loopback server without accounts) every runner is one's own. */
export function ownRunners(runners: Runner[], viewer: Actor | null): Runner[] {
  return viewer ? runners.filter((runner) => runner.ownerId === viewer.id) : runners;
}

/** The hub's rule for a base branch: letters, digits and ._/- only, no
 * leading "-" (it would read as a git flag) and no "..". */
export function validBranchName(name: string): boolean {
  return /^[A-Za-z0-9._/-]+$/.test(name) && !name.startsWith("-") && !name.includes("..");
}

function utf8Length(text: string): number {
  return new TextEncoder().encode(text).length;
}

export function installedAgents(runner: Runner | undefined): RunnerAgent[] {
  return (runner?.info?.agents ?? []).filter((agent) => agent.installed && !agent.signedOut);
}

export function defaultModel(agent: RunnerAgent | undefined): string {
  const models = agent?.models ?? [];
  return (models.find((model) => model.default) ?? models[0])?.id ?? "";
}

/** Whether repos are added to `runner` from the app: this computer's
 * built-in runner (single-user edition). Any other runner adds them on its
 * own PC. */
export function addsReposInApp(runner: Runner | undefined): boolean {
  return Boolean(runner?.builtIn && runner.info?.capabilities?.includes(CAP_REPO_ADMIN));
}

function minutesSince(iso: string | undefined): number {
  if (!iso) return 1;
  const ms = Date.now() - Date.parse(iso);
  if (!Number.isFinite(ms) || ms <= 0) return 1;
  return Math.max(1, Math.round(ms / 60_000));
}

function buildInitialContestDraft(runners: readonly Runner[]): ContestDraft {
  const defaultRunnerId = runners[0]?.id ?? "";
  const catalog = agentCatalog([...runners]);
  const options: { runnerId: string; agent: string; model: string }[] = [];
  for (const item of catalog) {
    const def = catalogDefaultModel(item);
    if (def) options.push({ runnerId: defaultRunnerId, agent: item.kind, model: def });
  }
  for (const item of catalog) {
    for (const model of item.models) {
      if (!options.some((o) => o.agent === item.kind && o.model === model.id)) {
        options.push({ runnerId: defaultRunnerId, agent: item.kind, model: model.id });
      }
    }
  }
  if (options.length === 0) {
    return {
      entries: [
        { runnerId: defaultRunnerId, agent: "claude", model: "default" },
        { runnerId: defaultRunnerId, agent: "codex", model: "default" },
      ],
      reviewer: null,
    };
  }
  return {
    entries: [options[0], options[1] ?? { ...options[0] }],
    reviewer: null,
  };
}

function contestDraftValid(draft: ContestDraft | null, runners: readonly Runner[]): boolean {
  if (!draft) return false;
  if (draft.entries.length < 2 || draft.entries.length > MAX_CONTEST_ENTRIES) return false;
  const seen = new Set<string>();
  const defaultRunnerId = runners[0]?.id ?? "";
  for (const entry of draft.entries) {
    if (!entry.agent.trim() || !entry.model.trim()) return false;
    const key = `${entry.agent.trim().toLowerCase()}::${entry.model.trim().toLowerCase()}`;
    if (seen.has(key)) return false;
    seen.add(key);
    const r = runners.find((item) => item.id === (entry.runnerId || defaultRunnerId)) ?? runners[0];
    if (!r?.info?.capabilities?.includes(CAP_COORD)) return false;
  }
  if (draft.reviewer) {
    if (!draft.reviewer.agent.trim() || !draft.reviewer.model.trim()) return false;
    const r = runners.find((item) => item.id === (draft.reviewer?.runnerId || defaultRunnerId)) ?? runners[0];
    if (!r?.info?.capabilities?.includes(CAP_COORD)) return false;
  }
  return true;
}

/**
 * "+ Add repo" beside a repo choice: the folder dialog for this computer's
 * built-in runner, or, for a runner on another PC, where to add it there
 * (with the command line under Advanced).
 */
export function AddRepoButton({
  runner,
  onAdded,
}: {
  runner: Runner;
  onAdded: (repo: RunnerRepo) => void;
}) {
  const { t } = useI18n();
  const [open, setOpen] = useState(false);
  const inApp = addsReposInApp(runner);
  return (
    <>
      <button
        type="button"
        className="run-link-button run-add-repo"
        aria-expanded={inApp ? undefined : open}
        onClick={() => setOpen((value) => !value)}
      >
        {t("repoAdd.button")}
      </button>
      {open && inApp && (
        <AddRepoDialog
          runnerId={runner.id}
          onClose={() => setOpen(false)}
          onAdded={(repo) => {
            setOpen(false);
            onAdded(repo);
          }}
        />
      )}
      {open && !inApp && (
        <div className="run-empty run-empty-left run-add-repo-remote" role="note">
          <p>{t("repoAdd.onRunner", { machine: runnerDisplayName(t, runner.name) })}</p>
          <details className="run-advanced">
            <summary>{t("repoAdd.advancedCli")}</summary>
            <code>{repoAddCommand("", undefined)}</code>
          </details>
        </div>
      )}
    </>
  );
}

/**
 * Dispatches a node to an agent on one of the viewer's runners: runner, then
 * agent, then model, then repository. On a team hub nothing starts here: the
 * runner shows the offer and someone there accepts it. In the single-user
 * edition there is no runner to pick: this computer's built-in runner starts
 * it right away (unless its owner asked to be asked first).
 */
export function DispatchDialog({
  nodeId,
  onClose,
  onCreated,
}: {
  nodeId: string;
  onClose: () => void;
  onCreated: (run: Run) => void;
}) {
  const { t } = useI18n();
  const { kindLabel } = useRouteKinds();
  const viewer = useViewer();
  const edition = useEdition();
  const nodeTitle = useApp(
    (s) => s.graph?.nodes?.find((node) => node.id === nodeId)?.title || nodeId,
  );
  // Agent roles: the stages this node is worked under (null: legacy, the
  // dialog as before) and whether it has the criteria they judge it by.
  const workflow = useApp((s) => workflowForNode(s.graph, nodeId));
  const hasCriteria = useApp(
    (s) => (s.graph?.nodes?.find((node) => node.id === nodeId)?.acceptance?.items?.length ?? 0) > 0,
  );
  const [overrideOn, setOverrideOn] = useState(false);
  const [overrideReason, setOverrideReason] = useState("");
  const runners = useRunners((s) => s.runners);
  const runnersLoaded = useRunners((s) => s.loaded);
  const runnersLoading = useRunners((s) => s.loading);
  const runnersError = useRunners((s) => s.error);
  const refreshRunners = useRunners((s) => s.refresh);
  const createRun = useRuns((s) => s.createRun);

  const single = edition === "single";
  const simple = useSimpleMode();
  const modePref = usePreference("runMode");
  const mine = useMemo(() => ownRunners(runners, viewer), [runners, viewer]);
  const localRunner = single ? builtInRunner(mine) : undefined;
  const choices = useMemo(() => (localRunner ? [localRunner] : mine), [localRunner, mine]);
  const eligible = useMemo(
    () => choices.filter((runner) => runnerUnavailableReason(runner) === ""),
    [choices],
  );
  const localDown = single && eligible.length === 0;
  const [addedNote, setAddedNote] = useState("");

  const [runnerId, setRunnerId] = useState("");
  const [agentKind, setAgentKind] = useState("");
  const [modelChoice, setModel] = useState("");
  const [repoChoice, setRepoId] = useState("");
  const [base, setBase] = useState("");
  const [prompt, setPrompt] = useState("");
  const [modeChoice, setModeChoice] = useState<DispatchMode | null>(null);
  const [contestOn, setContestOn] = useState(false);
  const [contestDraft, setContestDraft] = useState<ContestDraft | null>(null);
  const [pendingBusy, setPendingBusy] = useState<BusyResponse | null>(null);
  const [busyChanged, setBusyChanged] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  // W5: the fallback chain and the implement → review pipeline.
  const [fallbacks, setFallbacks] = useState<AgentModel[]>([]);
  const [pipelineOn, setPipelineOn] = useState(false);
  const [review, setReview] = useState<AgentModel>({ agent: "", model: "" });
  const [rounds, setRounds] = useState(1);
  // What the routing rules picked for this node; null when none matched.
  const [suggestion, setSuggestion] = useState<RouteSuggestion | null>(null);
  // What the user changed before the suggestion arrived keeps their choice.
  const touched = useRef({ runner: false, agent: false, fallbacks: false, pipeline: false });

  useEffect(() => {
    let cancelled = false;
    void fetchRouteSuggestion(nodeId).then((found) => {
      if (cancelled || !found?.ruleId) return;
      setSuggestion(found);
      const mark = touched.current;
      if (!mark.runner && found.runnerId) setRunnerId(found.runnerId);
      const implement = found.pipeline?.implement ?? found.target;
      if (!mark.agent && implement?.agent) {
        setAgentKind(implement.agent);
        setModel(implement.model ?? "");
      }
      if (!mark.fallbacks) {
        setFallbacks((found.fallbacks ?? []).map((item) => ({ agent: item.agent, model: item.model })));
      }
      if (!mark.pipeline && found.pipeline) {
        setPipelineOn(true);
        setReview({ ...found.pipeline.review });
        const max = Math.round(found.pipeline.maxRounds);
        setRounds(Math.min(MAX_PIPELINE_ROUNDS, Math.max(1, Number.isFinite(max) ? max : 1)));
      }
    });
    return () => {
      cancelled = true;
    };
  }, [nodeId]);

  const routingRules = useRouting((s) => s.rules);
  const needsRuleValue =
    suggestion !== null && suggestion.value === undefined && matchTakesValue(suggestion.match);
  useEffect(() => {
    if (needsRuleValue) void useRouting.getState().load();
  }, [needsRuleValue]);

  useEffect(() => {
    void refreshRunners();
  }, [refreshRunners]);

  const [stepUpOpen, setStepUpOpen] = useState(false);
  const closeStepUp = useCallback(() => setStepUpOpen(false), []);
  useEffect(() => {
    void useStepUp.getState().load();
  }, []);

  const budgetStatus = useBudget((s) => s.status);
  const loadBudget = useBudget((s) => s.load);
  useEffect(() => {
    void loadBudget();
  }, [loadBudget]);
  const budgetWarning = budgetExceededText(t, budgetStatus);

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  const runner = eligible.find((item) => item.id === runnerId) ?? eligible[0];
  const agents = installedAgents(runner);
  const agent = agents.find((item) => item.kind === agentKind) ?? agents[0];
  const models = agent?.models ?? [];
  const model = models.some((item) => item.id === modelChoice)
    ? modelChoice
    : defaultModel(agent);
  const catalog = useMemo(() => agentCatalog(runner ? [runner] : []), [runner]);
  const repos = runner?.info?.repos ?? [];
  const repoId = repos.some((repo) => repo.id === repoChoice) ? repoChoice : (repos[0]?.id ?? "");

  // Preflight node busy status whenever the target runner or repo changes.
  useEffect(() => {
    if (!runner?.id || !repoId) return;
    void useCoordStore.getState().loadNodeBusy({ runnerId: runner.id, repoId, nodeId });
  }, [runner?.id, repoId, nodeId]);

  const busyKey = runner?.id && repoId ? nodeBusyKey({ runnerId: runner.id, repoId, nodeId }) : "";
  const preflightBusy = useCoordStore((s) => (busyKey ? s.nodeBusy[busyKey] : undefined));
  const hasPreflightBusy = Boolean(
    preflightBusy && ((preflightBusy.holders?.length ?? 0) > 0 || preflightBusy.others > 0),
  );

  // The hub's own checks, so a bad value is caught while typing rather than
  // as a 400 after the click.
  const baseValue = base.trim();
  const baseError = baseValue && !validBranchName(baseValue) ? t("runs.baseInvalid") : "";
  const promptValue = prompt.trim();
  const promptBytes = utf8Length(promptValue);
  const promptError =
    promptBytes > MAX_RUN_PROMPT_BYTES
      ? t("runs.promptTooLong", { max: Math.floor(MAX_RUN_PROMPT_BYTES / 1024) })
      : "";

  const fallbackInvalid = fallbacks.some((item) => agentModelProblem(item) !== "");
  const repo = repos.find((item) => item.id === repoId);
  const blocked =
    workflow !== null &&
    !overrideOn &&
    ((requiresAcceptance(workflow) && !hasCriteria) ||
      (stagedNeedsTests(workflow) && Boolean(repo) && !repo?.verify));
  const simpleSkip = simple && blocked;
  const staged = workflow !== null && !overrideOn && !simpleSkip;
  const stagedPipeline = staged && Boolean(workflow?.stages.tester || workflow?.stages.reviewer);
  const reviewOn = staged ? Boolean(workflow?.stages.reviewer) : workflow === null && pipelineOn;
  const shownReview =
    reviewOn && agent && agentModelProblem(review) !== ""
      ? defaultPipeline({ agent: agent.kind, model }, catalog).review
      : review;
  const reviewInvalid = reviewOn && agentModelProblem(shownReview) !== "";
  const missingCriteria = staged && requiresAcceptance(workflow) && !hasCriteria;
  const needsTests = staged && stagedNeedsTests(workflow);
  const missingTestCommand = needsTests && Boolean(repo) && !repo?.verify;
  const runnerCaps = runner?.info?.capabilities ?? [];
  const oldRunner =
    stagedPipeline && Boolean(runner) && !(runnerCaps.includes("roles") && runnerCaps.includes("verify"));
  const overrideValue = overrideReason.trim();
  const overrideInvalid = overrideOn && (!overrideValue || overrideValue.length > MAX_OVERRIDE_CHARS);

  const activeContest = !simple && contestOn;
  const contestShapeError = activeContest && (staged || pipelineOn || fallbacks.length > 0);
  const activeDraft = useMemo(
    () => (activeContest ? (contestDraft ?? buildInitialContestDraft(eligible)) : null),
    [activeContest, contestDraft, eligible],
  );

  const modeCtx = useMemo(
    () => ({
      runner,
      agent,
      viewer,
      edition,
      staged,
      pipeline: stagedPipeline || (workflow === null && pipelineOn),
      fallbacks: fallbacks.length > 0,
      contest: activeContest,
    }),
    [runner, agent, viewer, edition, staged, stagedPipeline, workflow, pipelineOn, fallbacks.length, activeContest],
  );
  const defaultModeResult = defaultDispatchMode(modeCtx);
  const mode = resolveDispatchMode(modeChoice, modePref, defaultModeResult, modeCtx);

  const ready = activeContest
    ? Boolean(runner && repoId) &&
      !baseError &&
      !promptError &&
      !missingCriteria &&
      !missingTestCommand &&
      !oldRunner &&
      !overrideInvalid &&
      !contestShapeError &&
      contestDraftValid(activeDraft, eligible) &&
      !submitting
    : Boolean(runner && agent && model && repoId) &&
      !baseError &&
      !promptError &&
      !fallbackInvalid &&
      !reviewInvalid &&
      !missingCriteria &&
      !missingTestCommand &&
      !oldRunner &&
      !overrideInvalid &&
      !submitting;

  // "From rule: tag = review", and what of it this runner could not honour.
  const ruleValue =
    suggestion?.value ??
    routingRules?.find((rule) => rule.id === suggestion?.ruleId)?.value ??
    "";
  const autoPick = suggestion?.match === ROUTE_AUTO;
  const ruleNote = suggestion
    ? autoPick
      ? t("runs.route.auto")
      : t("dispatch.fromRule", { rule: routeLabel(t, suggestion.match, ruleValue, kindLabel) })
    : "";
  const routePick =
    autoPick &&
    suggestion?.ruleId.startsWith(`${ROUTE_AUTO}:`) &&
    agent?.kind === suggestion.target.agent &&
    model === (suggestion.target.model ?? "") &&
    (!suggestion.runnerId || runner?.id === suggestion.runnerId)
      ? suggestion.ruleId.slice(ROUTE_AUTO.length + 1)
      : "";
  const wanted = suggestion ? (suggestion.pipeline?.implement ?? suggestion.target) : undefined;
  const ruleMisses: string[] = [];
  if (suggestion && runner) {
    if (suggestion.runnerId && !touched.current.runner && runner.id !== suggestion.runnerId) {
      ruleMisses.push(t("dispatch.ruleRunnerMissing", { runner: runnerDisplayName(t, runner.name) }));
    }
    if (
      wanted?.agent &&
      !touched.current.agent &&
      (agent?.kind !== wanted.agent || (wanted.model && model !== wanted.model))
    ) {
      ruleMisses.push(
        t("dispatch.ruleModelMissing", {
          wanted: agentModelText(wanted),
          used: agent ? agentModelText({ agent: agent.kind, model }) : "—",
        }),
      );
    }
  }

  const submit = async (onBusy?: OnBusy, holderIds?: string[]) => {
    if (!ready || !runner) return;
    if (!activeContest && !agent) return;
    setSubmitting(true);
    setError(null);
    try {
      if (activeContest && activeDraft) {
        const defaultRunnerId = runner.id;
        const res = await api.createContest({
          nodeId,
          repoId,
          ...(baseValue ? { base: baseValue } : {}),
          ...(promptValue ? { prompt: promptValue } : {}),
          entries: activeDraft.entries.map((entry) => ({
            runnerId: entry.runnerId || defaultRunnerId,
            agent: entry.agent,
            model: entry.model,
          })),
          ...(activeDraft.reviewer
            ? {
                reviewer: {
                  runnerId: activeDraft.reviewer.runnerId || defaultRunnerId,
                  agent: activeDraft.reviewer.agent,
                  model: activeDraft.reviewer.model,
                },
              }
            : {}),
          ...(onBusy ? { onBusy } : {}),
          ...(holderIds && holderIds.length > 0 ? { holderIds } : {}),
        });
        const runs = res.runs ?? [];
        useRuns.getState().putRuns(runs);
        setPendingBusy(null);
        setBusyChanged(false);
        if (runs[0]) onCreated(runs[0]);
        else onClose();
        return;
      }

      const run = await createRun({
        nodeId,
        runnerId: runner.id,
        agent: agent!.kind,
        model,
        repoId,
        ...(baseValue ? { base: baseValue } : {}),
        ...(promptValue ? { prompt: promptValue } : {}),
        ...(fallbacks.length > 0 ? { fallbacks: fallbacks.map(trimAgentModel) } : {}),
        ...(stagedPipeline
          ? {
              pipeline: {
                ...(workflow?.stages.tester ? { tester: { agent: agent!.kind, model } } : {}),
                implement: { agent: agent!.kind, model },
                review: workflow?.stages.reviewer ? trimAgentModel(shownReview) : { agent: agent!.kind, model },
                maxRounds: rounds,
              },
            }
          : workflow === null && pipelineOn
            ? {
                pipeline: {
                  implement: { agent: agent!.kind, model },
                  review: trimAgentModel(review),
                  maxRounds: rounds,
                },
              }
            : {}),
        ...(overrideOn ? { override: overrideValue } : simpleSkip ? { override: SIMPLE_OVERRIDE_REASON } : {}),
        ...(mode === "interactive" ? { mode } : {}),
        ...(routePick ? { routePick } : {}),
        ...(simple ? { simple: true } : {}),
        ...(onBusy ? { onBusy } : {}),
        ...(holderIds && holderIds.length > 0 ? { holderIds } : {}),
      });
      setPendingBusy(null);
      setBusyChanged(false);
      onCreated(run);
    } catch (caught) {
      if (caught instanceof BusyError) {
        setPendingBusy(caught.busy);
        setBusyChanged(Boolean(onBusy));
        return;
      }
      const text = caught instanceof Error ? caught.message : String(caught);
      const overBudget = caught instanceof RequestError && caught.status === 429;
      const typeRefusal = nodeTypeErrorMessage(caught, nodeId);
      setError(
        typeRefusal
          ? typeRefusal
          : caught instanceof AuthError && caught.status === 403
            ? t("runs.permissionDenied", { message: text })
            : caught instanceof ConflictError
              ? caught.serverMessage ||
                t(mode === "interactive" ? "terminal.runnerCannotInteract" : "runs.runnerCannotRun")
              : overBudget
                ? t("runs.overBudget", { message: text })
                : text,
      );
      if (overBudget) void useBudget.getState().load();
    } finally {
      setSubmitting(false);
    }
  };

  const writeCriteria = () => {
    onClose();
    openNodeDetail(nodeId, "content");
    window.setTimeout(
      () => window.dispatchEvent(new CustomEvent(FOCUS_ACCEPTANCE_EVENT, { detail: nodeId })),
      50,
    );
  };

  const openRunners = () => {
    onClose();
    window.dispatchEvent(new CustomEvent("nodevas-open-runners"));
  };

  const openThisComputer = () => {
    onClose();
    openSettings("runners");
  };

  const runnerCardName = (item: Runner) => {
    const host = item.info?.hostname && item.info.hostname !== item.name ? ` (${item.info.hostname})` : "";
    return `${runnerDisplayName(t, item.name)}${host}`;
  };

  const runnerDisabledReason = (item: Runner) => {
    const reason = runnerUnavailableReason(item);
    return reason ? t(reason === "offline" ? "runs.runnerOffline" : "runs.runnerNoHeadless") : "";
  };

  const loadingRunners = !runnersLoaded || (runnersLoading && runners.length === 0);
  const fullAccess = Boolean(
    !activeContest &&
      localRunner &&
      agent &&
      runner?.id === localRunner.id &&
      permissionOf(runner, agent.kind) === "full",
  );

  return (
    <div className="confirm-backdrop run-dispatch-backdrop" onClick={onClose}>
      <div
        className="confirm-dialog run-dispatch-dialog"
        role="dialog"
        aria-modal="true"
        aria-labelledby="run-dispatch-title"
        onClick={(event) => event.stopPropagation()}
      >
        <header>
          <span className="confirm-dialog-icon">
            <IconSend size={17} />
          </span>
          <div>
            <h2 id="run-dispatch-title">{t("runs.dispatchTitle")}</h2>
            <p>
              {t(localRunner || localDown ? "runs.dispatchDescriptionLocal" : "runs.dispatchDescription", {
                title: nodeTitle,
              })}
            </p>
          </div>
          <button
            type="button"
            className="confirm-dialog-close"
            onClick={onClose}
            aria-label={t("common.close")}
          >
            <IconClose size={14} />
          </button>
        </header>

        <form
          id="run-dispatch-form"
          className="run-dispatch-body"
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
                onClick={() => {
                  onClose();
                  window.dispatchEvent(new CustomEvent("nodevas-open-budget"));
                }}
              >
                {t("budget.adjust")}
              </button>
            </p>
          )}

          {hasPreflightBusy && !pendingBusy && (
            <p className="run-stage-warning" role="status" data-testid="dispatch-busy-preflight">
              {simple
                ? t("busy.simple.title", { minutes: minutesSince(preflightBusy?.holders?.[0]?.since) })
                : t("busy.title")}
            </p>
          )}

          {simpleSkip && (
            <p className="run-stage-warning" role="status">
              {t("dispatch.simpleNoChecksLocal")}
            </p>
          )}

          {workflow && !simpleSkip && (
            <div className="run-stages">
              {!overrideOn && <StageChain workflow={workflow} />}
              {missingCriteria && (
                <div className="run-stage-block" role="alert">
                  <span>{t("dispatch.needsCriteria")}</span>
                  <button type="button" className="run-inline-link" onClick={writeCriteria}>
                    {t("dispatch.writeCriteria")}
                  </button>
                </div>
              )}
              {missingTestCommand && localRunner && (
                <div className="run-stage-warning" role="status">
                  <span>{t("dispatch.noTestCommandLocal")}</span>{" "}
                  <button type="button" className="run-inline-link" onClick={openThisComputer}>
                    {t("dispatch.setTestCommand")}
                  </button>
                </div>
              )}
              {missingTestCommand && !localRunner && (
                <div className="run-stage-warning" role="status">
                  <span>{t("dispatch.noTestCommand")}</span>
                  <code>{`nodevas runner repo set ${repo?.name ?? "<repo>"} --test-command "go test ./..."`}</code>
                </div>
              )}
              {oldRunner && (
                <p className="run-stage-warning" role="status">
                  {t("dispatch.oldRunner")}
                </p>
              )}
              <label className="route-toggle">
                <input
                  type="checkbox"
                  checked={overrideOn}
                  onChange={(event) => setOverrideOn(event.target.checked)}
                />
                <span>{t("dispatch.override")}</span>
              </label>
              {overrideOn && (
                <label className="run-field">
                  <span>{t("dispatch.overrideReason")}</span>
                  <input
                    value={overrideReason}
                    maxLength={MAX_OVERRIDE_CHARS}
                    placeholder={t("dispatch.overridePlaceholder")}
                    aria-invalid={overrideInvalid ? true : undefined}
                    onChange={(event) => setOverrideReason(event.target.value)}
                  />
                </label>
              )}
              {overrideOn && <small className="run-hint">{t("dispatch.overrideHint")}</small>}
            </div>
          )}

          {loadingRunners ? (
            <p className="run-empty">{t("common.loading")}</p>
          ) : localDown ? (
            <LocalNotReady starting={localState(mine) === "starting"} onOpenSettings={openThisComputer} />
          ) : mine.length === 0 ? (
            <div className="run-empty">
              <p>{t("runs.noRunners")}</p>
              <button type="button" className="run-link-button" onClick={openRunners}>
                {t("runs.manageRunners")}
              </button>
            </div>
          ) : (
            <>
              {ruleNote && (
                <p className="run-route-note" data-testid="dispatch-rule-note">
                  {ruleNote}
                  {ruleMisses.map((miss) => (
                    <span key={miss} className="run-route-miss">
                      {miss}
                    </span>
                  ))}
                </p>
              )}
              {!localRunner && (
                <RunnerCards
                  runners={mine}
                  value={runner?.id ?? ""}
                  onChange={(id) => {
                    touched.current.runner = true;
                    setRunnerId(id);
                  }}
                  disabled={eligible.length === 0}
                  displayName={runnerCardName}
                  disabledReason={runnerDisabledReason}
                />
              )}

              {eligible.length === 0 ? (
                <div className="run-empty">
                  <p>{t("runs.noEligible")}</p>
                  <button type="button" className="run-link-button" onClick={openRunners}>
                    {t("runs.manageRunners")}
                  </button>
                </div>
              ) : (
                <>
                  {!simple && (
                    <label className="route-toggle">
                      <input
                        type="checkbox"
                        checked={contestOn}
                        onChange={(event) => {
                          const on = event.target.checked;
                          setContestOn(on);
                          if (on && !contestDraft) {
                            setContestDraft(buildInitialContestDraft(eligible));
                          }
                        }}
                      />
                      <span>{t("contest.toggle")}</span>
                    </label>
                  )}

                  {activeContest ? (
                    <ContestForm
                      runners={eligible}
                      value={activeDraft ?? undefined}
                      shapeError={contestShapeError}
                      disabled={submitting}
                      onChange={setContestDraft}
                    />
                  ) : (
                    <>
                      <AgentCards
                        agents={runner?.info?.agents ?? []}
                        value={agent?.kind ?? ""}
                        onChange={(kind) => {
                          touched.current.agent = true;
                          setAgentKind(kind);
                        }}
                        simple={simple}
                        empty={
                          !localRunner ? (
                            <p className="run-hint">{t("runs.noAgents")}</p>
                          ) : (
                            <p className="run-hint">
                              {t("runs.noAgentsLocal")}{" "}
                              <button type="button" className="run-inline-link" onClick={openThisComputer}>
                                {t("offerAsk.fixLocal")}
                              </button>
                            </p>
                          )
                        }
                      />

                      {models.length > 0 && (
                        <ModelCards
                          agent={agent?.kind ?? ""}
                          models={models}
                          value={model}
                          onChange={(id) => {
                            touched.current.agent = true;
                            setModel(id);
                          }}
                          simple={simple}
                        />
                      )}

                      <details className="run-advanced" data-testid="dispatch-fallbacks">
                        <summary>{t("routing.fallbacks", { count: fallbacks.length })}</summary>
                        <FallbackChainEditor
                          value={fallbacks}
                          catalog={catalog}
                          onChange={(next) => {
                            touched.current.fallbacks = true;
                            setFallbacks(next);
                          }}
                        />
                      </details>

                      {workflow === null && (
                        <label className="route-toggle">
                          <input
                            type="checkbox"
                            checked={pipelineOn}
                            onChange={(event) => {
                              touched.current.pipeline = true;
                              const on = event.target.checked;
                              setPipelineOn(on);
                              if (on && agentModelProblem(review)) {
                                setReview(
                                  defaultPipeline({ agent: agent?.kind ?? "", model }, catalog).review,
                                );
                              }
                            }}
                          />
                          <span>{t("routing.pipelineToggle")}</span>
                        </label>
                      )}
                      {reviewOn && agent && (
                        <PipelineFields
                          value={{ implement: { agent: agent.kind, model }, review: shownReview, maxRounds: rounds }}
                          catalog={catalog}
                          implementNote={t("dispatch.implementIsAbove", {
                            value: agentModelText({ agent: agent.kind, model }),
                          })}
                          onChange={(next) => {
                            touched.current.pipeline = true;
                            setReview(next.review);
                            setRounds(next.maxRounds);
                          }}
                        />
                      )}
                    </>
                  )}

                  <RepoCards
                    mode="one"
                    repos={repos}
                    value={repoId}
                    onChange={setRepoId}
                    simple={simple}
                    headerAction={
                      runner && repos.length > 0 ? (
                        <AddRepoButton
                          runner={runner}
                          onAdded={(added) => {
                            setRepoId(added.id);
                            setAddedNote(t("repoAdd.added", { name: added.name }));
                          }}
                        />
                      ) : undefined
                    }
                    empty={
                      runner ? (
                        <div className="run-empty run-empty-left">
                          <p>{t(localRunner ? "runs.noReposLocal" : "runs.noRepos")}</p>
                          {!localRunner && <code>nodevas runner repo add &lt;path&gt;</code>}
                          <AddRepoButton
                            runner={runner}
                            onAdded={(added) => {
                              setRepoId(added.id);
                              setAddedNote(t("repoAdd.added", { name: added.name }));
                            }}
                          />
                        </div>
                      ) : undefined
                    }
                  />
                  {addedNote && (
                    <small className="run-hint run-repo-added" role="status">
                      {addedNote}
                    </small>
                  )}

                  <label className="run-field">
                    <span>{t("runs.base")}</span>
                    <input
                      value={base}
                      maxLength={200}
                      placeholder={t("runs.basePlaceholder")}
                      onChange={(event) => setBase(event.target.value)}
                      spellCheck={false}
                      autoCapitalize="off"
                      autoCorrect="off"
                      aria-invalid={baseError ? true : undefined}
                      aria-describedby={baseError ? "run-base-error" : undefined}
                    />
                    {baseError && (
                      <small id="run-base-error" className="run-field-error">
                        {baseError}
                      </small>
                    )}
                  </label>

                  <label className="run-field">
                    <span>
                      {t("runs.prompt")}
                      <small className="run-count">
                        {t("runs.promptCount", {
                          count: prompt.length,
                          max: MAX_RUN_PROMPT_CHARS,
                        })}
                      </small>
                    </span>
                    <textarea
                      value={prompt}
                      rows={4}
                      maxLength={MAX_RUN_PROMPT_CHARS}
                      placeholder={t("runs.promptPlaceholder")}
                      onChange={(event) => setPrompt(event.target.value)}
                      aria-invalid={promptError ? true : undefined}
                      aria-describedby={promptError ? "run-prompt-error" : undefined}
                    />
                    {promptError && (
                      <small id="run-prompt-error" className="run-field-error">
                        {promptError}
                      </small>
                    )}
                  </label>

                  {!activeContest && (
                    <>
                      <RunModePicker
                        runner={runner}
                        agent={agent}
                        model={model}
                        value={mode}
                        onChange={setModeChoice}
                        fallbackReason={defaultModeResult.reason}
                        interactiveDisabled={isInteractiveDisabled(modeCtx)}
                        simple={simple}
                      />

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
              )}
            </>
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

          {error && (
            <div className="notify-message error" role="alert">
              {error}
            </div>
          )}
        </form>

        <footer>
          {fullAccess && (
            <span className="run-full-access" role="note">
              {t("dispatch.fullAccessNote", { agent: agentName(agent?.kind ?? "") })}
            </span>
          )}
          <button type="button" onClick={onClose}>
            {t("common.cancel")}
          </button>
          <button type="submit" form="run-dispatch-form" className="primary" disabled={!ready}>
            {submitting ? t("runs.dispatching") : t("runs.dispatch")}
          </button>
        </footer>
        {/* Outside the form: a portal's submit still bubbles through React
            to its ancestors, and must not send the run. */}
        {stepUpOpen && <StepUpDialog onClose={closeStepUp} />}
      </div>
    </div>
  );
}
