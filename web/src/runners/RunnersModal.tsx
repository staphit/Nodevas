import { useEffect, useState } from "react";
import type { Runner } from "../api";
import { confirmAction } from "../components/ConfirmDialog";
import { IconClose, IconComputer, IconCopy, IconTrash } from "../icons";
import { languageLocale, useI18n } from "../i18n";
import { useRunners } from "./runnerStore";

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

function CopyButton({ value, label }: { value: string; label: string }) {
  const { t } = useI18n();
  const [copied, setCopied] = useState(false);
  return (
    <button
      type="button"
      className="runner-copy"
      aria-label={label}
      title={label}
      onClick={() => {
        void navigator.clipboard
          ?.writeText(value)
          .then(() => setCopied(true))
          .catch(() => setCopied(false));
      }}
    >
      <IconCopy size={13} />
      <span>{copied ? t("runners.copied") : t("runners.copy")}</span>
    </button>
  );
}

function RunnerCard({ runner }: { runner: Runner }) {
  const { t } = useI18n();
  const revoke = useRunners((s) => s.revoke);
  const revoking = useRunners((s) => Boolean(s.revoking[runner.id]));
  const info = runner.info;
  const agents = info?.agents ?? [];
  const repos = info?.repos ?? [];

  const onRevoke = async () => {
    const ok = await confirmAction({
      title: t("runners.revokeTitle", { name: runner.name }),
      description: t("runners.revokeDescription"),
      confirmLabel: t("runners.revoke"),
      tone: "danger",
    });
    if (ok) await revoke(runner.id);
  };

  const seen = runner.online
    ? t("runners.online")
    : runner.lastSeenAt
      ? t("runners.lastSeen", { when: relativeTime(runner.lastSeenAt) })
      : t("runners.neverConnected");

  return (
    <li className="runner-card" data-testid={`runner-${runner.id}`}>
      <div className="runner-card-head">
        <span
          className={`runner-dot${runner.online ? " online" : ""}`}
          aria-hidden="true"
        />
        <div className="runner-card-title">
          <strong>{runner.name}</strong>
          <span>
            {seen}
            {runner.ownerName ? ` · ${runner.ownerName}` : ""}
          </span>
        </div>
        <button
          type="button"
          className="runner-revoke"
          disabled={revoking}
          onClick={() => void onRevoke()}
          aria-label={t("runners.revokeTitle", { name: runner.name })}
          title={t("runners.revoke")}
        >
          <IconTrash size={14} />
        </button>
      </div>

      {info && (
        <div className="runner-meta">
          {[info.hostname, info.os && `${info.os}/${info.arch}`, info.version && `nodevas ${info.version}`]
            .filter(Boolean)
            .join(" · ")}
        </div>
      )}

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
                    : agent.error || t("runners.notInstalled")}
                </span>
                {agent.installed && agent.models.length > 0 && (
                  <span className="runner-models">
                    {agent.models.map((model) => (
                      <span
                        key={model.id}
                        className={`runner-model${model.default ? " default" : ""}`}
                        title={model.default ? t("runners.defaultModel") : undefined}
                      >
                        {model.label || model.id}
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
                  <span>{repo.remote || repo.path}</span>
                </li>
              ))}
            </ul>
          ) : (
            <span className="runner-empty-inline">{t("runners.noRepos")}</span>
          )}
        </div>
      )}
    </li>
  );
}

/**
 * The machines that run agents for this hub. The hub only lists them and hands
 * out tokens; adding a repo or running anything happens on the runner itself.
 */
export function RunnersModal({ onClose }: { onClose: () => void }) {
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
  // The token lives only in this dialog's state: closing it is the last time
  // anyone sees it, which is the point.
  const [created, setCreated] = useState<{ name: string; token: string } | null>(null);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

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
    <div className="confirm-backdrop" onClick={onClose}>
      <div
        className="confirm-dialog runners-dialog"
        role="dialog"
        aria-modal="true"
        aria-label={t("runners.title")}
        onClick={(event) => event.stopPropagation()}
      >
        <header>
          <span className="confirm-dialog-icon">
            <IconComputer size={17} />
          </span>
          <div>
            <h2>{t("runners.title")}</h2>
            <p>{t("runners.description")}</p>
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

        <div className="runners-body">
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
        </div>

        <footer>
          {!adding && (
            <button type="button" className="primary" onClick={() => setAdding(true)}>
              {t("runners.add")}
            </button>
          )}
          {adding && (
            <button type="button" onClick={() => setAdding(false)}>
              {t("common.cancel")}
            </button>
          )}
        </footer>
      </div>
    </div>
  );
}
