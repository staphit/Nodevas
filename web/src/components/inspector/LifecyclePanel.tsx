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
import type { Status } from "../../types";
import { OperationStatus } from "../InteractionPrimitives";

export function LifecyclePanel({ id }: { id: string }) {
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
        <h3 id={`lifecycle-heading-${id}`}>實際狀態</h3>
        <span className="section-hint">
          寫入 run/journal.jsonl 後無法收回，因此不會自動儲存；請按「套用狀態」或 Ctrl + S
        </span>
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
        <label>目前</label>
        <span className="status-chip" style={{ color: currentTheme.color }}>
          <StatusShape status={visibleStatus} definitions={customStatuses} />
          {currentTheme.label}
        </span>
        <label htmlFor={`lifecycle-select-${id}`}>改為</label>
        <select
          id={`lifecycle-select-${id}`}
          value={pending}
          disabled={busy}
          onChange={(event) =>
            stageLifecycleStatus(id, event.target.value as Status | "", note)
          }
        >
          <option value="">選擇實際狀態</option>
          {options.map((candidate) => (
            <option key={candidate} value={candidate}>
              {statusTheme(candidate, customStatuses).label}
            </option>
          ))}
        </select>
        <input
          className="status-note-input"
          value={note}
          onChange={(event) => stageLifecycleStatus(id, pending, event.target.value)}
          placeholder="紀錄註解（選填）"
          aria-label="實際狀態註解"
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
              套用狀態
            </button>
            <span className="lifecycle-staged" role="status">
              尚未套用
            </span>
          </>
        )}
      </div>
    </section>
  );
}
