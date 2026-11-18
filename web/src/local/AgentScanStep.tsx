// The agents on this computer (single-user edition), for someone setting
// them up: each of the five agents Nodevas knows, whether it is ready (with
// its version), and for one that is missing how to get it (the official page,
// three plain steps) or "I already have it" (show Nodevas the program file).
// "Check again" asks the built-in runner to look again.
//
// Only the body: the first-run questions, the describe box's "Set one up"
// and This computer each put their own frame around it. Names, versions and
// paths are rendered as text. A team hub renders nothing here.

import { useEffect, useRef, useState } from "react";

import { api, type Runner, type RunnerAgent, type RunnerSettingsRequest } from "../api";
import { agentName } from "../game/labels";
import { formatList, useI18n } from "../i18n";
import { useEdition, useLocalRunner, useLocalState } from "../runners/edition";
import { agentProblemPlain, runnerAgentProblem } from "../runners/runnerName";
import { useRunners } from "../runners/runnerStore";
import { signInCommand } from "../runs/signedOut";
import { useGlobalAgents } from "../station/globalAgentsStore";
import { AddAgentDialog, localSettingsErrorText } from "./AddAgentDialog";
import { AGENT_KINDS, EXTERNAL_LINK, installHelp } from "./agentInstallHelp";
import { LocalNotReady } from "./LocalNotReady";
import { holdRunner } from "./useAddRepo";

type Note = { kind: "saved" | "error"; text: string };

/** Every agent the runner reported, then any of the five it did not, as
 * missing; in the runner's order. */
export function scannedAgents(runner: Runner | undefined): RunnerAgent[] {
  const reported = runner?.info?.agents ?? [];
  const known = new Set(reported.map((agent) => agent.kind));
  const missing = AGENT_KINDS.filter((kind) => !known.has(kind)).map((kind) => ({
    kind,
    installed: false,
    models: [],
  }));
  return [...reported, ...missing].sort((a, b) => order(a.kind) - order(b.kind));
}

function order(kind: string): number {
  const at = (AGENT_KINDS as readonly string[]).indexOf(kind);
  return at < 0 ? AGENT_KINDS.length : at;
}

/** The kinds ready to take work. */
export function readyKinds(runner: Runner | undefined): string[] {
  return (runner?.info?.agents ?? []).filter((agent) => agent.installed).map((agent) => agent.kind);
}

export function AgentScanStep({
  variant = "step",
  onReadyChange,
}: {
  /** "step": the bare list (first run, the describe box's dialog);
   * "settings": This computer's section, under its own heading. */
  variant?: "step" | "settings";
  /** Whether at least one agent is ready and which: on mount once the
   * runner is known, then on every change. */
  onReadyChange?: (ready: boolean, kinds: string[]) => void;
}) {
  const single = useEdition() === "single";
  if (!single) return null;
  return <AgentScanBody variant={variant} onReadyChange={onReadyChange} />;
}

