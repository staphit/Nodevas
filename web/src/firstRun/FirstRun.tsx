/**
 * The first-run questions (single-user edition): two taps that set Nodevas up
 * for what this person came to do, then the agents on this computer.
 *
 * 1. "What would you like to do here?" Plan my own work, have AI build things,
 *    or both. Plan ends here: the workstation stays out of sight.
 * 2. "How do you want to work with the AI?" Keep it simple or show everything.
 *    One tap writes the answers as the settings they mean (the workstation
 *    on, `experience`, `firstRun` done), once: nothing is written per tap,
 *    so someone who closes the app halfway still looks new next time.
 * 3. The agent scan, then where the answers lead: Make opens the global
 *    agents in Station, Both stays on the board with the start screen.
 *
 * It asks only someone who looks new (gate.ts), and only once the board, the
 * project list and the account's choices are in, so an empty cache never
 * makes an existing person look new; if the choices cannot be loaded within
 * the wait it asks nothing and writes nothing. Someone who already used the
 * app gets `firstRun = "existing"` and nothing else. Settings can ask again
 * (OPEN_FIRST_RUN_EVENT): the same questions, without the agent step, and
 * nobody is moved anywhere.
 *
 * It mounts where the start screen did and renders it once done. A team hub
 * renders the start screen only: it never asks and never writes.
 */

import { useEffect, useId, useLayoutEffect, useRef, useState } from "react";

import { api } from "../api";
import { StartScreen, useFreshWorkspace } from "../components/StartScreen";
import { useI18n } from "../i18n";
import { AgentScanStep } from "../local/AgentScanStep";
import { firstPreferenceLoad } from "../preferences/sync";
import { useEdition } from "../runners/edition";
import { runnerInUse, useWorkstationInUse } from "../runners/workstation";
import { setExperience } from "../simple/simpleMode";
import { openGlobalAgents } from "../station/openGlobalAgents";
import { useApp, usePreference } from "../store";
import { looksNew, OPEN_FIRST_RUN_EVENT, setFirstRunHolding } from "./gate";

export { OPEN_FIRST_RUN_EVENT, useFirstRunHolding, looksNew } from "./gate";

/** How long it waits for the board, the projects and the account's choices
 * before it gives up for this time. */
export const FIRST_RUN_WAIT_MS = 4_000;

/** How long "Saved" stays after the questions were answered again. */
const SAVED_NOTICE_MS = 5_000;

type Use = "plan" | "make" | "both";
type Step = "use" | "level" | "agents";
/** Deciding whether to ask, asking, or out of the way. */
type Phase = "deciding" | "asking" | "closed";

export function FirstRun() {
  const single = useEdition() === "single";
  return single ? <FirstRunSingle /> : <StartScreen />;
}

