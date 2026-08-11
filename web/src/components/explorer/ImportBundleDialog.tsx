/**
 * Where a workspace bundle's projects land [B-06].
 *
 * Only asked when the picked file turns out to hold the whole workspace: a
 * single-project archive has one sensible destination and is imported without
 * a word. The choice has to be made before the upload rather than undone
 * after it, because "undo" for a restored workspace means moving every project
 * back out of a folder by hand.
 */

import { useEffect, useRef, useState } from "react";
import { IconImport } from "../../icons";
import type { BundleManifest } from "./projectArchive";

/** Where the archive's projects go. Mirrors the server's `mode` field. */
export interface ImportBundleChoice {
  mode: "root" | "folder";
  /** The new folder's name; only read when the mode is "folder". */
  name: string;
}

export function ImportBundleDialog({
  manifest,
  busy,
  target,
  onCancel,
  onConfirm,
}: {
  manifest: BundleManifest;
  busy: boolean;
  /** The project this import was started from, empty for the transfer bar. */
  target?: string;
  onCancel: () => void;
  onConfirm: (choice: ImportBundleChoice) => void;
}) {
  // Restoring the workspace as it was exported is what "匯入整個工作區" means to
  // the person who exported it; the folder is the deliberate second choice.
  // Started from a row in the tree, though, only the folder makes sense: the
  // request was for the archive to land inside that project, and spreading its
  // members across the workspace top level is the opposite of that.
  const [mode, setMode] = useState<ImportBundleChoice["mode"]>(
    target ? "folder" : "root",
  );
  const [name, setName] = useState(manifest.name);
  const confirmRef = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    confirmRef.current?.focus();
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        event.preventDefault();
        onCancel();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onCancel]);

  return (
    <div
      className="confirm-backdrop"
      onMouseDown={(event) => {
        if (event.target === event.currentTarget) onCancel();
      }}
    >
      <section
        className="confirm-dialog"
        role="dialog"
        aria-modal="true"
        aria-labelledby="import-bundle-title"
      >
        <header>
          <span className="confirm-dialog-icon">
            <IconImport size={17} />
          </span>
          <div>
            <h2 id="import-bundle-title">匯入專案封裝</h2>
            <p>
              偵測到這是整個工作區的封裝（{manifest.projects} 個專案）
              {target ? `，將放進 ${target} 底下` : ""}
            </p>
          </div>
        </header>
        <div className="notify-form">
          {!target && (
            <label className="notify-toggle">
              <input
                type="radio"
                name="import-bundle-mode"
                checked={mode === "root"}
                onChange={() => setMode("root")}
              />
              還原到工作區最上層
            </label>
          )}
          <label className="notify-toggle">
            <input
              type="radio"
              name="import-bundle-mode"
              checked={mode === "folder"}
              onChange={() => setMode("folder")}
            />
            放進新資料夾：
          </label>
          <div className="notify-field">
            <label htmlFor="import-bundle-folder" className="visually-hidden">
              新資料夾名稱
            </label>
            <input
              id="import-bundle-folder"
              value={name}
              onChange={(event) => setName(event.target.value)}
              // Typing a folder name is the same statement as picking the
              // radio, and a name typed into an unselected option would be
              // silently thrown away on 匯入.
              onFocus={() => setMode("folder")}
            />
          </div>
        </div>
        <footer>
          <button type="button" onClick={onCancel} disabled={busy}>
            取消
          </button>
          <button
            ref={confirmRef}
            type="button"
            className="primary"
            disabled={busy || (mode === "folder" && name.trim() === "")}
            onClick={() => onConfirm({ mode, name: name.trim() })}
          >
            匯入
          </button>
        </footer>
      </section>
    </div>
  );
}
