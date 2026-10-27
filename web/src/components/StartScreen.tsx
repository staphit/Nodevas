/**
 * The start screen: what a brand-new workspace opens on.
 *
 * A server given an empty workspace creates one blank project ("main") so
 * there is always something to open, which means "no projects" never reaches
 * the browser. A new workspace looks like this instead: exactly one project,
 * with no nodes. That is when this screen offers the two ways to begin — the
 * blank project that is already there, or a copy of one of the examples the
 * binary carries (repository demo/, served by POST /api/projects/example).
 *
 * It asks once (`startScreenDismissed`), and can be brought back from the
 * explorer's tools or the command palette for the examples.
 *
 * The first-run tour waits for it: two dialogs on a first launch is one too
 * many, so OnboardingTour holds its invitation while this is open.
 */

import { useCallback, useEffect, useRef, useState } from "react";
import { create } from "zustand";

import { useI18n } from "../i18n";
import { IconClose, IconPlus } from "../icons";
import { reportError, useApp, usePreference } from "../store";
import { useViewer } from "./SignIn";

/** Window event that opens the screen on demand (explorer tools, palette). */
export const OPEN_START_SCREEN_EVENT = "nodevas-open-start-screen";

/** The examples in repository demo/, in the order they are offered. */
export const EXAMPLE_PROJECTS = [
  "daily-work-schedule",
  "novel-writing",
  "ai-harness-engineering",
] as const;

/** Whether the screen was asked for, as opposed to shown because the
 * workspace is new. Shared so the tour can wait for either. */
const useStartScreenRequest = create<{ requested: boolean; set: (requested: boolean) => void }>(
  (set) => ({ requested: false, set: (requested) => set({ requested }) }),
);

/**
 * A new project asked for from here. A flag rather than an event: the explorer
 * may be folded, and only mounts once this screen has unfolded it.
 */
export const useNewProjectRequest = create<{ pending: boolean; set: (pending: boolean) => void }>(
  (set) => ({ pending: false, set: (pending) => set({ pending }) }),
);

/** A workspace that is exactly one project with nothing in it. */
function useFreshWorkspace(): boolean {
  const graph = useApp((s) => s.graph);
  const projects = useApp((s) => s.projects);
  const viewer = useViewer();
  if (!graph) return false;
  // Creating projects is an administrator's call on a team server.
  if (viewer && viewer.role !== "admin") return false;
  const openable = projects.filter((project) => !project.isFolder);
  return (
    openable.length === 1 &&
    openable[0].nodes === 0 &&
    (graph.nodes?.length ?? 0) === 0
  );
}

/** Whether the start screen is on screen now. */
export function useStartScreenVisible(): boolean {
  const dismissed = usePreference("startScreenDismissed");
  const requested = useStartScreenRequest((s) => s.requested);
  const fresh = useFreshWorkspace();
  return requested || (fresh && !dismissed);
}

export function StartScreen() {
  const { t } = useI18n();
  const visible = useStartScreenVisible();
  const setRequested = useStartScreenRequest((s) => s.set);
  const fresh = useFreshWorkspace();
  const updateUIPreference = useApp((s) => s.updateUIPreference);
  const openExampleProject = useApp((s) => s.openExample);
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const dialogRef = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    const onOpen = () => setRequested(true);
    window.addEventListener(OPEN_START_SCREEN_EVENT, onOpen);
    return () => window.removeEventListener(OPEN_START_SCREEN_EVENT, onOpen);
  }, [setRequested]);

  const close = useCallback(() => {
    setRequested(false);
    setError(null);
    updateUIPreference("startScreenDismissed", true);
  }, [setRequested, updateUIPreference]);

  useEffect(() => {
    if (!visible) return;
    dialogRef.current?.querySelector<HTMLButtonElement>("button.start-screen-blank")?.focus();
    const onKey = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return;
      event.preventDefault();
      close();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [visible, close]);

  if (!visible) return null;

  // In a new workspace the blank project is already open, so starting blank
  // is just getting out of the way. Asked for from a workspace with work in
  // it, it means a new project, which the explorer knows how to start.
  const startBlank = () => {
    const needsNewProject = !fresh;
    close();
    if (needsNewProject) {
      useNewProjectRequest.getState().set(true);
      updateUIPreference("explorerCollapsed", false);
    }
  };

  const openExample = async (example: string) => {
    if (busy) return;
    setBusy(example);
    setError(null);
    try {
      await openExampleProject(example);
      close();
    } catch (failure) {
      setError(t("start.openFailed", { error: (failure as Error).message }));
      reportError(failure);
    } finally {
      setBusy(null);
    }
  };

  return (
    <div className="start-screen-backdrop">
      <div
        ref={dialogRef}
        className="start-screen"
        role="dialog"
        aria-modal="true"
        aria-labelledby="start-screen-title"
      >
        <button
          type="button"
          className="start-screen-close"
          onClick={close}
          aria-label={t("start.close")}
          title={t("start.close")}
        >
          <IconClose size={14} />
        </button>
        <h2 id="start-screen-title">{t("start.title")}</h2>
        <p className="start-screen-lead">{t("start.lead")}</p>
        <button type="button" className="start-screen-blank" onClick={startBlank}>
          <IconPlus size={14} />
          <span>
            <b>{t("start.blank")}</b>
            <small>{t("start.blankHint")}</small>
          </span>
        </button>
        <h3>{t("start.examplesHeading")}</h3>
        <ul className="start-screen-examples">
          {EXAMPLE_PROJECTS.map((example) => (
            <li key={example}>
              <button
                type="button"
                disabled={busy !== null}
                onClick={() => void openExample(example)}
              >
                <b>{t(`start.example.${example}.title`)}</b>
                <small>
                  {busy === example ? t("start.opening") : t(`start.example.${example}.description`)}
                </small>
              </button>
            </li>
          ))}
        </ul>
        <p className="start-screen-note">{t("start.examplesNote")}</p>
        {error && (
          <div className="notify-message error" role="alert">
            {error}
          </div>
        )}
      </div>
    </div>
  );
}
