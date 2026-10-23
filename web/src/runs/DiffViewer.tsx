import { Fragment, useEffect, useMemo, useState } from "react";
import { createPortal } from "react-dom";
import type { FileChange, Run, RunChanges } from "../api";
import { IconClose } from "../icons";
import { useI18n } from "../i18n";
import {
  findDiffFile,
  parsePatch,
  splitRows,
  type DiffFile,
  type DiffHunk,
  type DiffLine,
} from "./patch";
import { MergeControl, mergeConfirmOpen } from "./MergeApproval";
import { LineCounts, RunChangesSummary } from "./RunChanges";
import { formatCount, safeExternalUrl } from "./runFormat";
import { useRuns } from "./runStore";

export type DiffLayout = "unified" | "split";

const LAYOUT_KEY = "nodevas.runs.diffLayout";
/** Side by side needs room for two columns of code. */
export const WIDE_QUERY = "(min-width: 900px)";
/** Lines rendered for a file before "show more", and per press after. */
export const INITIAL_LINES = 400;
export const MORE_LINES = 1000;

const STATUS_LETTER: Record<string, string> = {
  added: "A",
  modified: "M",
  deleted: "D",
  renamed: "R",
};

export function readDiffLayout(): DiffLayout {
  try {
    return window.localStorage.getItem(LAYOUT_KEY) === "unified" ? "unified" : "split";
  } catch {
    return "split";
  }
}

export function writeDiffLayout(layout: DiffLayout): void {
  try {
    window.localStorage.setItem(LAYOUT_KEY, layout);
  } catch {
    // A private window or blocked storage: the choice lasts this session.
  }
}

function useMediaQuery(query: string): boolean {
  const [matches, setMatches] = useState(() => {
    if (typeof window === "undefined" || !window.matchMedia) return false;
    return window.matchMedia(query).matches;
  });
  useEffect(() => {
    if (typeof window === "undefined" || !window.matchMedia) return;
    const list = window.matchMedia(query);
    const onChange = (event: MediaQueryListEvent) => setMatches(event.matches);
    setMatches(list.matches);
    list.addEventListener?.("change", onChange);
    return () => list.removeEventListener?.("change", onChange);
  }, [query]);
  return matches;
}

/** The run's file list, or one made from the patch if the runner sent none. */
function changedFiles(changes: RunChanges, parsed: DiffFile[]): FileChange[] {
  if (changes.files && changes.files.length > 0) return changes.files;
  return parsed.map((file) => ({
    path: file.path,
    status: file.status,
    oldPath: file.status === "renamed" ? file.oldPath : undefined,
    additions: file.additions,
    deletions: file.deletions,
    binary: file.binary || undefined,
  }));
}

function StatusLetter({ status }: { status: string }) {
  const { t } = useI18n();
  const known = status in STATUS_LETTER;
  return (
    <span
      className={`run-diff-status run-diff-status-${known ? status : "unknown"}`}
      title={known ? t(`diff.status.${status}`) : status}
      aria-label={known ? t(`diff.status.${status}`) : status}
    >
      {STATUS_LETTER[status] ?? "?"}
    </span>
  );
}

function NoNewline() {
  const { t } = useI18n();
  return (
    <span className="run-diff-nonl" title={t("diff.noNewline")}>
      {" "}⊘ {t("diff.noNewline")}
    </span>
  );
}

const SIGN: Record<DiffLine["kind"], string> = { context: " ", add: "+", del: "-" };

/** Rows a hunk takes, in either layout, for the "show more" budget. */
function hunkRowCount(hunk: DiffHunk, layout: DiffLayout): number {
  return 1 + (layout === "split" ? splitRows(hunk.lines).length : hunk.lines.length);
}

function UnifiedHunk({ hunk, budget }: { hunk: DiffHunk; budget: number }) {
  const lines = hunk.lines.slice(0, Math.max(0, budget));
  return (
    <>
      <tr className="run-diff-hunk">
        <td colSpan={3}>{hunk.header}</td>
      </tr>
      {lines.map((line, index) => (
        <tr key={index} className={`run-diff-line is-${line.kind}`}>
          <td className="run-diff-no">{line.oldNo ?? ""}</td>
          <td className="run-diff-no">{line.newNo ?? ""}</td>
          <td className="run-diff-code">
            <span className="run-diff-sign" aria-hidden="true">
              {SIGN[line.kind]}
            </span>
            {line.text}
            {line.noNewline && <NoNewline />}
          </td>
        </tr>
      ))}
    </>
  );
}

function SplitCell({ line, side }: { line: DiffLine | undefined; side: "old" | "new" }) {
  if (!line) {
    return (
      <>
        <td className="run-diff-no is-empty" />
        <td className="run-diff-code is-empty" />
      </>
    );
  }
  const kind = line.kind;
  return (
    <>
      <td className={`run-diff-no is-${kind}`}>{side === "old" ? line.oldNo : line.newNo}</td>
      <td className={`run-diff-code is-${kind}`}>
        <span className="run-diff-sign" aria-hidden="true">
          {SIGN[kind]}
        </span>
        {line.text}
        {line.noNewline && <NoNewline />}
      </td>
    </>
  );
}

