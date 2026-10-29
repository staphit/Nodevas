import { useCallback, useEffect, useId, useState } from "react";
import type { Runner } from "../api";
import { confirmAction } from "../components/ConfirmDialog";
import { IconShield, IconTrash } from "../icons";
import { MODE_INTERACTIVE } from "../runs/autoAccept";
import { formatClock, StepUpDialog } from "../stepup/StepUpDialog";
import { stepUpActive, useStepUp } from "../stepup/stepUpStore";
import { formatList, languageLocale, useI18n } from "../i18n";
import { CopyButton, LinkDeviceDialog } from "./LinkDevice";
import { useTeamServer } from "../components/SignIn";
import { useRunners } from "./runnerStore";
import { runnerAgentProblem, runnerDisplayName, runnerModelLabel } from "./runnerName";

/** "3 minutes ago" in the reader's language, or "" for an unparseable time. */
export function relativeTime(iso: string, now = Date.now()): string {
  const at = Date.parse(iso);
  if (!Number.isFinite(at)) return "";
  const seconds = Math.round((at - now) / 1000);
  const format = new Intl.RelativeTimeFormat(languageLocale(), { numeric: "auto" });
  const abs = Math.abs(seconds);
  if (abs < 60) return format.format(seconds, "second");
  if (abs < 3600) return format.format(Math.round(seconds / 60), "minute");
  if (abs < 86400) return format.format(Math.round(seconds / 3600), "hour");
  return format.format(Math.round(seconds / 86400), "day");
}

/** The runner CLI's auto-accept commands, as `nodevas runner` spells them. */
export const AUTOACCEPT_COMMANDS = [
  "nodevas runner autoaccept on|off|show",
  "nodevas runner autoaccept step-up required|optional",
  "nodevas runner autoaccept repo|agent|mode add|remove …",
];

/** The runner CLI command to toggle relaying permission prompts to Nodevas. */
export const PERMISSION_RELAY_COMMAND = "nodevas runner set permissionRelay true (or false)";

function PolicyChips({ items, empty }: { items: string[]; empty: string }) {
  if (items.length === 0) return <span className="runner-empty-inline">{empty}</span>;
  return (
    <span className="runner-models">
      {items.map((item) => (
        <span key={item} className="runner-model">
          {item}
        </span>
      ))}
    </span>
  );
}

/**
 * A runner's auto-accept allowlist (W6), read-only: the runner owns it and
 * enforces it, so the way to change it is a command on that machine.
 */
export function AutoAcceptPolicyView({ runner }: { runner: Runner }) {
  const { t } = useI18n();
  // Step-up words are hidden on a hub that does not use step-up at all.
  const stepUpShown = useStepUp((s) => s.status?.required !== false);
  const policy = runner.info?.autoAccept;
  const repoName = (id: string) => runner.info?.repos?.find((repo) => repo.id === id)?.name || id;
  const modes = policy?.modes?.length ? policy.modes : [""];

  let body: React.ReactNode;
  if (!policy) {
    body = <span className="runner-empty-inline">{t("runners.autoAcceptUnknown")}</span>;
  } else if (!policy.enabled) {
    body = <span className="runner-empty-inline">{t("runners.autoAcceptOff")}</span>;
  } else {
    body = (
      <dl className="runner-policy">
        <div>
          <dt>{t("runners.repos")}</dt>
          <dd>
            <PolicyChips items={(policy.repos ?? []).map(repoName)} empty={t("runners.policyNone")} />
          </dd>
        </div>
        <div>
          <dt>{t("runners.agents")}</dt>
          <dd>
            <PolicyChips items={policy.agents ?? []} empty={t("runners.policyNone")} />
          </dd>
        </div>
        <div>
          <dt>{t("runners.policyModes")}</dt>
          <dd>
            <PolicyChips
              items={modes.map((mode) =>
                mode === "" || mode === "headless"
                  ? t("runners.modeHeadless")
                  : mode === MODE_INTERACTIVE
                    ? t("runners.modeInteractive")
                    : mode,
              )}
              empty={t("runners.policyNone")}
            />
          </dd>
        </div>
        {stepUpShown && (
          <div>
            <dt>{t("runners.policyStepUp")}</dt>
            <dd className={policy.requireStepUp ? "" : "runner-policy-warn"}>
              {policy.requireStepUp ? t("runners.stepUpRequired") : t("runners.stepUpNotRequired")}
            </dd>
          </div>
        )}
      </dl>
    );
  }

  return (
    <div className="runner-section runner-autoaccept" data-testid={`runner-policy-${runner.id}`}>
      <span className="runner-section-label">
        {t("runners.autoAccept")}
        {policy && (
          <span className={`runner-policy-state${policy.enabled ? " on" : ""}`}>
            {policy.enabled ? t("runners.autoAcceptOn") : t("runners.autoAcceptOffShort")}
          </span>
        )}
      </span>
      {body}
      <span className="runner-hint runner-policy-hint">
        {t("runners.autoAcceptHint")}{" "}
        {AUTOACCEPT_COMMANDS.map((command) => (
          <code key={command}>{command}</code>
        ))}
      </span>
    </div>
  );
}

