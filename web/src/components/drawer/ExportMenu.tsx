import { useCallback, useEffect, useRef, useState } from "react";

import type { ExportFormat, ExportScope } from "../../api";
import { downloadExport, printExport } from "../../editor/documentExport";
import { IconClose, IconExport } from "../../icons";
import { useI18n } from "../../i18n";
import { useApp } from "../../store";

const EXPORT_SCOPES: readonly ExportScope[] = ["page", "node", "project"];

type ExportChoice = ExportFormat | "pdf";

const EXPORT_FORMATS: readonly (readonly [ExportChoice, string, string])[] = [
  ["docx", "format.docx", ".docx"],
  ["txt", "format.txt", ".txt"],
  ["html", "format.html", ".html"],
  ["md", "format.md", ".md"],
  ["pdf", "export.pdf", ".pdf"],
];

// The unsaved buffer travels with the request, so what downloads is what is
// on screen rather than what last reached the disk.
//
// Scope and format are both picked in a dialog and nothing leaves until the
// summary line — which names the actual page, node or project — is confirmed.
// The old dropdown fired on the format click, so a scope that had not visibly
// registered went out as whatever was selected before.
export function ExportMenu({
  nodeId,
  pageId,
  pageTitle,
  content,
}: {
  nodeId: string;
  pageId: string;
  pageTitle?: string;
  content: string;
}) {
  const { t } = useI18n();
  const nodeTitle = useApp(
    (state) => state.graph?.nodes?.find((node) => node.id === nodeId)?.title,
  );
  // The root project is named "." on disk; people know it by its label.
  const projectName = useApp(
    (state) =>
      state.projects.find((project) => project.name === state.activeProject)?.label ||
      state.activeProject,
  );
  const [open, setOpen] = useState(false);
  const [scope, setScope] = useState<ExportScope>("page");
  const [choice, setChoice] = useState<ExportChoice | null>(null);
  const [busy, setBusy] = useState(false);
  const [result, setResult] = useState<{ ok: boolean; text: string } | null>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const dialogRef = useRef<HTMLElement>(null);

  const close = useCallback(() => {
    if (busy) return;
    setOpen(false);
    triggerRef.current?.focus();
  }, [busy]);

  useEffect(() => {
    if (!open) return;
    dialogRef.current?.querySelector<HTMLElement>("input:checked")?.focus();
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        event.preventDefault();
        close();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open, close]);

  const pageName =
    pageId === "main" ? t("drawer.mainPage") : pageTitle?.trim() || pageId;
  const nodeName = nodeTitle?.trim() || nodeId;
  const scopeTarget: Record<ExportScope, string> = {
    page: `${nodeName} › ${pageName}`,
    node: nodeName,
    project: projectName || t("explorer.currentProject"),
  };
  const format = EXPORT_FORMATS.find(([id]) => id === choice);

  const runExport = async () => {
    if (!choice) return;
    setBusy(true);
    setResult(null);
    try {
      const request = { scope, nodeId, pageId, content };
      if (choice === "pdf") {
        await printExport(request);
        setResult({ ok: true, text: t("export.printOpened") });
      } else {
        const name = await downloadExport({ ...request, format: choice });
        setResult({ ok: true, text: t("export.exported", { name }) });
      }
    } catch (error) {
      setResult({
        ok: false,
        text: t(choice === "pdf" ? "export.pdfFailed" : "export.failed", {
          error: (error as Error).message,
        }),
      });
    } finally {
      setBusy(false);
    }
  };

  return (
    <>
      <button
        ref={triggerRef}
        type="button"
        className="tool-btn"
        title={t("export.fileTitle")}
        aria-label={t("export.fileLabel")}
        aria-haspopup="dialog"
        onClick={() => {
          setResult(null);
          setChoice(null);
          setOpen(true);
        }}
      >
        <IconExport size={14} />
      </button>
      {open && (
        <div
          className="confirm-backdrop"
          onMouseDown={(event) => {
            if (event.target === event.currentTarget) close();
          }}
        >
          <section
            ref={dialogRef}
            className="confirm-dialog export-dialog"
            role="dialog"
            aria-modal="true"
            aria-labelledby="export-dialog-title"
          >
            <header>
              <span className="confirm-dialog-icon">
                <IconExport size={17} />
              </span>
              <div>
                <h2 id="export-dialog-title">{t("export.fileLabel")}</h2>
                <p>{t("export.dialogHint")}</p>
              </div>
              <button
                type="button"
                className="confirm-dialog-close"
                onClick={close}
                aria-label={t("common.close")}
              >
                <IconClose size={15} />
              </button>
            </header>
            <div className="export-dialog-body">
              <fieldset>
                <legend>{t("export.scopeTitle")}</legend>
                {EXPORT_SCOPES.map((id) => (
                  <label key={id} className={scope === id ? "selected" : ""}>
                    <input
                      type="radio"
                      name="export-scope"
                      checked={scope === id}
                      onChange={() => {
                        setScope(id);
                        setResult(null);
                      }}
                    />
                    <span>
                      <b>{t(`export.scope.${id}`)}</b>
                      <small>{scopeTarget[id]}</small>
                    </span>
                  </label>
                ))}
              </fieldset>
              <fieldset>
                <legend>{t("export.format")}</legend>
                {EXPORT_FORMATS.map(([id, label, extension]) => (
                  <label key={id} className={choice === id ? "selected" : ""}>
                    <input
                      type="radio"
                      name="export-format"
                      checked={choice === id}
                      onChange={() => {
                        setChoice(id);
                        setResult(null);
                      }}
                    />
                    <span>
                      <b>{t(label)}</b>
                      <small className="mono">{extension}</small>
                    </span>
                  </label>
                ))}
              </fieldset>
              <div className="export-dialog-summary" aria-live="polite">
                {format
                  ? t("export.summary", {
                      target: scopeTarget[scope],
                      scope: t(`export.scope.${scope}`),
                      format: `${t(format[1])} (${format[2]})`,
                    })
                  : t("export.pickFormat")}
                {choice === "pdf" && <small>{t("export.pdfHint")}</small>}
              </div>
              {result && (
                <div
                  className={`export-dialog-notice${result.ok ? "" : " error"}`}
                  role={result.ok ? "status" : "alert"}
                >
                  {result.text}
                </div>
              )}
            </div>
            <footer>
              <button type="button" onClick={close} disabled={busy}>
                {result?.ok ? t("common.close") : t("common.cancel")}
              </button>
              {!result?.ok && (
                <button
                  type="button"
                  className="primary"
                  disabled={busy || !choice}
                  onClick={() => void runExport()}
                >
                  {busy ? t("export.exporting") : t("export.confirm")}
                </button>
              )}
            </footer>
          </section>
        </div>
      )}
    </>
  );
}
