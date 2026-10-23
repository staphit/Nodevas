import { useEffect, useMemo, useRef, useState } from "react";
import {
  AuthError,
  ConflictError,
  MAX_RUN_PROMPT_BYTES,
  MAX_RUN_PROMPT_CHARS,
  MAX_PIPELINE_ROUNDS,
  RequestError,
  type Actor,
  type AgentModel,
  type RouteSuggestion,
  type Run,
  type Runner,
  type RunnerAgent,
} from "../api";
import { useViewer } from "../components/SignIn";
import { IconClose, IconSend } from "../icons";
import { useI18n } from "../i18n";
import { useApp } from "../store";
import { useRunners } from "../runners/runnerStore";
import {
  agentCatalog,
  agentModelProblem,
  agentModelText,
  defaultPipeline,
  FallbackChainEditor,
  PipelineFields,
  trimAgentModel,
} from "./AgentModelFields";
import { budgetExceededText } from "./BudgetSettings";
import { matchTakesValue, routeLabel } from "./RoutingSettings";
import { fetchRouteSuggestion, useRouting } from "./routingStore";
import { useBudget } from "./budgetStore";
import { useRuns } from "./runStore";

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

function installedAgents(runner: Runner | undefined): RunnerAgent[] {
  return (runner?.info?.agents ?? []).filter((agent) => agent.installed);
}

function defaultModel(agent: RunnerAgent | undefined): string {
  const models = agent?.models ?? [];
  return (models.find((model) => model.default) ?? models[0])?.id ?? "";
}