function FirstRunSingle() {
  const { t } = useI18n();
  const titleId = useId();
  const questionId = useId();
  const updateUIPreference = useApp((s) => s.updateUIPreference);
  const firstRun = usePreference("firstRun");
  const graphLoaded = useApp((s) => s.graph !== null);
  const projectsLoaded = useApp((s) => s.projects.length > 0);
  const fresh = useFreshWorkspace();
  const inUse = useWorkstationInUse();

  // The cached answer is enough to stay out of the way at once.
  const [phase, setPhase] = useState<Phase>(() => (firstRun === "pending" ? "deciding" : "closed"));
  const [prefsLoaded, setPrefsLoaded] = useState(false);
  const [again, setAgain] = useState(false);
  const [step, setStep] = useState<Step>("use");
  const [use, setUse] = useState<Use | null>(null);
  const [ready, setReady] = useState(false);
  const [saved, setSaved] = useState(false);
  const dialogRef = useRef<HTMLDivElement | null>(null);

  // The start screen and the tour's invitation wait while this decides or
  // asks. A layout effect, so it holds before either one's effects run.
  useLayoutEffect(() => setFirstRunHolding(phase !== "closed"), [phase]);
  useLayoutEffect(() => () => setFirstRunHolding(false), []);

  // The account's choices first, within the wait; a failed load asks nothing.
  useEffect(() => {
    if (phase !== "deciding") return;
    let live = true;
    const cap = window.setTimeout(() => {
      if (live) setPhase("closed");
    }, FIRST_RUN_WAIT_MS);
    void firstPreferenceLoad().then((loaded) => {
      if (!live) return;
      if (loaded) setPrefsLoaded(true);
      else setPhase("closed");
    });
    return () => {
      live = false;
      window.clearTimeout(cap);
    };
  }, [phase]);

  // The runner list too (the store loads it only when something asks): a
  // built-in runner that already has a folder (serve --repo) is a sign of an
  // existing person, and must not turn the agents on after the questions
  // were answered with Plan. A failed probe counts as no runner in use.
  const [runnersInUse, setRunnersInUse] = useState<boolean | null>(null);
  useEffect(() => {
    if (phase !== "deciding") return;
    let live = true;
    api.listRunners().then(
      (response) => {
        if (live) setRunnersInUse((response.runners ?? []).some(runnerInUse));
      },
      () => {
        if (live) setRunnersInUse(false);
      },
    );
    return () => {
      live = false;
    };
  }, [phase]);

  // Then, with the board, the project list and the runners in, ask or step
  // aside.
  useEffect(() => {
    if (phase !== "deciding" || !prefsLoaded || !graphLoaded || !projectsLoaded || runnersInUse === null) return;
    const prefs = useApp.getState().preferences;
    // Answered on another device: the server's copy just said so.
    if (prefs.firstRun !== "pending") {
      setPhase("closed");
      return;
    }
    const asking = looksNew({
      freshWorkspace: fresh,
      startScreenDismissed: prefs.startScreenDismissed,
      tourPrompt: prefs.tourPrompt,
      tourChaptersDone: prefs.tourChaptersDone,
      agentWorkstation: prefs.agentWorkstation,
      graphConnectTipDismissed: prefs.graphConnectTipDismissed,
      workstationInUse: inUse || runnersInUse,
      experience: prefs.experience,
    });
    if (!asking) updateUIPreference("firstRun", "existing");
    setPhase(asking ? "asking" : "closed");
  }, [phase, prefsLoaded, graphLoaded, projectsLoaded, runnersInUse, fresh, inUse, updateUIPreference]);

  // Settings asks again.
  useEffect(() => {
    if (phase !== "closed") return;
    const onOpen = () => {
      setAgain(true);
      setStep("use");
      setUse(null);
      setSaved(false);
      setPhase("asking");
    };
    window.addEventListener(OPEN_FIRST_RUN_EVENT, onOpen);
    return () => window.removeEventListener(OPEN_FIRST_RUN_EVENT, onOpen);
  }, [phase]);

  useEffect(() => {
    if (!saved) return;
    const timer = window.setTimeout(() => setSaved(false), SAVED_NOTICE_MS);
    return () => window.clearTimeout(timer);
  }, [saved]);

  /** The questions are over: asked again, they say they saved. */
  const done = () => {
    setPhase("closed");
    if (again) setSaved(true);
  };

  const choosePlan = () => {
    updateUIPreference("agentWorkstation", false);
    updateUIPreference("firstRun", "done");
    done();
  };

  const chooseUse = (next: "make" | "both") => {
    setUse(next);
    setStep("level");
  };

  const chooseLevel = (level: "simple" | "full") => {
    updateUIPreference("agentWorkstation", true);
    setExperience(level);
    updateUIPreference("firstRun", "done");
    if (again) done();
    else setStep("agents");
  };

  /** Continue or Do this later: where the answers lead. */
  const leaveAgents = () => {
    if (use === "make") {
      updateUIPreference("startScreenDismissed", true);
      openGlobalAgents();
    }
    done();
  };

  /** Keeps what was already answered; everything else stays as it is today. */
  const skip = () => {
    if (again) {
      setPhase("closed");
      return;
    }
    if (use) updateUIPreference("agentWorkstation", true);
    updateUIPreference("firstRun", "skipped");
    setPhase("closed");
  };

  const back = () => {
    setUse(null);
    setStep("use");
  };

  const asking = phase === "asking";
  const latest = useRef({ skip, leaveAgents, step });
  latest.current = { skip, leaveAgents, step };

  // Escape skips the questions, or on the agent step means "Do this later".
  // A dialog opened over this one (adding an agent) takes Escape first.
  useEffect(() => {
    if (!asking) return;
    const onKey = (event: KeyboardEvent) => {
      if (event.key !== "Escape" || event.defaultPrevented) return;
      event.preventDefault();
      const current = latest.current;
      if (current.step === "agents") current.leaveAgents();
      else current.skip();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [asking]);

  // Each screen starts at its first choice (or its one button).
  useEffect(() => {
    if (!asking) return;
    dialogRef.current
      ?.querySelector<HTMLButtonElement>(".first-run-choice:not(:disabled), .first-run-next")
      ?.focus();
  }, [asking, step]);

  if (phase === "deciding") return null;
  if (phase === "closed") {
    return (
      <>
        <StartScreen />
        {saved && (
          <p className="first-run-saved" role="status">
            {t("firstRun.saved")}
          </p>
        )}
      </>
    );
  }

  const stepNumber = step === "use" ? 1 : step === "level" ? 2 : 3;
  // A choice that cannot be taken says why, right under it.
  const choice = (key: string, onClick: () => void, why = "") => (
    <>
      <button
        type="button"
        className="first-run-choice"
        disabled={Boolean(why)}
        aria-describedby={why ? `${questionId}-${key}-why` : undefined}
        onClick={onClick}
      >
        <b>{t(key)}</b>
        <small>{t(`${key}Hint`)}</small>
      </button>
      {why && (
        <p id={`${questionId}-${key}-why`} className="first-run-why">
          {why}
        </p>
      )}
    </>
  );

  return (
    <div className="start-screen-backdrop">
      <div
        ref={dialogRef}
        className="start-screen first-run"
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
      >
        <p className="first-run-step">{t("firstRun.step", { n: String(stepNumber) })}</p>
        <h2 id={titleId}>{step === "agents" ? t("local.scan.title") : t("firstRun.title")}</h2>
        <p className="start-screen-lead">{step === "agents" ? t("local.scan.lead") : t("firstRun.lead")}</p>
        {step === "use" && (
          <>
            <h3 id={questionId} className="first-run-question">
              {t("firstRun.use.question")}
            </h3>
            <div className="first-run-choices" role="group" aria-labelledby={questionId}>
              {/* Hiding the workstation means nothing while it is in use:
                  it would come straight back. */}
              {/* Agents in use (a runner's hello may land while asking) are
                  turned back on at once (useWorkstationAutoEnable): Plan
                  could not keep them hidden. */}
              {choice("firstRun.use.plan", choosePlan, inUse ? t("firstRun.use.planInUse") : "")}
              {choice("firstRun.use.make", () => chooseUse("make"))}
              {choice("firstRun.use.both", () => chooseUse("both"))}
            </div>
          </>
        )}
        {step === "level" && (
          <>
            <h3 id={questionId} className="first-run-question">
              {t("firstRun.level.question")}
            </h3>
            <div className="first-run-choices" role="group" aria-labelledby={questionId}>
              {choice("firstRun.level.simple", () => chooseLevel("simple"))}
              {choice("firstRun.level.full", () => chooseLevel("full"))}
            </div>
          </>
        )}
        {step === "agents" && <AgentScanStep variant="step" onReadyChange={(next) => setReady(next)} />}
        <div className="first-run-actions">
          {step === "level" && (
            <button type="button" onClick={back}>
              {t("firstRun.back")}
            </button>
          )}
          <span className="first-run-spacer" />
          {step === "agents" ? (
            <button type="button" className="first-run-next" onClick={leaveAgents}>
              {ready ? t("firstRun.agents.continue") : t("firstRun.agents.later")}
            </button>
          ) : (
            <button type="button" className="first-run-skip" onClick={skip}>
              {t("firstRun.skip")}
            </button>
          )}
        </div>
      </div>
    </div>
  );
}