/** The step-up line above the auto-accept policies: whether this browser
 * is verified, and the way to verify. Nothing on a hub without step-up. */
function StepUpRow({ onVerify }: { onVerify: () => void }) {
  const { t } = useI18n();
  const status = useStepUp((s) => s.status);
  if (!status?.required) return null;
  const active = stepUpActive(status);
  const until = formatClock(status.until);
  return (
    <section className={`runner-stepup${active ? " is-active" : ""}`} aria-label={t("stepUp.title")}>
      <IconShield size={15} />
      <span className="runner-stepup-text">
        {active
          ? until
            ? t("stepUp.verifiedUntil", { time: until })
            : t("stepUp.verified")
          : t("runners.stepUpInactive")}
      </span>
      <button type="button" className="runner-copy" onClick={onVerify}>
        {active ? t("runners.verifyAgain") : t("runners.verifyIdentity")}
      </button>
    </section>
  );
}

/** How many runners here auto-accept, of how many. */
export function autoAcceptCounts(runners: readonly Runner[]): { on: number; total: number } {
  return {
    on: runners.filter((runner) => runner.info?.autoAccept?.enabled).length,
    total: runners.length,
  };
}

/**
 * Every runner's auto-accept allowlist, read-only, with the step-up line that
 * decides whether a strict runner takes this browser's runs: Settings ›
 * Agents › Advanced › Auto-accept. Changing a policy is a command on that
 * runner (AUTOACCEPT_COMMANDS); there is no way to do it from the web.
 */
export function AutoAcceptSettings() {
  const { t } = useI18n();
  const runners = useRunners((s) => s.runners);
  const loaded = useRunners((s) => s.loaded);
  const refresh = useRunners((s) => s.refresh);
  const [stepUpOpen, setStepUpOpen] = useState(false);
  const closeStepUp = useCallback(() => setStepUpOpen(false), []);

  useEffect(() => {
    if (!useRunners.getState().loaded) void refresh();
  }, [refresh]);
  // W6: whether this browser is stepped up, asked afresh on every open.
  useEffect(() => {
    void useStepUp.getState().load();
  }, []);

  return (
    <div className="runner-autoaccept-list">
      <StepUpRow onVerify={() => setStepUpOpen(true)} />
      {!loaded ? (
        <p className="runner-empty">{t("common.loading")}</p>
      ) : runners.length === 0 ? (
        <p className="runner-empty">{t("runners.empty")}</p>
      ) : (
        <ul className="runner-list">
          {runners.map((runner) => (
            <li key={runner.id} className="runner-card">
              <strong>{runnerDisplayName(t, runner.name)}</strong>
              <AutoAcceptPolicyView runner={runner} />
            </li>
          ))}
        </ul>
      )}
      {stepUpOpen && <StepUpDialog onClose={closeStepUp} />}
    </div>
  );
}

type Translate = (key: string, values?: Record<string, string | number>) => string;

/** "Online · claude, codex · 2 repos": a runner in one line. */
export function runnerSummary(runner: Runner, t: Translate): string {
  const seen = runner.online
    ? t("runners.online")
    : runner.lastSeenAt
      ? t("runners.lastSeen", { when: relativeTime(runner.lastSeenAt) })
      : t("runners.neverConnected");
  const info = runner.info;
  if (!info) return seen;
  const agents = (info.agents ?? []).filter((agent) => agent.installed).map((agent) => agent.kind);
  const repos = info.repos?.length ?? 0;
  return [
    seen,
    formatList(agents),
    repos === 1 ? t("settingsPage.runnerRepoOne") : t("settingsPage.runnerRepos", { count: repos }),
  ]
    .filter(Boolean)
    .join(" · ");
}

/**
 * A runner in one line: whether it is up, its agents and how many repos.
 * The long parts (the machine, every agent's version and models, the repos'
 * paths) open under Details.
 */