/**
 * Dispatches a node to an agent on one of the viewer's runners: runner, then
 * agent, then model, then repository. Nothing starts here — the runner shows
 * the offer and someone there accepts it.
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
  const viewer = useViewer();
  const nodeTitle = useApp(
    (s) => s.graph?.nodes?.find((node) => node.id === nodeId)?.title || nodeId,
  );
  const runners = useRunners((s) => s.runners);
  const runnersLoaded = useRunners((s) => s.loaded);
  const runnersLoading = useRunners((s) => s.loading);
  const runnersError = useRunners((s) => s.error);
  const refreshRunners = useRunners((s) => s.refresh);
  const createRun = useRuns((s) => s.createRun);

  const mine = useMemo(() => ownRunners(runners, viewer), [runners, viewer]);
  const eligible = useMemo(
    () => mine.filter((runner) => runnerUnavailableReason(runner) === ""),
    [mine],
  );

  const [runnerId, setRunnerId] = useState("");
  const [agentKind, setAgentKind] = useState("");
  // What the user picked; each falls back to a valid value below when the
  // pick is not (or no longer) offered.
  const [modelChoice, setModel] = useState("");
  const [repoChoice, setRepoId] = useState("");
  const [base, setBase] = useState("");
  const [prompt, setPrompt] = useState("");
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

  // The suggestion names its rule, not the rule's tag or kind: when the hub
  // does not send the value, it is looked up in the viewer's rules.
  const routingRules = useRouting((s) => s.rules);
  const needsRuleValue =
    suggestion !== null && suggestion.value === undefined && matchTakesValue(suggestion.match);
  useEffect(() => {
    if (needsRuleValue) void useRouting.getState().load();
  }, [needsRuleValue]);

  useEffect(() => {
    void refreshRunners();
  }, [refreshRunners]);

  // Today's budget, so the dialog can say up front that the hub would refuse.
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

  // Keep each choice valid as the one above it changes (or a runners-changed
  // push replaces the list): an unavailable runner, an agent it no longer has,
  // a model or repo that went away all fall back to the first sensible value.
  // Derived on every render rather than corrected in effects, so the form
  // never shows a frame with a stale choice.
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
  const reviewInvalid = pipelineOn && agentModelProblem(review) !== "";

  const ready =
    Boolean(runner && agent && model && repoId) &&
    !baseError &&
    !promptError &&
    !fallbackInvalid &&
    !reviewInvalid &&
    !submitting;

  // "From rule: tag = review", and what of it this runner could not honour.
  const ruleValue =
    suggestion?.value ??
    routingRules?.find((rule) => rule.id === suggestion?.ruleId)?.value ??
    "";
  const ruleNote = suggestion
    ? t("dispatch.fromRule", { rule: routeLabel(t, suggestion.match, ruleValue) })
    : "";
  const wanted = suggestion ? (suggestion.pipeline?.implement ?? suggestion.target) : undefined;
  const ruleMisses: string[] = [];
  if (suggestion && runner) {
    if (suggestion.runnerId && !touched.current.runner && runner.id !== suggestion.runnerId) {
      ruleMisses.push(t("dispatch.ruleRunnerMissing", { runner: runner.name }));
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

  const submit = async () => {
    if (!ready || !runner || !agent) return;
    setSubmitting(true);
    setError(null);
    try {
      const run = await createRun({
        nodeId,
        runnerId: runner.id,
        agent: agent.kind,
        model,
        repoId,
        ...(baseValue ? { base: baseValue } : {}),
        ...(promptValue ? { prompt: promptValue } : {}),
        ...(fallbacks.length > 0 ? { fallbacks: fallbacks.map(trimAgentModel) } : {}),
        ...(pipelineOn
          ? {
              pipeline: {
                implement: { agent: agent.kind, model },
                review: trimAgentModel(review),
                maxRounds: rounds,
              },
            }
          : {}),
      });
      onCreated(run);
    } catch (caught) {
      const text = caught instanceof Error ? caught.message : String(caught);
      const overBudget = caught instanceof RequestError && caught.status === 429;
      setError(
        caught instanceof AuthError && caught.status === 403
          ? t("runs.permissionDenied", { message: text })
          : // On this route a 409 only ever means the runner cannot take runs.
            caught instanceof ConflictError
            ? t("runs.runnerCannotRun")
            : overBudget
              ? t("runs.overBudget", { message: text })
              : text,
      );
      // The refusal names the limit; the warning line catches up with it.
      if (overBudget) void useBudget.getState().load();
    } finally {
      setSubmitting(false);
    }
  };

  const openRunners = () => {
    onClose();
    window.dispatchEvent(new CustomEvent("nodevas-open-runners"));
  };

  const runnerLabel = (item: Runner) => {
    const reason = runnerUnavailableReason(item);
    const host = item.info?.hostname && item.info.hostname !== item.name ? ` (${item.info.hostname})` : "";
    return reason
      ? `${item.name}${host} — ${t(reason === "offline" ? "runs.runnerOffline" : "runs.runnerNoHeadless")}`
      : `${item.name}${host}`;
  };

  const loadingRunners = !runnersLoaded || (runnersLoading && runners.length === 0);

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
            <p>{t("runs.dispatchDescription", { title: nodeTitle })}</p>
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
                  useBudget.getState().requestShow(true);
                  openRunners();
                }}
              >
                {t("budget.adjust")}
              </button>
            </p>
          )}

          {loadingRunners ? (
            <p className="run-empty">{t("common.loading")}</p>
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
              <label className="run-field">
                <span>{t("runs.runner")}</span>

                <select
                  value={runner?.id ?? ""}
                  onChange={(event) => {
                    touched.current.runner = true;
                    setRunnerId(event.target.value);
                  }}
                  disabled={eligible.length === 0}
                >
                  {eligible.length === 0 && <option value="">{t("runs.noEligibleOption")}</option>}
                  {mine.map((item) => (
                    <option
                      key={item.id}
                      value={item.id}
                      disabled={runnerUnavailableReason(item) !== ""}
                    >
                      {runnerLabel(item)}
                    </option>
                  ))}
                </select>
              </label>

              {eligible.length === 0 ? (
                <div className="run-empty">
                  <p>{t("runs.noEligible")}</p>
                  <button type="button" className="run-link-button" onClick={openRunners}>
                    {t("runs.manageRunners")}
                  </button>
                </div>
              ) : (
                <>
                  <div className="run-field-row">
                    <label className="run-field">
                      <span>{t("runs.agent")}</span>
                      <select
                        value={agent?.kind ?? ""}
                        onChange={(event) => {
                          touched.current.agent = true;
                          setAgentKind(event.target.value);
                        }}
                        disabled={agents.length === 0}
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
                      <span>{t("runs.model")}</span>
                      <select
                        value={model}
                        onChange={(event) => {
                          touched.current.agent = true;
                          setModel(event.target.value);
                        }}
                        disabled={models.length === 0}
                      >
                        {models.length === 0 && <option value="">{t("runs.noModelsOption")}</option>}
                        {models.map((item) => (
                          <option key={item.id} value={item.id}>
                            {(item.label || item.id) + (item.default ? t("runs.defaultSuffix") : "")}
                          </option>
                        ))}
                      </select>
                    </label>
                  </div>
                  {agents.length === 0 && <p className="run-hint">{t("runs.noAgents")}</p>}

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
                  {pipelineOn && agent && (
                    <PipelineFields
                      value={{ implement: { agent: agent.kind, model }, review, maxRounds: rounds }}
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

                  <label className="run-field">
                    <span>{t("runs.repo")}</span>
                    <select
                      value={repoId}
                      onChange={(event) => setRepoId(event.target.value)}
                      disabled={repos.length === 0}
                    >
                      {repos.length === 0 && <option value="">{t("runs.noReposOption")}</option>}
                      {repos.map((repo) => (
                        <option key={repo.id} value={repo.id}>
                          {repo.path ? `${repo.name} — ${repo.path}` : repo.name}
                        </option>
                      ))}
                    </select>
                  </label>
                  {repos.length === 0 && (
                    <div className="run-empty run-empty-left">
                      <p>{t("runs.noRepos")}</p>
                      <code>nodevas runner repo add &lt;path&gt;</code>
                    </div>
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

                  <p className="run-note">{t("runs.acceptNote")}</p>
                </>
              )}
            </>
          )}

          {error && (
            <div className="notify-message error" role="alert">
              {error}
            </div>
          )}
        </form>

        <footer>
          <button type="button" onClick={onClose}>
            {t("common.cancel")}
          </button>
          <button type="submit" form="run-dispatch-form" className="primary" disabled={!ready}>
            {submitting ? t("runs.dispatching") : t("runs.dispatch")}
          </button>
        </footer>
      </div>
    </div>
  );
}
