// The first folder for an agent (single-user edition), inside the describe
// box when this computer has none: a name (my-project) and "Create the
// folder", which makes <home>/Nodevas-Projects/<name> and adds it with a
// history started in it (firstFolder.ts). "Use a folder I already have" opens
// the folder picker in plain words instead. A team hub renders nothing.

import { useEffect, useId, useState } from "react";

import { api, type RunnerRepo } from "../api";
import { useI18n } from "../i18n";
import { useEdition } from "../runners/edition";
import { AddRepoDialog } from "./AddRepoDialog";
import { EXTERNAL_LINK } from "./agentInstallHelp";
import {
  badNameChars,
  createFirstFolder,
  DEFAULT_FOLDER_NAME,
  FirstFolderError,
  firstFolderErrorText,
  firstFolderPath,
} from "./firstFolder";

export function FirstFolderStep({ runnerId, onAdded }: { runnerId: string; onAdded: (repo: RunnerRepo) => void }) {
  const single = useEdition() === "single";
  if (!single) return null;
  return <FirstFolderBody runnerId={runnerId} onAdded={onAdded} />;
}

function FirstFolderBody({ runnerId, onAdded }: { runnerId: string; onAdded: (repo: RunnerRepo) => void }) {
  const { t } = useI18n();
  const fieldId = useId();
  const [name, setName] = useState(DEFAULT_FOLDER_NAME);
  const [home, setHome] = useState("");
  const [creating, setCreating] = useState(false);
  const [error, setError] = useState<{ text: string; link: string } | null>(null);
  const [picking, setPicking] = useState(false);

  // Where it will be made, said before anything is made.
  useEffect(() => {
    let cancelled = false;
    api
      .listDirs("")
      .then((top) => {
        if (!cancelled) setHome(top.home ?? "");
      })
      .catch(() => {
        /* the line saying where is left out */
      });
    return () => {
      cancelled = true;
    };
  }, []);

  const value = name.trim();
  const bad = badNameChars(value);
  const nameError = bad.length > 0 ? t("local.firstFolder.badName", { chars: bad.join(" ") }) : "";
  const shown = nameError ? { text: nameError, link: "" } : error;

  const create = async () => {
    if (creating || !value || bad.length > 0) return;
    setCreating(true);
    setError(null);
    try {
      const { repo } = await createFirstFolder(runnerId, value);
      onAdded(repo);
    } catch (caught) {
      setError({
        text: firstFolderErrorText(t, caught),
        link: caught instanceof FirstFolderError ? caught.link : "",
      });
    } finally {
      setCreating(false);
    }
  };

  return (
    <div className="first-folder">
      <h3>{t("local.firstFolder.title")}</h3>
      <p className="run-hint">{t("local.firstFolder.lead")}</p>
      <div className="run-field">
        <label className="run-field-label" htmlFor={fieldId}>
          {t("local.firstFolder.name")}
        </label>
        <input
          id={fieldId}
          value={name}
          maxLength={80}
          autoComplete="off"
          spellCheck={false}
          disabled={creating}
          aria-invalid={nameError ? true : undefined}
          onChange={(event) => {
            setName(event.target.value);
            setError(null);
          }}
          onKeyDown={(event) => {
            if (event.key === "Enter") {
              event.preventDefault();
              void create();
            }
          }}
        />
        {home && value && !nameError && (
          <small className="run-hint first-folder-where">
            {t("local.firstFolder.where", { path: firstFolderPath(home, value) })}
          </small>
        )}
      </div>
      {shown && (
        <p className="notify-message error" role="alert">
          {shown.text}
          {shown.link && (
            <>
              {" "}
              <a href={shown.link} target={EXTERNAL_LINK.target} rel={EXTERNAL_LINK.rel}>
                {t("repoAdd.gitDownload")}
              </a>
            </>
          )}
        </p>
      )}
      <div className="first-folder-actions">
        <button
          type="button"
          className="ap-btn is-primary"
          disabled={creating || !value || bad.length > 0}
          onClick={() => void create()}
        >
          {creating ? t("local.firstFolder.creating") : t("local.firstFolder.create")}
        </button>
        <button type="button" className="run-link-button" disabled={creating} onClick={() => setPicking(true)}>
          {t("local.firstFolder.pickExisting")}
        </button>
      </div>
      {picking && (
        <AddRepoDialog
          runnerId={runnerId}
          plain
          onClose={() => setPicking(false)}
          onAdded={(repo) => {
            setPicking(false);
            onAdded(repo);
          }}
        />
      )}
    </div>
  );
}
