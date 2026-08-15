/**
 * Actual lifecycle panel [B-04/B-06].
 *
 * Journal-owned: the change goes to `run/journal.jsonl`, never to `graph.yaml`.
 * Picking a state stages it in the store; it is written only when somebody says
 * so — this panel's own button, or any of the explicit saves (Ctrl/⌘ + S in the
 * drawer, the global one, save-all, the popout editor), all of which commit it
 * alongside the document.
 *
 * The document itself auto-saves now, and this does not, on purpose: see
 * runSlice.ts. The consequence for this panel is that it needs the button it
 * previously did without. When Ctrl + S was the only way to save anything, "the
 * status goes out with the document" cost the user nothing to remember. Now
 * that the document leaves on its own, a staged status with no visible way to
 * apply it would be a change that silently never happens.
 */

import { reportError } from "../../store";
import { operationScope, useApp, useOperation } from "../../store";
import {
  StatusShape,
  statusOptions as availableStatusOptions,
  statusTheme,
} from "../../statusTheme";
import { localizedStatusLabel, useI18n } from "../../i18n";
import type { Status } from "../../types";
import { OperationStatus } from "../InteractionPrimitives";

export function LifecyclePanel({ id }: { id: string }) {
  const { t } = useI18n();
  const graph = useApp((state) => state.graph);
  const statuses = useApp((state) => state.statuses);
  const staged = useApp((state) => state.stagedLifecycle[id] ?? null);
  const stageLifecycleStatus = useApp((state) => state.stageLifecycleStatus);
  const commitStagedLifecycle = useApp((state) => state.commitStagedLifecycle);
  const operation = useOperation(operationScope.lifecycle(id));

  const customStatuses = graph?.ui?.customStatuses ?? [];
  const status: Status = statuses[id] ?? "ready";
  const visibleStatus = status === "locked" ? "ready" : status;
  const currentTheme = statusTheme(visibleStatus, customStatuses);
  const options = availableStatusOptions(customStatuses);

  const pending = staged?.status ?? "";
  const note = staged?.note ?? "";
  const busy = operation.status === "pending";
  // Choosing the state a node is already in is not a change worth writing.
  const unsaved = Boolean(pending) && pending !== visibleStatus;

  return (
    <section className="lifecycle-form" aria-labelledby={`lifecycle-heading-${id}`}>
      <div className="section-head">
        <h3 id={`lifecycle-heading-${id}`}>{t("lifecycle.title")}</h3>
        <span className="section-hint">{t("lifecycle.hint")}</span>
        <OperationStatus
          status={operation.status}
          message={
            operation.status === "error" || operation.status === "conflict"
              ? operation.message
              : undefined
          }
        />
      </div>
      <div className="meta-row">
        <label>{t("lifecycle.current")}</label>
        <span className="status-chip" style={{ color: currentTheme.color }}>
          <StatusShape status={visibleStatus} definitions={customStatuses} />
          {localizedStatusLabel(visibleStatus, customStatuses)}
        </span>
        <label htmlFor={`lifecycle-select-${id}`}>{t("lifecycle.changeTo")}</label>
        <select
          id={`lifecycle-select-${id}`}
          value={pending}
          disabled={busy}
          onChange={(event) =>
            stageLifecycleStatus(id, event.target.value as Status | "", note)
          }
        >
          <option value="">{t("lifecycle.choose")}</option>
          {options.map((candidate) => (
            <option key={candidate} value={candidate}>
              {localizedStatusLabel(candidate, customStatuses)}
            </option>
          ))}
        </select>
        <input
          className="status-note-input"
          value={note}
          onChange={(event) => stageLifecycleStatus(id, pending, event.target.value)}
          placeholder={t("lifecycle.notePlaceholder")}
          aria-label={t("lifecycle.noteLabel")}
          disabled={!pending}
        />
        {unsaved && (
          <>
            <button
              type="button"
              className="primary lifecycle-apply"
              disabled={busy}
              onClick={() => void commitStagedLifecycle(id).catch(reportError)}
            >
              {t("lifecycle.apply")}
            </button>
            <span className="lifecycle-staged" role="status">
              {t("lifecycle.pending")}
            </span>
          </>
        )}
      </div>
    </section>
  );
}
