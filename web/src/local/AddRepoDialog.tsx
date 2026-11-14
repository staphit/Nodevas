// "Add a repo" (single-user edition): the folder browser, then the built-in
// runner's word on the picked folder and the one button that fits it. A
// folder that is not a Git project yet can become one after the person saw
// what its first version saves and leaves out. The command-line way stays
// under Advanced, collapsed.

import { useEffect, useId, type ReactNode } from "react";
import { createPortal } from "react-dom";

import { api, type Runner, type RunnerRepo } from "../api";
import { FolderBrowser } from "../components/FolderBrowser";
import { IconClose, IconFolderPlus } from "../icons";
import { formatList, useI18n } from "../i18n";
import { useRunners } from "../runners/runnerStore";
import { inspectName, refusalText, useAddRepo } from "./useAddRepo";

/** Where Git is downloaded from: the project's own site. */
export const GIT_DOWNLOAD_URL = "https://git-scm.com/downloads";

/** The command that adds a repo from a command line, for Advanced. */
export function repoAddCommand(path: string, configPath: string | undefined): string {
  const target = path ? `"${path}"` : "<path>";
  return `nodevas runner repo add ${target}${configPath ? ` --config "${configPath}"` : ""}`;
}

export function AddRepoDialog({
  runnerId,
  onClose,
  onAdded,
}: {
  runnerId: string;
  onClose: () => void;
  /** The repo added (or picked when it was added already), with the runner's
   * new hello when the hub sent one. */
  onAdded: (repo: RunnerRepo, runner?: Runner) => void;
}) {
  const { t } = useI18n();
  const titleId = useId();
  const configPath = useRunners((s) => s.runners.find((item) => item.id === runnerId)?.info?.local?.configPath);
  const { state, browse, check, add, startInit, cancelInit, setError } = useAddRepo(runnerId, (repo, runner) =>
    onAdded(repo, runner),
  );
  const { listing, path, inspect, checking, adding, confirmInit, error, errorCode } = state;

  useEffect(() => {
    void browse("");
  }, [browse]);

  // Over the dispatch dialog or a form: Escape closes this one only.
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

  const busy = checking || adding;
  const name = inspect ? inspectName(inspect) : "";
  const plan = inspect?.init ?? null;

  let result: ReactNode = null;
  let primary: { label: string; run: () => void; disabled?: boolean } | null = {
    label: t("repoAdd.useFolder"),
    run: () => void check(),
    disabled: !path || busy,
  };
  if (checking) {
    result = <p className="repo-add-result" role="status">{t("repoAdd.checking")}</p>;
  } else if (inspect) {
    switch (inspect.state) {
      case "repo":
      case "inside_repo":
        if (inspect.already) {
          result = <p className="repo-add-result is-ok">{t("repoAdd.already", { name })}</p>;
          primary = inspect.repo
            ? { label: t("repoAdd.useExisting"), run: () => inspect.repo && onAdded(inspect.repo) }
            : null;
        } else {
          result = (
            <p className="repo-add-result is-ok">
              {inspect.state === "repo"
                ? t("repoAdd.ready", { name })
                : t("repoAdd.partOf", { top: inspect.top || inspect.path })}
            </p>
          );
          primary = {
            label: adding
              ? t("repoAdd.adding")
              : inspect.state === "repo"
                ? t("repoAdd.add")
                : t("repoAdd.addNamed", { name }),
            run: () => void add(),
            disabled: busy,
          };
        }
        break;
      case "not_repo":
        if (plan?.tooBig) {
          result = <p className="repo-add-result is-refused">{t("repoAdd.tooBig", { count: plan.files })}</p>;
          primary = null;
        } else if (confirmInit && plan) {
          const ignored = plan.ignored ?? [];
          result = (
            <div className="repo-add-result is-plan" role="group" aria-label={t("repoAdd.initOffer")}>
              <p>{t("repoAdd.initConfirm", { count: plan.files })}</p>
              {ignored.length > 0 && <p>{t("repoAdd.initIgnored", { list: formatList(ignored) })}</p>}
              {plan.writesGitignore && <p>{t("repoAdd.initGitignore")}</p>}
            </div>
          );
          primary = {
            label: adding ? t("repoAdd.adding") : t("repoAdd.initAndAdd"),
            run: () => void add(),
            disabled: busy || !plan.hash,
          };
        } else {
          result = <p className="repo-add-result">{t("repoAdd.notRepo")}</p>;
          primary = plan ? { label: t("repoAdd.initOffer"), run: startInit, disabled: busy } : null;
        }
        break;
      default:
        result = <p className="repo-add-result is-refused">{refusalText(t, inspect.refused)}</p>;
        primary = null;
    }
  }

  return createPortal(
    <div
      className="confirm-backdrop repo-add-backdrop"
      onMouseDown={(event) => {
        if (event.target === event.currentTarget) onClose();
      }}
      onClick={(event) => event.stopPropagation()}
    >
      <div className="confirm-dialog repo-add-dialog" role="dialog" aria-modal="true" aria-labelledby={titleId}>
        <header>
          <span className="confirm-dialog-icon">
            <IconFolderPlus size={17} />
          </span>
          <div>
            <h2 id={titleId}>{t("repoAdd.title")}</h2>
            <p>{t("repoAdd.subtitle")}</p>
          </div>
          <button type="button" className="confirm-dialog-close" onClick={onClose} aria-label={t("common.close")}>
            <IconClose size={14} />
          </button>
        </header>
        <div className="repo-add-body">
          <FolderBrowser
            listing={listing}
            selected={Boolean(path)}
            busy={busy}
            onBrowse={(next) => void browse(next)}
            createFolder={(parent, folder) => api.createDir(parent, folder)}
            onCreated={(created) => void browse(created)}
            onError={setError}
          />
          {result}
          {error && (
            <p className="notify-message error repo-add-error" role="alert">
              {error}
              {errorCode === "git_missing" && (
                <>
                  {" "}
                  <a href={GIT_DOWNLOAD_URL} target="_blank" rel="noreferrer noopener">
                    {t("repoAdd.gitDownload")}
                  </a>
                </>
              )}
            </p>
          )}
          <details className="run-advanced repo-add-advanced">
            <summary>{t("repoAdd.advancedCli")}</summary>
            <code>{repoAddCommand(path, configPath)}</code>
          </details>
        </div>
        <footer>
          <button type="button" onClick={confirmInit && !adding ? cancelInit : onClose} disabled={adding}>
            {t("common.cancel")}
          </button>
          {primary && (
            <button type="button" className="primary" disabled={primary.disabled} onClick={primary.run}>
              {primary.label}
            </button>
          )}
        </footer>
      </div>
    </div>,
    document.body,
  );
}