function SplitHunk({ hunk, budget }: { hunk: DiffHunk; budget: number }) {
  const rows = splitRows(hunk.lines).slice(0, Math.max(0, budget));
  return (
    <>
      <tr className="run-diff-hunk">
        <td colSpan={4}>{hunk.header}</td>
      </tr>
      {rows.map((row, index) => (
        <tr key={index} className="run-diff-line">
          <SplitCell line={row.left} side="old" />
          <SplitCell line={row.right} side="new" />
        </tr>
      ))}
    </>
  );
}

/** One file's hunks as a table, the first `INITIAL_LINES` rows at a time. */
export function DiffTable({ file, layout }: { file: DiffFile; layout: DiffLayout }) {
  const { t } = useI18n();
  const [limit, setLimit] = useState(INITIAL_LINES);
  const total = useMemo(
    () => file.hunks.reduce((sum, hunk) => sum + hunkRowCount(hunk, layout), 0),
    [file, layout],
  );

  let left = limit;
  const shown: { hunk: DiffHunk; budget: number }[] = [];
  for (const hunk of file.hunks) {
    if (left <= 0) break;
    // The header row counts too.
    shown.push({ hunk, budget: left - 1 });
    left -= hunkRowCount(hunk, layout);
  }
  const remaining = Math.max(0, total - limit);

  return (
    <div className="run-diff-scroll">
      <table className={`run-diff-table is-${layout}`} aria-label={file.path}>
        {layout === "split" && (
          <colgroup>
            <col className="run-diff-col-no" />
            <col />
            <col className="run-diff-col-no" />
            <col />
          </colgroup>
        )}
        <tbody>
          {shown.map(({ hunk, budget }, index) => (
            <Fragment key={index}>
              {layout === "split" ? (
                <SplitHunk hunk={hunk} budget={budget} />
              ) : (
                <UnifiedHunk hunk={hunk} budget={budget} />
              )}
            </Fragment>
          ))}
        </tbody>
      </table>
      {remaining > 0 && (
        <button
          type="button"
          className="run-diff-more"
          onClick={() => setLimit((value) => value + MORE_LINES)}
        >
          {t("diff.showMore", { count: formatCount(Math.min(remaining, MORE_LINES)) })}
        </button>
      )}
    </div>
  );
}

function FileView({
  change,
  file,
  changes,
  run,
  layout,
}: {
  change: FileChange;
  file: DiffFile | undefined;
  changes: RunChanges;
  run: Run;
  layout: DiffLayout;
}) {
  const { t } = useI18n();
  const prHref = safeExternalUrl(run.prUrl);
  let body: React.ReactNode;
  if (changes.patchTruncated || !changes.patch) {
    body = (
      <p className="run-diff-note">
        {prHref ? t("diff.truncated") : t("diff.truncatedNoPr")}
        {prHref && (
          <>
            {" "}
            <a href={prHref} target="_blank" rel="noopener noreferrer">
              {t("diff.openPr", { number: run.prNumber ?? "" })}
            </a>
          </>
        )}
      </p>
    );
  } else if (change.binary || file?.binary) {
    body = <p className="run-diff-note">{t("diff.binary")}</p>;
  } else if (!file) {
    body = <p className="run-diff-note">{t("diff.notInPatch")}</p>;
  } else if (file.hunks.length === 0) {
    body = <p className="run-diff-note">{t("diff.noLines")}</p>;
  } else {
    body = <DiffTable key={`${file.path}:${layout}`} file={file} layout={layout} />;
  }
  const oldPath = change.oldPath || (file?.status === "renamed" ? file.oldPath : "");
  return (
    <>
      <div className="run-diff-file-head">
        <StatusLetter status={change.status} />
        <div className="run-diff-file-name">
          <strong className="mono">{change.path}</strong>
          {change.status === "renamed" && oldPath && (
            <small className="mono">{t("diff.renamedFrom", { path: oldPath })}</small>
          )}
        </div>
        {!change.binary && <LineCounts additions={change.additions} deletions={change.deletions} />}
      </div>
      {body}
    </>
  );
}

/**
 * A finished run's changes, read-only: the file list and, for the chosen
 * file, its hunks — side by side on a wide screen (or unified, the reader's
 * choice), always unified on a phone. Fetches the changes when opened.
 */
