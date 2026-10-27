import { useEffect, useMemo, useState } from "react";
import { DialogFrame, useInlineDialog } from "./DialogFrame";
import { operationScope, reportError, useApp, useOperation } from "../store";
import { IconClose } from "../icons";
import { useI18n } from "../i18n";
import { OperationStatus, InlineNotice } from "./InteractionPrimitives";
import { AppearanceEditor } from "./settings/AppearanceEditor";
import { CopilotEditor } from "./settings/CopilotEditor";
import { MilestoneTypeEditor } from "./settings/MilestoneTypeEditor";
import { PeopleEditor } from "./settings/PeopleEditor";
import { StatusVocabularyEditor } from "./settings/StatusVocabularyEditor";
import { TwoFactorEditor } from "./settings/TwoFactorEditor";
import { useTeamServer, useViewer } from "./SignIn";
import { MembersEditor } from "../members/MembersEditor";
import { ServerMembersPanel } from "../members/ServerMembersPanel";
import type { SettingsNotify } from "./settings/notify";

export type SettingsTab = "workflow" | "milestones" | "people" | "members" | "server" | "appearance" | "copilot" | "account";

const TABS: { id: SettingsTab; labelKey: string; hintKey: string }[] = [
  { id: "workflow", labelKey: "settings.workflow", hintKey: "settings.workflowHint" },
  { id: "milestones", labelKey: "settings.milestones", hintKey: "settings.milestonesHint" },
  { id: "people", labelKey: "settings.people", hintKey: "settings.peopleHint" },
  { id: "members", labelKey: "members.tab", hintKey: "members.tabHint" },
  { id: "appearance", labelKey: "settings.appearance", hintKey: "settings.appearanceHint" },
  { id: "copilot", labelKey: "settings.copilot", hintKey: "settings.copilotHint" },
];

/** The signed-in person's own account, not the project's: only on a server
 * with accounts. */
const ACCOUNT_TAB = { id: "account" as const, labelKey: "auth.totp.tab", hintKey: "auth.totp.tabHint" };

/** The server's own accounts, for its administrators. */
const SERVER_TAB = { id: "server" as const, labelKey: "serverMembers.tab", hintKey: "serverMembers.tabHint" };

/**
 * The single entry point for project-wide definitions [B-05].
 *
 * Each tab edits a different persisted domain, so the tabs are separate
 * components; this frame owns only the chrome and the one pair of banners they
 * all report through.
 */
export function ProjectSettings({
  onClose,
  only,
}: {
  onClose: () => void;
  /** The tabs to offer, in their usual order; the Settings view shows one
   * part at a time. Unset: every tab this viewer may use. */
  only?: readonly SettingsTab[];
}) {
  const { t } = useI18n();
  const inline = useInlineDialog();
  const activeProject = useApp((s) => s.activeProject);
  const viewer = useViewer();
  // Members, server accounts and account security only mean something with
  // accounts; a standalone install has one person and the OS decided who that is.
  const teamServer = useTeamServer();
  const serverAdmin = viewer?.role === "admin";
  const projectTabs = teamServer ? TABS : TABS.filter((entry) => entry.id !== "members");
  const allowed = viewer && teamServer ? [...projectTabs, ...(serverAdmin ? [SERVER_TAB] : []), ACCOUNT_TAB] : projectTabs;
  const tabs = only ? allowed.filter((entry) => only.includes(entry.id)) : allowed;
  const workflowOperation = useOperation(operationScope.workflow());

  const [tab, setTab] = useState<SettingsTab>(tabs[0]?.id ?? "workflow");
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape" && !inline) onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose, inline]);

  // Stable so an editor's effects do not restart every time a banner changes.
  const notify: SettingsNotify = useMemo(() => ({ onError: setError, onNotice: setNotice }), []);

  return (
    <DialogFrame className="project-settings-dialog" label={t("settings.title")} onDismiss={onClose}>
      <div className="settings-head">
        <div>
          <b>{t("settings.title")}</b>
          <small>{activeProject || t("settings.currentProject")}</small>
        </div>
        <OperationStatus
          status={workflowOperation.status}
          message={
            workflowOperation.status === "error" || workflowOperation.status === "conflict"
              ? workflowOperation.message
              : undefined
          }
        />
        <button type="button" data-dialog-close aria-label={t("settings.close")} onClick={onClose}>
          <IconClose size={14} />
        </button>
      </div>

      <div className="settings-tabs" role="tablist" aria-label={t("settings.categories")} hidden={tabs.length < 2}>
        {tabs.map((entry) => (
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

        {tab === "members" && (
          <MembersEditor notify={notify} onInvite={serverAdmin ? () => setTab("server") : undefined} />
        )}

        {tab === "server" && serverAdmin && <ServerMembersPanel notify={notify} />}

        {tab === "appearance" && <AppearanceEditor notify={notify} />}

        {tab === "copilot" && <CopilotEditor notify={notify} />}

        {tab === "account" && viewer && <TwoFactorEditor notify={notify} />}
      </div>
    </DialogFrame>
  );
}

/** Exposed so the topbar button can report failures the same way. */
export function reportSettingsError(error: unknown): void {
  reportError(error);
}
