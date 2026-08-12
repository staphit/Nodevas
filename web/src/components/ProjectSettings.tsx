import { useEffect, useMemo, useState } from "react";
import { operationScope, reportError, useApp, useOperation } from "../store";
import { IconClose } from "../icons";
import { useI18n } from "../i18n";
import { OperationStatus, InlineNotice } from "./InteractionPrimitives";
import { AppearanceEditor } from "./settings/AppearanceEditor";
import { MilestoneTypeEditor } from "./settings/MilestoneTypeEditor";
import { PeopleEditor } from "./settings/PeopleEditor";
import { StatusVocabularyEditor } from "./settings/StatusVocabularyEditor";
import type { SettingsNotify } from "./settings/notify";

type SettingsTab = "workflow" | "milestones" | "people" | "appearance";

const TABS: { id: SettingsTab; labelKey: string; hintKey: string }[] = [
  { id: "workflow", labelKey: "settings.workflow", hintKey: "settings.workflowHint" },
  { id: "milestones", labelKey: "settings.milestones", hintKey: "settings.milestonesHint" },
  { id: "people", labelKey: "settings.people", hintKey: "settings.peopleHint" },
  { id: "appearance", labelKey: "settings.appearance", hintKey: "settings.appearanceHint" },
];

/**
 * The single entry point for project-wide definitions [B-05].
 *
 * Each tab edits a different persisted domain, so the tabs are separate
 * components; this frame owns only the chrome and the one pair of banners they
 * all report through.
 */
export function ProjectSettings({ onClose }: { onClose: () => void }) {
  const { t } = useI18n();
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
        aria-label={t("settings.title")}
        onClick={(event) => event.stopPropagation()}
      >
        <div className="settings-head">
          <div>
            <b>{t("settings.title")}</b>
            <small>{activeProject || t("settings.currentProject")}</small>
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
          <button type="button" aria-label={t("settings.close")} onClick={onClose}>
            <IconClose size={14} />
          </button>
        </div>

        <div className="settings-tabs" role="tablist" aria-label={t("settings.categories")}>
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
              title={t(entry.hintKey)}
            >
              {t(entry.labelKey)}
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