export function RunDiffDialog({ run, onClose }: { run: Run; onClose: () => void }) {
  const { t } = useI18n();
  const held = useRuns((s) => s.changesByRun[run.id]);
  const loading = useRuns((s) => Boolean(s.changesLoading[run.id]));
  const error = useRuns((s) => s.changesErrors[run.id]);
  const loadChanges = useRuns((s) => s.loadChanges);
  const wide = useMediaQuery(WIDE_QUERY);
  const [preferred, setPreferred] = useState<DiffLayout>(readDiffLayout);
  const layout: DiffLayout = wide ? preferred : "unified";
  const [selectedPath, setSelectedPath] = useState<string | null>(null);

  useEffect(() => {
    void loadChanges(run.id, run.commit);
  }, [run.id, run.commit, loadChanges]);

  useEffect(() => {
    // Captured first and stopped: the Drawer underneath closes on Escape too,
    // and one press should only close the diff.
    const onKey = (event: KeyboardEvent) => {
      // A merge confirmation over the diff takes the Escape for itself.
      if (event.key !== "Escape" || mergeConfirmOpen()) return;
      event.stopPropagation();
      onClose();
    };
    window.addEventListener("keydown", onKey, true);
    return () => window.removeEventListener("keydown", onKey, true);
  }, [onClose]);

  const changes = held?.changes ?? null;
  const parsed = useMemo(
    () => (changes?.patch && !changes.patchTruncated ? parsePatch(changes.patch) : []),
    [changes],
  );
  const files = useMemo(() => (changes ? changedFiles(changes, parsed) : []), [changes, parsed]);

  // A wide screen always shows a file; a phone starts on the list.
  const selected =
    files.find((file) => file.path === selectedPath) ?? (wide ? files[0] : undefined);

  const chooseLayout = (next: DiffLayout) => {
    setPreferred(next);
    writeDiffLayout(next);
  };

  let content: React.ReactNode;
  if (!held) {
    content = error ? (
      <div className="notify-message error" role="alert">
        {error}
      </div>
    ) : (
      <p className="run-diff-note">{loading ? t("common.loading") : ""}</p>
    );
  } else if (!changes) {
    content = <p className="run-diff-note">{t("diff.none")}</p>;
  } else if (files.length === 0) {
    content = <p className="run-diff-note">{t("diff.empty")}</p>;
  } else {
    content = (
      <div className={`run-diff-body${wide ? " is-wide" : ""}${selected ? " has-file" : ""}`}>
        <nav className="run-diff-files" aria-label={t("diff.files")}>
          <ul>
            {files.map((file) => (
              <li key={file.path}>
                <button
                  type="button"
                  className="run-diff-file-button"
                  aria-current={file.path === selected?.path ? "true" : undefined}
                  onClick={() => setSelectedPath(file.path)}
                >
                  <StatusLetter status={file.status} />
                  <span className="run-diff-file-path mono">{file.path}</span>
                  {file.binary ? (
                    <span className="run-diff-bin">bin</span>
                  ) : (
                    <LineCounts additions={file.additions} deletions={file.deletions} />
                  )}
                </button>
              </li>
            ))}
          </ul>
        </nav>
        {selected && (
          <section className="run-diff-file" aria-label={selected.path}>
            {!wide && (
              <button
                type="button"
                className="run-diff-back"
                onClick={() => setSelectedPath(null)}
              >
                ← {t("diff.back")}
              </button>
            )}
            <FileView
              change={selected}
              file={findDiffFile(parsed, selected.path, selected.oldPath)}
              changes={changes}
              run={run}
              layout={layout}
            />
          </section>
        )}
      </div>
    );
  }

  const branch = run.branch || run.base;
  // Portalled: the Drawer that opens it may be transformed (the phone sheet),
  // which would pin a fixed backdrop to the Drawer instead of the viewport.
  return createPortal(
    <div className="confirm-backdrop run-diff-backdrop" onClick={onClose}>
      <div
        className="run-diff-dialog"
        role="dialog"
        aria-modal="true"
        aria-labelledby="run-diff-title"
        onClick={(event) => event.stopPropagation()}
      >
        <header className="run-diff-head">
          <div className="run-diff-heading">
            <h2 id="run-diff-title">{t("diff.title")}</h2>
            <p className="mono">
              {t("diff.subtitle", {
                agent: run.model ? `${run.agent} · ${run.model}` : run.agent,
                branch,
              })}
            </p>
          </div>
          {wide && changes && files.length > 0 && (
            <div className="run-diff-layout" role="group" aria-label={t("diff.layout")}>
              {(["split", "unified"] as const).map((option) => (
                <button
                  key={option}
                  type="button"
                  aria-pressed={layout === option}
                  onClick={() => chooseLayout(option)}
                >
                  {t(option === "split" ? "diff.split" : "diff.unified")}
                </button>
              ))}
            </div>
          )}
          <button
            type="button"
            className="confirm-dialog-close"
            onClick={onClose}
            aria-label={t("common.close")}
          >
            <IconClose size={14} />
          </button>
        </header>
        {changes && files.length > 0 && (
          <div className="run-diff-summary">
            <RunChangesSummary
              run={{
                ...run,
                additions: changes.additions,
                deletions: changes.deletions,
                filesChanged: files.length,
                commit: changes.commit || run.commit,
              }}
            />
          </div>
        )}
        {content}
        {/* Read the diff, then approve: the merge sits at the foot, where a
            thumb reaches it on a phone. Empty (and hidden) without a PR. */}
        <footer className="run-diff-foot">
          <MergeControl run={run} />
        </footer>
      </div>
    </div>,
    document.body,
  );
}
