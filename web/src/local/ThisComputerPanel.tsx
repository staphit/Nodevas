// "This computer" (single-user edition): the part of Nodevas that runs
// agents, in plain words. Which agents it found (and how to get or add one),
// the repos added here, how many runs it takes at once, what each agent may
// do without asking, whether to be asked before every run and before each
// command an agent that may only edit wants to run. Changes go to the built-in runner
// (PUT /api/runners/:id/settings, the repo routes); new runs use them, runs
// already going keep theirs. Command lines and the settings file only show
// under Advanced, collapsed. In Simple the repos are folders and no test
// command is asked for.

import { useEffect, useId, useState } from "react";

import {
  api,
  type PermissionLevel,
  type Runner,
  type RunnerRepo,
  type RunnerSettingsRequest,
} from "../api";
import { confirmAction } from "../components/ConfirmDialog";
import { agentName } from "../game/labels";
import { formatList, useI18n } from "../i18n";
import { useLocalRunner, useLocalState } from "../runners/edition";
import { useRunners } from "../runners/runnerStore";
import { useSimpleMode } from "../simple/simpleMode";
import { useGlobalAgents } from "../station/globalAgentsStore";
import { localSettingsErrorText } from "./AddAgentDialog";
import { AddRepoDialog } from "./AddRepoDialog";
import { AgentScanStep } from "./AgentScanStep";
import { LocalNotReady } from "./LocalNotReady";
import { holdRunner, repoErrorText } from "./useAddRepo";

type Translate = ReturnType<typeof useI18n>["t"];

const LEVELS: readonly PermissionLevel[] = ["read", "edit", "full"];

/** Each agent's runner setting and the mode each level maps to (the same
 * table the runner keeps): shown under Advanced as command lines. */
export const PERMISSION_MODES: Record<string, { key: string } & Record<PermissionLevel, string>> = {
  claude: { key: "permissionMode", read: "plan", edit: "acceptEdits", full: "bypassPermissions" },
  codex: { key: "codexSandbox", read: "read-only", edit: "workspace-write", full: "danger-full-access" },
  gemini: { key: "geminiApprovalMode", read: "default", edit: "auto_edit", full: "yolo" },
  opencode: { key: "opencodePermission", read: "read-only", edit: "edit", full: "full" },
  antigravity: { key: "antigravityMode", read: "plan", edit: "accept-edits", full: "full" },
};

/** The most runs at once when the runner names no ceiling. */
const DEFAULT_CEILING = 8;

/** An agent's level as the runner reports it; "edit" (the shipped default)
 * when it reports none. */
export function permissionOf(runner: Runner | undefined, kind: string): PermissionLevel | "custom" {
  const level = runner?.info?.local?.permission?.[kind];
  if (level === "read" || level === "edit" || level === "full" || level === "custom") return level;
  return "edit";
}

/** "Claude、Codex · 2 repos · 2 at a time" ("2 folders" in Simple), or why
 * there is nothing to say. */
export function localSummary(t: Translate, runner: Runner | undefined, simple = false): string {
  if (!runner?.online) return t("local.unavailable");
  const agents = (runner.info?.agents ?? []).filter((agent) => agent.installed).map((agent) => agentName(agent.kind));
  const repos = runner.info?.repos?.length ?? 0;
  const values = {
    agents: agents.length > 0 ? formatList(agents) : t("local.noAgents"),
    count: Math.max(1, runner.info?.maxConcurrent || 1),
  };
  if (simple) return t(repos === 1 ? "simple.local.summaryOne" : "simple.local.summary", { ...values, folders: repos });
  return t(repos === 1 ? "local.summaryOneRepo" : "local.summary", { ...values, repos });
}

/** The command lines that make the same settings, for Advanced. */
export function localCommands(runner: Runner): string[] {
  const local = runner.info?.local;
  const config = local?.configPath ? ` --config "${local.configPath}"` : "";
  const lines = [`nodevas runner set maxConcurrent ${Math.max(1, runner.info?.maxConcurrent || 1)}${config}`];
  for (const agent of runner.info?.agents ?? []) {
    const modes = PERMISSION_MODES[agent.kind];
    const level = permissionOf(runner, agent.kind);
    if (!agent.installed || !modes || level === "custom") continue;
    lines.push(`nodevas runner set ${modes.key} ${modes[level]}${config}`);
  }
  if (local) {
    lines.push(`nodevas runner set autoPR ${local.autoPR}${config}`);
    lines.push(`nodevas runner set prDraft ${local.prDraft}${config}`);
  }
  return lines;
}

type Note = { kind: "saved" | "error"; text: string };

