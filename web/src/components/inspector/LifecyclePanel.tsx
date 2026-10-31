/**
 * The node's status picker [B-04/B-06].
 *
 * A node has one status a person sets: the actual lifecycle state, which is
 * journal-owned — the change goes to `run/journal.jsonl`, never to
 * `graph.yaml`. (The planned milestones are dates, not a state; they live on
 * the Activity tab's timeline.) So the panel shows a single picker, opened on
 * the state the node is in.
 *
 * Picking a different state stages it in the store; it is written only when
 * somebody says so — the Apply button that appears beside the picker, or any
 * of the explicit saves (Ctrl/⌘ + S in the drawer, the global one, save-all,
 * the popout editor), all of which commit it alongside the document.
 *
 * The document auto-saves, and this does not, on purpose: see runSlice.ts. A
 * staged status with no visible way to apply it would be a change that
 * silently never happens, hence the button. The optional note travels with the
 * entry and sits behind a small toggle, since most changes do not carry one.
 *
 * The node's type can take the picker away: a type with no status (a Note)
 * says so instead, and one that settles itself once its prerequisites do
 * (Start, End) shows the state it is in, read-only.
 */

import { useEffect, useState } from "react";

import { reportError } from "../../store";
import { operationScope, useApp, useOperation } from "../../store";
import {
  StatusShape,
  statusOptions as availableStatusOptions,
} from "../../statusTheme";
import { localizedStatusLabel, useI18n } from "../../i18n";
import type { Status } from "../../types";
import { OperationStatus } from "../InteractionPrimitives";
import { useNodeType } from "../../nodeTypeTheme";

export function LifecyclePanel({ id }: { id: string }) {
  const { t } = useI18n();
  const graph = useApp((state) => state.graph);
  const statuses = useApp((state) => state.statuses);
  const staged = useApp((state) => state.stagedLifecycle[id] ?? null);
  const stageLifecycleStatus = useApp((state) => state.stageLifecycleStatus);
  const commitStagedLifecycle = useApp((state) => state.commitStagedLifecycle);
  const operation = useOperation(operationScope.lifecycle(id));
  const { completion } = useNodeType({ nodeId: id });

  const customStatuses = graph?.ui?.customStatuses ?? [];
  const status: Status = statuses[id] ?? "ready";
  const visibleStatus = status === "locked" ? "ready" : status;
  const options = availableStatusOptions(customStatuses);

  const pending = staged?.status ?? "";
  const note = staged?.note ?? "";
  const busy = operation.status === "pending";
  // Choosing the state a node is already in is not a change worth writing.
  const unsaved = Boolean(pending) && pending !== visibleStatus;
  const shown = unsaved ? (pending as Status) : visibleStatus;
  // A note already typed stays in view; otherwise it waits behind the toggle.
  const [noteOpen, setNoteOpen] = useState(Boolean(note));
  useEffect(() => {
    if (!unsaved) setNoteOpen(false);
  }, [unsaved]);

  if (completion === "none") {
    return (
      <div className="status-control no-status" role="group" aria-label={t("lifecycle.title")}>
        <span className="status-control-readonly">{t("lifecycle.noStatus")}</span>
      </div>
    );
  }
  if (completion === "auto") {
    return (
      <div className="status-control auto-status" role="group" aria-label={t("lifecycle.title")}>
        <span className="status-control-shape" aria-hidden="true">
          <StatusShape status={visibleStatus} definitions={customStatuses} />
        </span>
        <span className="status-control-readonly">
          {localizedStatusLabel(visibleStatus, customStatuses)}
        </span>
        <small className="status-control-hint">{t("lifecycle.autoStatus")}</small>
      </div>
    );
  }

  return (
    <div
      className={`status-control${unsaved ? " staged" : ""}`}
      role="group"
      aria-label={t("lifecycle.title")}
    >
      <span className="status-control-shape" aria-hidden="true">
        <StatusShape status={shown} definitions={customStatuses} />
      </span>
      <select
        id={`lifecycle-select-${id}`}
        className="status-control-select"
        aria-label={t("drawer.status")}
        value={shown}
        disabled={busy}
        onChange={(event) => {
          const next = event.target.value as Status;
          stageLifecycleStatus(id, next === visibleStatus ? "" : next, note);
        }}
      >
        {!options.includes(visibleStatus) && (
          <option value={visibleStatus}>
            {localizedStatusLabel(visibleStatus, customStatuses)}
          </option>
        )}
        {options.map((candidate) => (
          <option key={candidate} value={candidate}>
            {localizedStatusLabel(candidate, customStatuses)}
          </option>
        ))}
      </select>
      {unsaved && (
        <>
          <button
            type="button"
            className="primary lifecycle-apply"
            title={t("lifecycle.hint")}
            disabled={busy}
            onClick={() => void commitStagedLifecycle(id).catch(reportError)}
          >
            {t("lifecycle.apply")}
          </button>
          <span className="lifecycle-staged" role="status">
            {t("lifecycle.pending")}
          </span>
          <button
            type="button"
            className="status-note-toggle"
            aria-expanded={noteOpen}
            aria-controls={`lifecycle-note-${id}`}
            onClick={() => setNoteOpen((open) => !open)}
          >
            {t("drawer.statusNote")}
          </button>
        </>
      )}
      <OperationStatus
        status={operation.status}
        message={
          operation.status === "error" || operation.status === "conflict"
            ? operation.message
            : undefined
        }
      />
      {unsaved && noteOpen && (
        <input
          id={`lifecycle-note-${id}`}
          className="status-note-input"
          value={note}
          autoFocus
          onChange={(event) => stageLifecycleStatus(id, pending, event.target.value)}
          placeholder={t("lifecycle.notePlaceholder")}
          aria-label={t("lifecycle.noteLabel")}
        />
      )}
    </div>
  );
}