function RunnerCard({ runner }: { runner: Runner }) {
  const { t } = useI18n();
  const revoke = useRunners((s) => s.revoke);
  const revoking = useRunners((s) => Boolean(s.revoking[runner.id]));
  const [open, setOpen] = useState(false);
  const detailsId = useId();
  const info = runner.info;
  const agents = info?.agents ?? [];
  const repos = info?.repos ?? [];

  const onRevoke = async () => {
    const ok = await confirmAction({
      title: t("runners.revokeTitle", { name: runnerDisplayName(t, runner.name) }),
      description: t("runners.revokeDescription"),
      confirmLabel: t("runners.revoke"),
      tone: "danger",
    });
    if (ok) await revoke(runner.id);
  };

  const summary = runnerSummary(runner, t);
  const machine = info
    ? [info.hostname, info.os && `${info.os}/${info.arch}`, info.version && `nodevas ${info.version}`]
        .filter(Boolean)
        .join(" · ")
    : "";

  return (
    <li className="runner-card" data-testid={`runner-${runner.id}`}>
      <div className="runner-card-head">
        <span className={`runner-dot${runner.online ? " online" : ""}`} aria-hidden="true" />
        <div className="runner-card-title">
          <strong title={runnerDisplayName(t, runner.name)}>{runnerDisplayName(t, runner.name)}</strong>
          {/* Cut with an ellipsis where the card is narrow; whole on hover. */}
          <span title={summary}>{summary}</span>
        </div>
        <button
          type="button"
          className="runner-copy"
          aria-expanded={open}
          aria-controls={open ? detailsId : undefined}
          aria-label={t("settingsPage.runnerDetailsFor", { name: runnerDisplayName(t, runner.name) })}
          onClick={() => setOpen((value) => !value)}
        >
          {t("settingsPage.runnerDetails")}
        </button>
      </div>

      {open && (
        <div id={detailsId} className="runner-details">
          {machine && <div className="runner-meta">{machine}</div>}
          {runner.ownerName && <div className="runner-meta">{runner.ownerName}</div>}

          {agents.length > 0 && (
            <div className="runner-section">
              <span className="runner-section-label">{t("runners.agents")}</span>
              <ul className="runner-agents">
                {agents.map((agent) => (
                  <li key={agent.kind} className={agent.installed ? "" : "missing"}>
                    <span className="runner-agent-kind">{agent.kind}</span>
                    <span className="runner-agent-version">
                      {agent.installed
                        ? agent.version || t("runners.installed")
                        : agent.error ? runnerAgentProblem(t, agent.error) : t("runners.notInstalled")}
                    </span>
                    {agent.installed && agent.models.length > 0 && (
                      <span className="runner-models">
                        {agent.models.map((model) => (
                          <span
                            key={model.id}
                            className={`runner-model${model.default ? " default" : ""}`}
                            title={model.default ? t("runners.defaultModel") : undefined}
                          >
                            {runnerModelLabel(t, model)}
                            {model.default ? " ★" : ""}
                          </span>
                        ))}
                      </span>
                    )}
                  </li>
                ))}
              </ul>
            </div>
          )}

          {info && (
            <div className="runner-section">
              <span className="runner-section-label">{t("runners.repos")}</span>
              {repos.length > 0 ? (
                <ul className="runner-repos">
                  {repos.map((repo) => (
                    <li key={repo.id}>
                      <strong>{repo.name}</strong>
                      {/* Wrapped whole, never cut: a path is read and copied. */}
                      <span className="runner-repo-path" title={repo.remote || repo.path}>
                        {repo.remote || repo.path}
                      </span>
                    </li>
                  ))}
                </ul>
              ) : (
                <span className="runner-empty-inline">{t("runners.noRepos")}</span>
              )}
            </div>
          )}

          <div className="runner-section runner-permission-relay" data-testid={`runner-relay-${runner.id}`}>
            <span className="runner-section-label">
              {t("runners.permissionRelayStatus", { status: "\0" }).split("\0")[0]}
              <span className={`runner-policy-state${info?.permissionRelay ? " on" : ""}`}>
                {info?.permissionRelay ? t("runners.permissionRelayOn") : t("runners.permissionRelayOff")}
              </span>
              {t("runners.permissionRelayStatus", { status: "\0" }).split("\0")[1]}
            </span>
            <span className="runner-hint">
              {t("runners.permissionRelayHelp", { command: "\0" }).split("\0")[0]}
              <code>{PERMISSION_RELAY_COMMAND}</code>
              {t("runners.permissionRelayHelp", { command: "\0" }).split("\0")[1]}
            </span>
          </div>

          {/* Revoking is rare and final: it waits in the details, not on
              every runner's line beside Details. */}
          <div className="runner-details-actions">
            <button
              type="button"
              className="runner-revoke"
              disabled={revoking}
              onClick={() => void onRevoke()}
              aria-label={t("runners.revokeTitle", { name: runnerDisplayName(t, runner.name) })}
              title={t("runners.revoke")}
            >
              <IconTrash size={14} />
              <span>{t("runners.revoke")}</span>
            </button>
          </div>
        </div>
      )}
    </li>
  );
}