function AgentScanBody({
  variant,
  onReadyChange,
}: {
  variant: "step" | "settings";
  onReadyChange?: (ready: boolean, kinds: string[]) => void;
}) {
  const { t } = useI18n();
  const runner = useLocalRunner();
  const state = useLocalState();
  const loaded = useRunners((s) => s.loaded);
  const [saving, setSaving] = useState(false);
  const [note, setNote] = useState<Note | null>(null);
  const [rechecked, setRechecked] = useState(false);
  const [howTo, setHowTo] = useState("");
  const [adding, setAdding] = useState("");

  useEffect(() => {
    if (!useRunners.getState().loaded) void useRunners.getState().refresh();
  }, []);

  // Ready or not, told once per change; nothing until the runner is known.
  const usable = runner?.online ? readyKinds(runner) : [];
  const known = Boolean(runner?.online);
  const readyKey = usable.join(",");
  const told = useRef<string | null>(null);
  const tell = useRef(onReadyChange);
  tell.current = onReadyChange;
  useEffect(() => {
    if (!known && told.current === null) return;
    if (told.current === readyKey) return;
    told.current = readyKey;
    tell.current?.(readyKey !== "", readyKey ? readyKey.split(",") : []);
  }, [known, readyKey]);

  if (!runner || (variant === "step" && !runner.online)) {
    if (!loaded) {
      return (
        <p className="agent-scan-looking" role="status">
          {t("local.scan.looking")}
        </p>
      );
    }
    return <LocalNotReady starting={state === "starting"} />;
  }

  const agents = scannedAgents(runner);
  const paths = runner.info?.local?.agentPaths ?? {};
  const locked = saving || !runner.online;
  const anyMissing = agents.some((agent) => !agent.installed);

  const save = async (body: RunnerSettingsRequest, saved: string) => {
    if (saving) return;
    setSaving(true);
    setNote(null);
    try {
      const res = await api.setRunnerSettings(runner.id, body);
      holdRunner(res.runner);
      setNote(res.persisted ? { kind: "saved", text: saved } : { kind: "error", text: t("local.notSaved") });
      useGlobalAgents.getState().resync();
      if (body.redetect) setRechecked(true);
    } catch (error) {
      setNote({ kind: "error", text: localSettingsErrorText(t, error) });
    } finally {
      setSaving(false);
    }
  };

  const recheck = (
    <button
      type="button"
      className="ap-btn"
      disabled={locked}
      onClick={() => void save({ redetect: true }, t("local.rechecked"))}
    >
      {saving ? t("local.saving") : t("local.recheck")}
    </button>
  );

  const body = (
    <>
      {variant === "step" && (
        <p className={`agent-scan-summary${usable.length > 0 ? " is-ready" : ""}`} role="status">
          {usable.length > 0
            ? t("local.scan.someReady", { agents: formatList(usable.map(agentName)) })
            : t("local.scan.noneReady")}
        </p>
      )}
      <ul className="this-computer-agents agent-scan-list">
        {agents.map((agent) => {
          const name = agentName(agent.kind);
          const help = installHelp(agent.kind);
          const path = paths[agent.kind] ?? "";
          const open = howTo === agent.kind;
          // Found, but it needs something Nodevas says plainly (a setting,
          // another version): nothing to get or show, which would
          // contradict "Found".
          const plain = agent.error ? agentProblemPlain(t, agent.error, name) : "";
          // Here but signed out: signing in is all it needs, and the
          // runner's own sentence about it adds nothing.
          const signIn = agent.signedOut ? t("local.scan.signInHint", { command: signInCommand(agent.kind) }) : "";
          const offerGet = !agent.installed && !plain && !signIn;
          const status = agent.installed
            ? t("local.scan.found")
            : signIn
              ? t("local.scan.signedOut")
              : agent.error
                ? t("local.scan.problem")
                : t("local.scan.missing");
          return (
            <li
              key={agent.kind}
              className={`agent-scan-row ${agent.installed ? "is-installed" : agent.error || signIn ? "is-problem" : "is-missing"}`}
            >
              <strong>{name}</strong>
              <span>
                {status}
                {agent.installed && agent.version ? ` · ${agent.version}` : ""}
              </span>
              {signIn && <small className="this-computer-problem agent-scan-problem">{signIn}</small>}
              {agent.error &&
                !signIn &&
                (plain ? (
                  <div className="this-computer-problem agent-scan-problem">
                    <small>{plain}</small>
                    <details className="agent-scan-details">
                      <summary>{t("result.details")}</summary>
                      <small>{runnerAgentProblem(t, agent.error)}</small>
                    </details>
                  </div>
                ) : (
                  <small className="this-computer-problem">{runnerAgentProblem(t, agent.error)}</small>
                ))}
              {path && (
                <span className="agent-scan-added">
                  <small>
                    {t("local.scan.addedByYou")} <span className="mono">{path}</span>
                  </small>
                  <button
                    type="button"
                    className="run-link-button"
                    disabled={locked}
                    onClick={() => void save({ agentPath: { [agent.kind]: "" } }, t("local.saved"))}
                  >
                    {t("local.scan.forget")}
                  </button>
                </span>
              )}
              {offerGet && (
                <span className="agent-scan-actions">
                  {help && (
                    <button
                      type="button"
                      className="run-link-button"
                      aria-expanded={open}
                      onClick={() => setHowTo(open ? "" : agent.kind)}
                    >
                      {t("local.scan.howToGet", { agent: name })}
                    </button>
                  )}
                  <button type="button" className="run-link-button" disabled={locked} onClick={() => setAdding(agent.kind)}>
                    {t("local.scan.haveIt")}
                  </button>
                </span>
              )}
              {offerGet && open && help && (
                <div className="agent-scan-howto">
                  <ol>
                    <li>{t("local.install.step1", { agent: name })}</li>
                    <li>{t("local.install.step2")}</li>
                    <li>{t("local.install.step3")}</li>
                  </ol>
                  <a className="ap-btn" href={help.url} target={EXTERNAL_LINK.target} rel={EXTERNAL_LINK.rel}>
                    {t("local.install.open")}
                  </a>
                  {help.vendor && <p className="run-hint">{t("local.install.account", { agent: name, vendor: help.vendor })}</p>}
                  <p className="run-hint">{t("local.install.pasteNote")}</p>
                </div>
              )}
            </li>
          );
        })}
      </ul>
      {variant === "step" && <div className="agent-scan-foot">{recheck}</div>}
      {rechecked && anyMissing && <p className="run-hint agent-scan-still">{t("local.scan.stillMissing")}</p>}
      {note && (
        <p className={`this-computer-saved${note.kind === "error" ? " is-error" : ""}`} role={note.kind === "error" ? "alert" : undefined}>
          {note.text}
        </p>
      )}
      {adding && (
        <AddAgentDialog
          runnerId={runner.id}
          kind={adding}
          onClose={() => setAdding("")}
          onAdded={() => {
            const kind = adding;
            setAdding("");
            setNote({ kind: "saved", text: t("local.addAgent.added", { agent: agentName(kind) }) });
          }}
        />
      )}
    </>
  );

  if (variant === "settings") {
    return (
      <section className="this-computer-part agent-scan" aria-labelledby={`${runner.id}-agents`}>
        <div className="this-computer-head">
          <h3 id={`${runner.id}-agents`}>{t("local.agentsFound")}</h3>
          {recheck}
        </div>
        {body}
      </section>
    );
  }
  return <div className="agent-scan">{body}</div>;
}