export function ThisComputerPanel() {
  const { t } = useI18n();
  const runner = useLocalRunner();
  const state = useLocalState();
  const loaded = useRunners((s) => s.loaded);
  const loading = useRunners((s) => s.loading);
  const [saving, setSaving] = useState(false);
  const [note, setNote] = useState<Note | null>(null);
  const [adding, setAdding] = useState(false);
  const [warnFor, setWarnFor] = useState("");
  const simple = useSimpleMode();

  useEffect(() => {
    if (!useRunners.getState().loaded) void useRunners.getState().refresh();
  }, []);

  if (!runner) {
    return (
      <div className="this-computer">
        {!loaded || loading ? (
          <p className="this-computer-note" role="status">
            {t("common.loading")}
          </p>
        ) : (
          <LocalNotReady starting={state === "starting"} />
        )}
      </div>
    );
  }

  const info = runner.info;
  const local = info?.local ?? null;
  const agents = info?.agents ?? [];
  const installed = agents.filter((agent) => agent.installed);
  const repos = info?.repos ?? [];
  const max = Math.max(1, info?.maxConcurrent || 1);
  const ceiling = info?.maxConcurrentCeiling || DEFAULT_CEILING;
  const shellFree = new Set(local?.shellWithoutAsking ?? []);
  const locked = saving || !runner.online;

  const save = async (body: RunnerSettingsRequest, saved = t("local.saved")) => {
    if (saving) return false;
    setSaving(true);
    setNote(null);
    try {
      const res = await api.setRunnerSettings(runner.id, body);
      holdRunner(res.runner);
      setNote(res.persisted ? { kind: "saved", text: saved } : { kind: "error", text: t("local.notSaved") });
      // Station's line of this computer's load follows.
      useGlobalAgents.getState().resync();
      return true;
    } catch (error) {
      setNote({ kind: "error", text: localSettingsErrorText(t, error) });
      return false;
    } finally {
      setSaving(false);
    }
  };

  const pickLevel = (kind: string, level: PermissionLevel) => {
    if (level === "full") {
      setWarnFor(kind);
      return;
    }
    void save({ permission: { [kind]: level } });
  };

  return (
    <div className="this-computer">
      {!runner.online && <LocalNotReady starting={false} />}
      {(saving || note) && (
        <p className={`this-computer-saved${note?.kind === "error" ? " is-error" : ""}`} role="status">
          {saving ? t("local.saving") : note?.text}
        </p>
      )}

      <AgentScanStep variant="settings" />

      <section className="this-computer-part" aria-labelledby={`${runner.id}-repos`}>
        <div className="this-computer-head">
          <h3 id={`${runner.id}-repos`}>{t(simple ? "simple.local.folders" : "local.repos")}</h3>
          <button type="button" className="ap-btn" disabled={!runner.online} onClick={() => setAdding(true)}>
            {t(simple ? "simple.local.addFolder" : "repoAdd.button")}
          </button>
        </div>
        {repos.length === 0 ? (
          <p className="this-computer-empty">{t(simple ? "simple.local.foldersNone" : "local.reposNone")}</p>
        ) : (
          <ul className="this-computer-repos">
            {repos.map((repo) => (
              <RepoRow key={repo.id} runner={runner} repo={repo} onNote={setNote} testCommand={!simple} />
            ))}
          </ul>
        )}
      </section>

      <section className="this-computer-part" aria-labelledby={`${runner.id}-concurrency`}>
        <div className="this-computer-head">
          <h3 id={`${runner.id}-concurrency`}>{t("local.concurrency")}</h3>
          <span className="global-capacity-stepper" role="group" aria-label={t("local.concurrency")}>
            <button
              type="button"
              className="ap-btn global-capacity-step"
              aria-label={t("local.fewer")}
              disabled={locked || max <= 1}
              onClick={() => void save({ maxConcurrent: max - 1 })}
            >
              −
            </button>
            <output className="global-capacity-value" aria-label={t("local.concurrencyValue", { count: max })}>
              {max}
            </output>
            <button
              type="button"
              className="ap-btn global-capacity-step"
              aria-label={t("local.more")}
              title={max >= ceiling ? t("local.concurrencyCeiling", { max: ceiling }) : undefined}
              disabled={locked || max >= ceiling}
              onClick={() => void save({ maxConcurrent: max + 1 })}
            >
              +
            </button>
          </span>
        </div>
      </section>

      {installed.length > 0 && (
        <section className="this-computer-part" aria-labelledby={`${runner.id}-permission`}>
          <h3 id={`${runner.id}-permission`}>{t("local.permission.section")}</h3>
          {installed.map((agent) => (
            <PermissionPicker
              key={agent.kind}
              runner={runner}
              kind={agent.kind}
              locked={locked}
              askFirst={!shellFree.has(agent.kind)}
              onPick={(level) => pickLevel(agent.kind, level)}
              onAskFirst={(askFirst) => void save({ shellWithoutAsking: { [agent.kind]: !askFirst } })}
            />
          ))}
        </section>
      )}

      <section className="this-computer-part">
        <label className="route-toggle this-computer-ask">
          <input
            type="checkbox"
            checked={Boolean(local?.askEveryRun)}
            disabled={locked}
            onChange={(event) => void save({ askEveryRun: event.target.checked })}
          />
          <span>{t("local.askEveryRun")}</span>
        </label>
        <small className="run-hint">{t("local.askEveryRunHelp")}</small>
      </section>

      <section className="this-computer-part">
        <label className="route-toggle this-computer-ask">
          <input
            type="checkbox"
            checked={Boolean(info?.permissionRelay)}
            disabled={locked}
            onChange={(event) => void save({ permissionRelay: event.target.checked })}
          />
          <span>{t("local.permissionRelay")}</span>
        </label>
        <small className="run-hint">{t("local.permissionRelayHelp")}</small>
      </section>

      <details className="run-advanced this-computer-advanced">
        <summary>{t("local.advanced")}</summary>
        {/* Simple keeps finished work on this computer: no online review
            to switch on here (Settings / General brings every option back). */}
        {local && !simple && (
          <div className="this-computer-pr">
            <label className="route-toggle">
              <input
                type="checkbox"
                checked={local.autoPR}
                disabled={locked}
                onChange={(event) => void save({ autoPR: event.target.checked })}
              />
              <span>{t("local.autoPR")}</span>
            </label>
            <label className="route-toggle">
              <input
                type="checkbox"
                checked={local.prDraft}
                disabled={locked || !local.autoPR}
                onChange={(event) => void save({ prDraft: event.target.checked })}
              />
              <span>{t("local.prDraft")}</span>
            </label>
          </div>
        )}
        {local?.configPath && (
          <p className="run-hint">
            {t("local.settingsFile")} <code>{local.configPath}</code>
          </p>
        )}
        <p className="run-hint">{t("local.cliHint")}</p>
        <pre className="this-computer-cli">{localCommands(runner).join("\n")}</pre>
      </details>

      {adding && (
        <AddRepoDialog
          runnerId={runner.id}
          plain={simple}
          onClose={() => setAdding(false)}
          onAdded={(repo) => {
            setAdding(false);
            setNote({ kind: "saved", text: t("repoAdd.added", { name: repo.name }) });
          }}
        />
      )}
      {warnFor && (
        <FullAccessDialog
          agent={agentName(warnFor)}
          simple={simple}
          onCancel={() => setWarnFor("")}
          onConfirm={(askFirst) => {
            const kind = warnFor;
            setWarnFor("");
            void save({ permission: { [kind]: "full" }, shellWithoutAsking: { [kind]: !askFirst } });
          }}
        />
      )}
    </div>
  );
}