/**
 * The machines that run agents for this hub, in Settings › Agents. The hub
 * only lists them and hands out tokens; adding a repo or running anything
 * happens on the runner itself.
 *
 * Each runner is one line with its details folded away; "Add runner", the
 * one primary button here, opens its form in place. The daily budget,
 * routing rules, each runner's auto-accept policy, usage and the runners'
 * Access token are rows of Agents › Advanced beside this; browser
 * notifications are under General.
 */
export function RunnersPanel({
  addRequested = false,
  onAddOpened,
}: {
  /** Asked from outside (the search box) to open the add form; the panel
   * opens it and says so, so the request does not outlive it. */
  addRequested?: boolean;
  onAddOpened?: () => void;
}) {
  const { t } = useI18n();
  const runners = useRunners((s) => s.runners);
  const loading = useRunners((s) => s.loading);
  const loaded = useRunners((s) => s.loaded);
  const error = useRunners((s) => s.error);
  const creating = useRunners((s) => s.creating);
  const refresh = useRunners((s) => s.refresh);
  const create = useRunners((s) => s.create);
  const [adding, setAdding] = useState(false);
  const [name, setName] = useState("");
  // The token lives only in this panel's state: leaving it is the last time
  // anyone sees it, which is the point.
  const [created, setCreated] = useState<{ name: string; token: string } | null>(null);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  useEffect(() => {
    if (!addRequested) return;
    setAdding(true);
    onAddOpened?.();
  }, [addRequested, onAddOpened]);

  const teamServer = useTeamServer();
  const [linkOpen, setLinkOpen] = useState(false);
  const closeLink = useCallback(() => setLinkOpen(false), []);

  const submit = async () => {
    if (!name.trim()) return;
    const result = await create(name);
    if (result) {
      setCreated({ name: result.runner.name, token: result.token });
      setAdding(false);
      setName("");
    }
  };

  const hub = typeof window === "undefined" ? "" : window.location.origin;
  const loginCommand = `nodevas runner login --hub ${hub}`;

  return (
    <div className="runners-panel">
      {created && (
        <section className="runner-token" aria-label={t("runners.token")}>
          <strong>{t("runners.tokenTitle", { name: created.name })}</strong>
          <p className="runner-token-warning" role="alert">
            {t("runners.tokenWarning")}
          </p>
          <div className="runner-token-row">
            <input
              readOnly
              value={created.token}
              aria-label={t("runners.token")}
              onFocus={(event) => event.currentTarget.select()}
            />
            <CopyButton value={created.token} label={t("runners.copyToken")} />
          </div>
          <ol className="runner-steps">
            <li>
              {t("runners.stepLogin")}
              <div className="runner-token-row">
                <code>{loginCommand}</code>
                <CopyButton value={loginCommand} label={t("runners.copyCommand")} />
              </div>
              <span className="runner-hint">{t("runners.stepAccess")}</span>
            </li>
            <li>
              {t("runners.stepStart")}
              <div className="runner-token-row">
                <code>nodevas runner</code>
              </div>
            </li>
          </ol>
          <button type="button" className="runner-done" onClick={() => setCreated(null)}>
            {t("runners.tokenSaved")}
          </button>
        </section>
      )}

      {error && (
        <div className="notify-message error" role="status">
          {error}
        </div>
      )}

      {!loaded || (loading && runners.length === 0) ? (
        <p className="runner-empty">{t("common.loading")}</p>
      ) : runners.length === 0 ? (
        <p className="runner-empty">{t("runners.empty")}</p>
      ) : (
        <ul className="runner-list">
          {runners.map((runner) => (
            <RunnerCard key={runner.id} runner={runner} />
          ))}
        </ul>
      )}

      {adding && (
        <form
          className="runner-add"
          onSubmit={(event) => {
            event.preventDefault();
            void submit();
          }}
        >
          <label htmlFor="runner-name">{t("runners.name")}</label>
          <div className="runner-token-row">
            <input
              id="runner-name"
              value={name}
              maxLength={64}
              autoFocus
              placeholder={t("runners.namePlaceholder")}
              onChange={(event) => setName(event.target.value)}
            />
            <button type="submit" className="primary" disabled={creating || !name.trim()}>
              {t("runners.create")}
            </button>
          </div>
        </form>
      )}

      <div className="runners-actions">
        {adding ? (
          <button type="button" onClick={() => setAdding(false)}>
            {t("common.cancel")}
          </button>
        ) : (
          <>
            <button type="button" className="primary" onClick={() => setAdding(true)}>
              {t("runners.add")}
            </button>
            {/* Device links are for runners on other machines reaching a
                team server; a standalone hub is only reachable from this
                one, where "Add runner" is the way in. */}
            {teamServer && (
              <button type="button" onClick={() => setLinkOpen(true)}>
                {t("runners.link.open")}
              </button>
            )}
          </>
        )}
      </div>
      {linkOpen && <LinkDeviceDialog onClose={closeLink} />}
    </div>
  );
}
