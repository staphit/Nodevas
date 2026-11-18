// Simple mode's run view (single-user edition): the Log and Changes tabs
// open on what happened in plain words (how it ended, how many files it
// changed, what the agent said last) instead of the raw log (worktree,
// branch, tool calls, token counts) or the diff. Those stay one press away,
// behind "Show the full record". Full mode never draws this.

import { useEffect, useId, useMemo, useRef, useState, type ReactNode } from "react";

import { runFinal, type Run, type RunEvent } from "../api";
import { useI18n } from "../i18n";
import { useViewer } from "../components/SignIn";
import { useLocalRunner } from "../runners/edition";
import { useRunners } from "../runners/runnerStore";
import { resultState } from "./resultState";
import { paragraphs } from "./runFormat";
import { useRuns } from "./runStore";
import { runSignedOut, signInReader, signedOutText, type SignInReader } from "./signedOut";

type Translate = ReturnType<typeof useI18n>["t"];

/** How the run stands, in the describe box's words. `plain` is whether the
 * result buttons ("Take a look", "Apply to my folder") are on screen;
 * `reader` is who would sign a signed-out agent in (signedOut.ts). */
export function plainRunState(t: Translate, run: Run, plain: boolean, reader: SignInReader = "local"): string {
  // Its agent is here but signed out: how to sign in, as the box says it.
  if (runSignedOut(run)) return signedOutText(t, run, reader);
  switch (run.status) {
    case "succeeded":
      if (run.workspace === "merged_local") return t("simple.box.applied");
      if (!run.commit) return t("simple.box.noChange");
      return t(plain ? "simple.box.done" : "simple.summary.done");
    case "failed":
      return t("simple.box.failed");
    case "cancelled":
      return t("simple.box.stopped");
    case "declined":
      return t("simple.box.declined");
    case "offered":
      return t("simple.summary.waiting");
    default:
      return t("simple.summary.working");
  }
}

/** The agent's last message: what it says it did. */
export function lastSaid(events: readonly RunEvent[] | undefined): string {
  if (!events) return "";
  for (let i = events.length - 1; i >= 0; i--) {
    const event = events[i];
    if (event.type === "text" && event.text?.trim()) return event.text.trim();
  }
  return "";
}

export function SimpleRunSummary({ run, children }: { run: Run; children: ReactNode }) {
  const { t } = useI18n();
  const localRunnerId = useLocalRunner()?.id;
  const runners = useRunners((s) => s.runners);
  const viewer = useViewer();
  const events = useRuns((s) => s.eventsByRun[run.id]);
  const watchRun = useRuns((s) => s.watchRun);
  const loadEvents = useRuns((s) => s.loadEvents);
  const [open, setOpen] = useState(false);
  const detailsId = useId();

  useEffect(() => watchRun(run.id), [run.id, watchRun]);
  // The agent's last words can land after the status flips: one more
  // fetch on the flip, as the full log does.
  const final = runFinal(run.status);
  const wasFinal = useRef(final);
  useEffect(() => {
    if (final && !wasFinal.current) void loadEvents(run.id);
    wasFinal.current = final;
  }, [final, run.id, loadEvents]);

  const said = useMemo(() => lastSaid(events), [events]);
  const files = run.status === "succeeded" && run.commit ? (run.filesChanged ?? 0) : 0;
  const plain = resultState(run, localRunnerId) !== "none";

  return (
    <>
      <section className="simple-run-summary" aria-label={t("simple.summary.label")}>
        <p className="simple-run-state">{plainRunState(t, run, plain, signInReader(run, runners, viewer))}</p>
        {files > 0 && (
          <p className="simple-run-files">
            {files === 1 ? t("simple.summary.filesOne") : t("simple.summary.files", { count: files })}
          </p>
        )}
        {said && (
          <div className="simple-run-said">
            <h3>{t("simple.summary.said")}</h3>
            {paragraphs(said).map((part, index) => (
              <p key={index}>{part}</p>
            ))}
          </div>
        )}
        <button
          type="button"
          className="run-link-button simple-run-toggle"
          aria-expanded={open}
          aria-controls={detailsId}
          onClick={() => setOpen((was) => !was)}
        >
          {t(open ? "simple.summary.hideDetails" : "simple.summary.showDetails")}
        </button>
      </section>
      <div id={detailsId} className="simple-run-details" hidden={!open}>
        {open && children}
      </div>
    </>
  );
}