function PermissionPicker({
  runner,
  kind,
  locked,
  askFirst,
  onPick,
  onAskFirst,
}: {
  runner: Runner;
  kind: string;
  locked: boolean;
  askFirst: boolean;
  onPick: (level: PermissionLevel) => void;
  onAskFirst: (askFirst: boolean) => void;
}) {
  const { t } = useI18n();
  const name = useId();
  const agent = agentName(kind);
  const level = permissionOf(runner, kind);
  const detail = runner.info?.local?.permissionDetail?.[kind] ?? "";
  return (
    <fieldset className="this-computer-permission">
      <legend>{t("local.permission.title", { agent })}</legend>
      {LEVELS.map((item) => (
        <label key={item} className={`this-computer-level${level === item ? " selected" : ""}`}>
          <input
            type="radio"
            name={name}
            value={item}
            checked={level === item}
            disabled={locked}
            onChange={() => onPick(item)}
          />
          <span>{t(`local.permission.${item}`)}</span>
        </label>
      ))}
      {level === "custom" && (
        <label className="this-computer-level selected">
          <input type="radio" name={name} value="custom" checked readOnly disabled />
          <span>{t("local.permission.custom", { detail: detail || "—" })}</span>
        </label>
      )}
      {level === "full" && (
        <label className="route-toggle this-computer-askfirst">
          <input
            type="checkbox"
            checked={askFirst}
            disabled={locked}
            onChange={(event) => onAskFirst(event.target.checked)}
          />
          <span>{t("local.permission.fullAsk")}</span>
        </label>
      )}
    </fieldset>
  );
}

/** The warning before an agent may run any command, with the one guard
 * that stays: automatic runs still ask (checked unless unticked). */
