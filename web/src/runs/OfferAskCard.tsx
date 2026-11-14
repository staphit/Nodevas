// "Start this run?": the built-in runner asks its owner in the app before it
// starts a run (single-user edition): an automatic run of an agent set to
// run any command, or every run when "ask me before every run" is on. On a
// team hub the same reasons read as "waiting for <owner> on <PC>", with no
// buttons: approvals there stay on the member's own PC.

import { useState } from "react";

import { api, RequestError, type Run } from "../api";
import { useViewer } from "../components/SignIn";
import { agentName } from "../game/labels";
import { formatLocalizedTime, useI18n } from "../i18n";
import { useRunners } from "../runners/runnerStore";
import { openSettings } from "../settings/openSettings";
import { useApp } from "../store";
import { askMachine, askReasonFix, askReasonText, shellAgentOf } from "./offerReasons";
import { useRuns } from "./runStore";

type Translate = ReturnType<typeof useI18n>["t"];

/** Whether the run waits for an answer: offered, with an ask. */
export function runAsks(run: Pick<Run, "status" | "ask"> | null | undefined): boolean {
  return Boolean(run && run.status === "offered" && run.ask);
}

/** Whether the run waits for an answer in this app (the built-in runner's). */
export function runAsksInApp(run: Pick<Run, "status" | "ask"> | null | undefined): boolean {
  return runAsks(run) && run?.ask?.where === "app";
}

/** "Codex in my-app: “Fix the typo”": the agent, the repo and the task. */
export function askSubject(t: Translate, run: Run, task: string): string {
  const values = { agent: agentName(run.agent), repo: run.repoName || run.repoId };
  return task ? t("offerAsk.task", { ...values, task }) : t("offerAsk.taskNoTitle", values);
}

/** A refused answer in the reader's words. */
export function answerErrorText(t: Translate, error: unknown, machine: string): string {
  if (error instanceof RequestError) {
    if (error.code === "not_waiting") return t("offerAsk.error.not_waiting");
    if (error.code === "approve_on_runner") return t("offerAsk.error.approve_on_runner", { machine });
    if (error.status === 503) return t("local.unavailable");
  }
  return error instanceof Error ? error.message : String(error);
}

export function OfferAskCard({ run, className }: { run: Run; className?: string }) {
  // Most rows ask nothing: they render no card and read nothing for one.
  if (!runAsks(run)) return null;
  return <OfferAskBody run={run} className={className} />;
}

function OfferAskBody({ run, className }: { run: Run; className?: string }) {
  const { t, language } = useI18n();
  const viewer = useViewer();
  const runner = useRunners((s) => s.runners.find((item) => item.id === run.runnerId));
  const nodeTitle = useApp((s) =>
    run.global ? "" : (s.graph?.nodes?.find((node) => node.id === run.nodeId)?.title ?? ""),
  );
  const [always, setAlways] = useState(false);
  const [busy, setBusy] = useState<"start" | "decline" | null>(null);
  const [error, setError] = useState("");

  if (!runAsks(run) || !run.ask) return null;
  const ask = run.ask;
  const machine = askMachine(t, run);
  const task = run.global ? run.title || "" : nodeTitle;
  const subject = askSubject(t, run, task);
  const reasons = (ask.reasons ?? [])
    .map((reason) => ({ reason, text: askReasonText(t, reason, run), fix: askReasonFix(reason) }))
    .filter((item) => item.text);
  const inApp = ask.where === "app";
  // Owner only: a server without accounts has one person, the owner.
  const canAnswer = inApp && (!viewer || runner?.ownerId === viewer.id);
  const shellAgent = shellAgentOf(ask.reasons, run.agent);
  const expires = ask.expiresAt ? formatLocalizedTime(ask.expiresAt, language) : "";

  const answer = async (start: boolean) => {
    if (busy) return;
    setBusy(start ? "start" : "decline");
    setError("");
    try {
      const updated = await api.answerOffer(run.id, {
        start,
        ...(start && always && shellAgent ? { alwaysAllowShell: true } : {}),
      });
      if (updated) useRuns.getState().putRun(updated);
      else void useRuns.getState().refreshRun(run.id);
    } catch (caught) {
      setError(answerErrorText(t, caught, machine));
      // Answered elsewhere, or it ran out: the run's own state says which.
      void useRuns.getState().refreshRun(run.id);
    } finally {
      setBusy(null);
    }
  };

  const fixLabel = (fix: string) =>
    fix === "permission"
      ? t("offerAsk.fixPermission", { agent: agentName(shellAgent || run.agent) })
      : t("offerAsk.fixAskEveryRun");

  if (!canAnswer) {
    const owner = runner?.ownerName ?? "";
    return (
      <section
        className={`offer-ask is-remote${className ? ` ${className}` : ""}`}
        aria-label={t("offerAsk.title")}
        data-testid="offer-ask"
      >
        <p className="offer-ask-title">
          {owner ? t("offerAsk.waitingRemote", { owner, machine }) : t("offerAsk.waitingOn", { machine })}
        </p>
        <p className="offer-ask-subject">{subject}</p>
        {reasons.length > 0 && (
          <ul className="offer-ask-reasons">
            {reasons.map((item, index) => (
              <li key={index}>{item.text}</li>
            ))}
          </ul>
        )}
      </section>
    );
  }

  return (
    <section
      className={`offer-ask${className ? ` ${className}` : ""}`}
      role="group"
      aria-label={t("offerAsk.title")}
      data-testid="offer-ask"
    >
      <p className="offer-ask-title">{t("offerAsk.title")}</p>
      <p className="offer-ask-subject">{subject}</p>
      {reasons.length > 0 && (
        <details className="offer-ask-why">
          <summary>{t("offerAsk.why")}</summary>
          <ul className="offer-ask-reasons">
            {reasons.map((item, index) => (
              <li key={index}>
                {item.text}
                {item.fix && (
                  <>
                    {" "}
                    <button type="button" className="run-inline-link" onClick={() => openSettings("runners")}>
                      {fixLabel(item.fix)}
                    </button>
                  </>
                )}
              </li>
            ))}
          </ul>
        </details>
      )}
      {shellAgent && (
        <label className="route-toggle offer-ask-always">
          <input
            type="checkbox"
            checked={always}
            disabled={Boolean(busy)}
            onChange={(event) => setAlways(event.target.checked)}
          />
          <span>{t("offerAsk.alwaysAllow", { agent: agentName(shellAgent) })}</span>
        </label>
      )}
      <div className="offer-ask-foot">
        {expires && <span className="offer-ask-expires">{t("offerAsk.expires", { time: expires })}</span>}
        <span className="offer-ask-buttons">
          <button type="button" className="ap-btn" disabled={Boolean(busy)} onClick={() => void answer(false)}>
            {busy === "decline" ? t("offerAsk.sending") : t("offerAsk.decline")}
          </button>
          <button
            type="button"
            className="ap-btn is-primary"
            disabled={Boolean(busy)}
            onClick={() => void answer(true)}
          >
            {busy === "start" ? t("offerAsk.sending") : t("offerAsk.start")}
          </button>
        </span>
      </div>
      {error && (
        <p className="notify-message error offer-ask-error" role="alert">
          {error}
        </p>
      )}
    </section>
  );
}
