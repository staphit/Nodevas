import { useEffect, useMemo, useState } from "react";
import { operationScope, reportError, useApp, useOperation } from "../store";
import { IconClose } from "../icons";
import { OperationStatus, InlineNotice } from "./InteractionPrimitives";
import { AppearanceEditor } from "./settings/AppearanceEditor";
import { MilestoneTypeEditor } from "./settings/MilestoneTypeEditor";
import { PeopleEditor } from "./settings/PeopleEditor";
import { StatusVocabularyEditor } from "./settings/StatusVocabularyEditor";
import type { SettingsNotify } from "./settings/notify";

type SettingsTab = "workflow" | "milestones" | "people" | "appearance";

const TABS: { id: SettingsTab; label: string; hint: string }[] = [
  { id: "workflow", label: "實際狀態", hint: "節點可以進入哪些實際狀態" },
  { id: "milestones", label: "里程碑類型", hint: "預期計畫可以安排哪些里程碑" },
  { id: "people", label: "成員", hint: "可指派的負責人" },
  { id: "appearance", label: "外觀與版面", hint: "只影響這台機器" },
];

/**
 * The single entry point for project-wide definitions [B-05].
 *
 * Each tab edits a different persisted domain, so the tabs are separate
 * components; this frame owns only the chrome and the one pair of banners they
 * all report through.
 */
export function ProjectSettings({ onClose }: { onClose: () => void }) {
  const activeProject = useApp((s) => s.activeProject);
  const workflowOperation = useOperation(operationScope.workflow());

  const [tab, setTab] = useState<SettingsTab>("workflow");
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  // Stable so an editor's effects do not restart every time a banner changes.
  const notify: SettingsNotify = useMemo(
    () => ({ onError: setError, onNotice: setNotice }),
    [],
  );

  return (
    <div className="confirm-backdrop" onClick={onClose}>
      <div
        className="confirm-dialog project-settings-dialog"
        role="dialog"
        aria-modal="true"
        aria-label="專案設定"
        onClick={(event) => event.stopPropagation()}
      >
        <div className="settings-head">
          <div>
            <b>專案設定</b>
            <small>{activeProject || "目前專案"}</small>
          </div>
          <OperationStatus
            status={workflowOperation.status}
            message={
              workflowOperation.status === "error" ||
              workflowOperation.status === "conflict"
                ? workflowOperation.message
                : undefined
            }
          />
          <button type="button" aria-label="關閉" onClick={onClose}>
            <IconClose size={14} />
          </button>
        </div>

        <div className="settings-tabs" role="tablist" aria-label="設定分類">
          {TABS.map((entry) => (
            <button
              key={entry.id}
              type="button"
              role="tab"
              aria-selected={tab === entry.id}
              className={tab === entry.id ? "active" : ""}
              onClick={() => {
                setTab(entry.id);
                setError(null);
                setNotice(null);
              }}
              title={entry.hint}
            >
              {entry.label}
            </button>
          ))}
        </div>

        <div className="settings-body">
          {error && <InlineNotice kind="error">{error}</InlineNotice>}
          {notice && !error && <InlineNotice kind="success">{notice}</InlineNotice>}

          {tab === "workflow" && <StatusVocabularyEditor notify={notify} />}

          {tab === "milestones" && <MilestoneTypeEditor notify={notify} />}

          {tab === "people" && <PeopleEditor notify={notify} />}

          {tab === "appearance" && <AppearanceEditor notify={notify} />}
        </div>
      </div>
    </div>
  );
}

/** Exposed so the topbar button can report failures the same way. */
export function reportSettingsError(error: unknown): void {
  reportError(error);
}