function FullAccessDialog({
  agent,
  simple,
  onCancel,
  onConfirm,
}: {
  agent: string;
  /** Simple mode: the person's folder, not "the repo". */
  simple: boolean;
  onCancel: () => void;
  onConfirm: (askFirst: boolean) => void;
}) {
  const { t } = useI18n();
  const titleId = useId();
  const [askFirst, setAskFirst] = useState(true);

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return;
      event.stopPropagation();
      onCancel();
    };
    window.addEventListener("keydown", onKey, true);
    return () => window.removeEventListener("keydown", onKey, true);
  }, [onCancel]);

  return (
    <div
      className="confirm-backdrop"
      onMouseDown={(event) => {
        if (event.target === event.currentTarget) onCancel();
      }}
    >
      <section className="confirm-dialog danger" role="alertdialog" aria-modal="true" aria-labelledby={titleId}>
        <header>
          <div>
            <h2 id={titleId}>{t("local.permission.warnTitle", { agent })}</h2>
            <p>{t(simple ? "simple.local.fullWarn" : "local.permission.fullWarn", { agent })}</p>
          </div>
        </header>
        <div className="this-computer-warn-body">
          <label className="route-toggle">
            <input type="checkbox" checked={askFirst} onChange={(event) => setAskFirst(event.target.checked)} />
            <span>{t("local.permission.fullAsk")}</span>
          </label>
        </div>
        <footer>
          <button type="button" onClick={onCancel}>
            {t("common.cancel")}
          </button>
          <button type="button" className="danger" onClick={() => onConfirm(askFirst)}>
            {t("local.permission.confirm")}
          </button>
        </footer>
      </section>
    </div>
  );
}

function RepoRow({
  runner,
  repo,
  onNote,
  testCommand,
}: {
  runner: Runner;
  repo: RunnerRepo;
  onNote: (note: Note) => void;
  /** Whether the test command field shows: not in Simple, where nothing is
   * checked before work starts and no command is typed. */
  testCommand: boolean;
}) {
  const { t } = useI18n();
  const fieldId = useId();
  const [command, setCommand] = useState("");
  const [busy, setBusy] = useState(false);

  const setTest = async (value: string) => {
    if (busy) return;
    setBusy(true);
    try {
      const res = await api.setRepoTestCommand(runner.id, repo.id, value);
      holdRunner(res.runner);
      setCommand("");
      onNote({ kind: "saved", text: t(value ? "repoAdd.testSaved" : "repoAdd.testCleared", { name: repo.name }) });
    } catch (error) {
      onNote({ kind: "error", text: repoErrorText(t, error, { name: repo.name }) });
    } finally {
      setBusy(false);
    }
  };

  const remove = async () => {
    const ok = await confirmAction({
      title: t("repoAdd.removeTitle", { name: repo.name }),
      description: t("repoAdd.removeConfirm", { name: repo.name }),
      confirmLabel: t("repoAdd.remove"),
      tone: "danger",
    });
    if (!ok || busy) return;
    setBusy(true);
    try {
      const res = await api.removeRepo(runner.id, repo.id);
      holdRunner(res.runner);
      onNote({ kind: "saved", text: t("repoAdd.removed", { name: repo.name }) });
    } catch (error) {
      onNote({ kind: "error", text: repoErrorText(t, error, { name: repo.name }) });
    } finally {
      setBusy(false);
    }
  };

  const value = command.trim();
  return (
    <li className="this-computer-repo">
      <div className="this-computer-repo-head">
        <strong>{repo.name}</strong>
        {repo.path && <span className="mono">{repo.path}</span>}
        <button
          type="button"
          className="ap-btn is-danger"
          disabled={busy || !runner.online}
          onClick={() => void remove()}
        >
          {t("repoAdd.remove")}
        </button>
      </div>
      {testCommand && (
        <div className="this-computer-test">
          <label htmlFor={fieldId}>{t("repoAdd.testCommand")}</label>
          <span className="this-computer-test-row">
            <input
              id={fieldId}
              value={command}
              maxLength={500}
              spellCheck={false}
              autoCapitalize="off"
              autoCorrect="off"
              placeholder={repo.verify ? t("repoAdd.testCommandSet") : t("repoAdd.testPlaceholder")}
              disabled={busy || !runner.online}
              onChange={(event) => setCommand(event.target.value)}
              onKeyDown={(event) => {
                if (event.key === "Enter" && value) {
                  event.preventDefault();
                  void setTest(value);
                }
              }}
              aria-describedby={`${fieldId}-help`}
            />
            <button
              type="button"
              className="ap-btn"
              disabled={busy || !value || !runner.online}
              onClick={() => void setTest(value)}
            >
              {t("common.save")}
            </button>
            {repo.verify && (
              <button type="button" className="ap-btn" disabled={busy || !runner.online} onClick={() => void setTest("")}>
                {t("repoAdd.testClear")}
              </button>
            )}
          </span>
          <small id={`${fieldId}-help`} className="run-hint">
            {t("repoAdd.testCommandHelp")}
          </small>
        </div>
      )}
    </li>
  );
}
