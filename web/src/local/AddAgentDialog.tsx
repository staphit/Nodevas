// "I already have it" (single-user edition): the person shows Nodevas the
// agent's program file when it is not found on its own. Starts in their home
// folder and lists only files named like the agent's program (claude,
// claude.exe, claude.cmd…). The built-in runner checks the file (its name,
// where it is kept, that it is a program) and asks it for its version before
// it keeps it; a refusal says why in plain words.

import { useCallback, useEffect, useId, useRef, useState } from "react";
import { createPortal } from "react-dom";

import { api, GlobalRunError, RunnerRequestError, type Runner } from "../api";
import { AGENT_PATH_REFUSALS } from "../api/runners";
import type { DirListing } from "../api/workspace";
import { FolderBrowser } from "../components/FolderBrowser";
import { agentName } from "../game/labels";
import { IconClose, IconComputer } from "../icons";
import { useI18n } from "../i18n";
import { useGlobalAgents } from "../station/globalAgentsStore";
import { agentProgram } from "./agentInstallHelp";
import { holdRunner } from "./useAddRepo";

type Translate = (key: string, values?: Record<string, string | number>) => string;

/** Why a change of this computer's settings was refused, in plain words. */
export function localSettingsErrorText(t: Translate, error: unknown): string {
  if (error instanceof GlobalRunError) {
    if (error.code === "RUNNER_OFFLINE" || error.status === 503 || error.blocked === "local_only") {
      return t("local.unavailable");
    }
    if (error.code === "TIMEOUT" || error.status === 504) return t("local.timeout");
    if (error.code === "INVALID_ARGUMENT" && error.max > 0) return t("local.concurrencyCeiling", { max: error.max });
  }
  return t("local.error", { message: error instanceof Error ? error.message : String(error) });
}

/** Why the runner would not use the picked file, in plain words. */
export function agentPathErrorText(t: Translate, error: unknown, kind: string): string {
  if (error instanceof RunnerRequestError && (AGENT_PATH_REFUSALS as readonly string[]).includes(error.refused)) {
    return t(`local.addAgent.refused.${error.refused}`, {
      agent: agentName(kind),
      file: agentProgram(kind),
      message: error.message,
    });
  }
  return localSettingsErrorText(t, error);
}

export function AddAgentDialog({
  runnerId,
  kind,
  onClose,
  onAdded,
}: {
  runnerId: string;
  kind: string;
  onClose: () => void;
  /** The runner as its new hello describes it, with the agent found. */
  onAdded: (runner: Runner) => void;
}) {
  const { t } = useI18n();
  const titleId = useId();
  const agent = agentName(kind);
  const file = agentProgram(kind);
  const [listing, setListing] = useState<DirListing | null>(null);
  const [home, setHome] = useState("");
  const [hidden, setHidden] = useState(false);
  const [picked, setPicked] = useState("");
  const [checking, setChecking] = useState(false);
  const [error, setError] = useState("");
  // A newer browse wins: an older answer arriving late is dropped.
  const turn = useRef(0);
  const live = useRef(true);
  useEffect(() => {
    live.current = true;
    return () => {
      live.current = false;
    };
  }, []);

  const browse = useCallback(
    async (path: string, showHidden: boolean) => {
      const mine = ++turn.current;
      try {
        // The list of drives holds no files to pick.
        const options = path ? (showHidden ? { files: file, hidden: true } : { files: file }) : undefined;
        const next = options ? await api.listDirs(path, options) : await api.listDirs(path);
        if (mine !== turn.current || !live.current) return;
        setListing(next);
        setPicked("");
        setError("");
      } catch (caught) {
        if (mine !== turn.current || !live.current) return;
        setError((caught as Error).message || t("explorer.readFolderFailed"));
      }
    },
    [file, t],
  );

  // Where to start: the home folder, which the drive list names.
  useEffect(() => {
    let cancelled = false;
    void (async () => {
      try {
        const top = await api.listDirs("");
        if (cancelled) return;
        setHome(top.home ?? "");
        if (top.home) void browse(top.home, false);
        else setListing(top);
      } catch (caught) {
        if (!cancelled) setError((caught as Error).message || t("explorer.readFolderFailed"));
      }
    })();
    return () => {
      cancelled = true;
    };
    // Once, when the dialog opens.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Over the scan step or a form: Escape closes this one only.
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return;
      if (document.querySelector('[role="alertdialog"]')) return;
      event.stopPropagation();
      onClose();
    };
    window.addEventListener("keydown", onKey, true);
    return () => window.removeEventListener("keydown", onKey, true);
  }, [onClose]);

  const use = async () => {
    if (!picked || checking) return;
    setChecking(true);
    setError("");
    try {
      const res = await api.setRunnerSettings(runnerId, { agentPath: { [kind]: picked }, redetect: true });
      holdRunner(res.runner);
      useGlobalAgents.getState().resync();
      if (live.current) setChecking(false);
      onAdded(res.runner);
    } catch (caught) {
      if (!live.current) return;
      setChecking(false);
      setError(agentPathErrorText(t, caught, kind));
    }
  };

  return createPortal(
    <div
      className="confirm-backdrop repo-add-backdrop"
      onMouseDown={(event) => {
        if (event.target === event.currentTarget && !checking) onClose();
      }}
      onClick={(event) => event.stopPropagation()}
    >
      <div className="confirm-dialog repo-add-dialog agent-add-dialog" role="dialog" aria-modal="true" aria-labelledby={titleId}>
        <header>
          <span className="confirm-dialog-icon">
            <IconComputer size={17} />
          </span>
          <div>
            <h2 id={titleId}>{t("local.addAgent.title", { agent })}</h2>
            <p>{t("local.addAgent.lead", { agent, file })}</p>
          </div>
          <button type="button" className="confirm-dialog-close" onClick={onClose} aria-label={t("common.close")}>
            <IconClose size={14} />
          </button>
        </header>
        <div className="repo-add-body">
          <FolderBrowser
            listing={listing}
            selected={false}
            busy={checking}
            onBrowse={(next) => void browse(next, hidden)}
            onError={(message) => setError(message ?? "")}
            home={home}
            files={listing?.path ? (listing.files ?? []) : undefined}
            pickedFile={picked}
            onPickFile={setPicked}
            emptyFilesText={t("local.addAgent.none", { file })}
          />
          <label className="route-toggle agent-add-hidden">
            <input
              type="checkbox"
              checked={hidden}
              disabled={checking}
              onChange={(event) => {
                setHidden(event.target.checked);
                if (listing) void browse(listing.path, event.target.checked);
              }}
            />
            <span>{t("local.addAgent.showHidden")}</span>
          </label>
          {checking && (
            <p className="repo-add-result" role="status">
              {t("local.addAgent.checking")}
            </p>
          )}
          {error && (
            <p className="notify-message error repo-add-error" role="alert">
              {error}
            </p>
          )}
        </div>
        <footer>
          <button type="button" onClick={onClose} disabled={checking}>
            {t("common.cancel")}
          </button>
          <button type="button" className="primary" disabled={!picked || checking} onClick={() => void use()}>
            {t("local.addAgent.use")}
          </button>
        </footer>
      </div>
    </div>,
    document.body,
  );
}
