import { useEffect, useMemo, useState } from "react";
import {
  AuthError,
  ConflictError,
  MAX_RUN_PROMPT_BYTES,
  MAX_RUN_PROMPT_CHARS,
  type Actor,
  type Run,
  type Runner,
  type RunnerAgent,
} from "../api";
import { useViewer } from "../components/SignIn";
import { IconClose, IconSend } from "../icons";
import { useI18n } from "../i18n";
import { useApp } from "../store";
import { useRunners } from "../runners/runnerStore";
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

  useEffect(() => {
    void refreshRunners();
  }, [refreshRunners]);

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

  const ready =
    Boolean(runner && agent && model && repoId) && !baseError && !promptError && !submitting;

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
      });
      onCreated(run);
    } catch (caught) {
      const text = caught instanceof Error ? caught.message : String(caught);
      setError(
        caught instanceof AuthError && caught.status === 403
          ? t("runs.permissionDenied", { message: text })
          : // req() turns every 409 into a ConflictError and drops the body;
            // on this route a 409 only ever means the runner cannot take runs.
            caught instanceof ConflictError
            ? t("runs.runnerCannotRun")
            : text,
      );
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
              <label className="run-field">
                <span>{t("runs.runner")}</span>
                <select
                  value={runner?.id ?? ""}
                  onChange={(event) => setRunnerId(event.target.value)}
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
                        onChange={(event) => setAgentKind(event.target.value)}
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
                        onChange={(event) => setModel(event.target.value)}
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
