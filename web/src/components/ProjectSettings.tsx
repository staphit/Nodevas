import { useMemo, useState } from "react";
import { operationScope, reportError, useOperation } from "../store";
import { OperationStatus, InlineNotice } from "./InteractionPrimitives";
import { AppearanceEditor, LayoutEditor } from "./settings/AppearanceEditor";
import { CopilotEditor } from "./settings/CopilotEditor";
import { MilestoneTypeEditor } from "./settings/MilestoneTypeEditor";
import { PeopleEditor } from "./settings/PeopleEditor";
import { StatusVocabularyEditor } from "./settings/StatusVocabularyEditor";
import { TwoFactorEditor } from "./settings/TwoFactorEditor";
import { useTeamServer, useViewer } from "./SignIn";
import { MembersEditor } from "../members/MembersEditor";
import { ServerMembersPanel } from "../members/ServerMembersPanel";
import type { SettingsNotify } from "./settings/notify";

export type SettingsTab =
  | "workflow"
  | "milestones"
  | "people"
  | "server"
  | "appearance"
  | "layout"
  | "copilot"
  | "account";

/**
 * One of the project-wide definitions [B-05] (or one of the viewer's own
 * settings), as a part of the Settings page: the editor and the one pair of
 * banners it reports through.
 *
 * Each editor edits a different persisted domain, so they are separate
 * components; this owns only the banners. People also carries the project's
 * members on a server with accounts. Server accounts and account security
 * only mean something with accounts, and server accounts only to an
 * administrator; elsewhere those draw nothing.
 */
export function ProjectSettings({
  tab,
  onInvite,
}: {
  tab: SettingsTab;
  /** Members' "invite to the server": opens the server's accounts. */
  onInvite?: () => void;
}) {
  const viewer = useViewer();
  const teamServer = useTeamServer();
  const serverAdmin = Boolean(teamServer && viewer?.role === "admin");
  const workflowOperation = useOperation(operationScope.workflow());

  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);

  // Stable so an editor's effects do not restart every time a banner changes.
  const notify: SettingsNotify = useMemo(() => ({ onError: setError, onNotice: setNotice }), []);
  const workflow = tab === "workflow" || tab === "milestones" || tab === "people";

  return (
    <div className="settings-editor">
      {workflow && (
        <OperationStatus
          status={workflowOperation.status}
          message={
            workflowOperation.status === "error" || workflowOperation.status === "conflict"
              ? workflowOperation.message
              : undefined
          }
        />
      )}
      {error && <InlineNotice kind="error">{error}</InlineNotice>}
      {notice && !error && <InlineNotice kind="success">{notice}</InlineNotice>}

      {tab === "workflow" && <StatusVocabularyEditor notify={notify} />}

      {tab === "milestones" && <MilestoneTypeEditor notify={notify} />}

      {tab === "people" && (
        <>
          <PeopleEditor notify={notify} />
          {teamServer && <MembersEditor notify={notify} onInvite={serverAdmin ? onInvite : undefined} />}
        </>
      )}

      {tab === "server" && serverAdmin && <ServerMembersPanel notify={notify} />}

      {tab === "appearance" && <AppearanceEditor />}

      {tab === "layout" && <LayoutEditor notify={notify} />}

      {tab === "copilot" && <CopilotEditor notify={notify} />}

      {tab === "account" && teamServer && viewer && <TwoFactorEditor notify={notify} />}
    </div>
  );
}

/** Exposed so the topbar button can report failures the same way. */
export function reportSettingsError(error: unknown): void {
  reportError(error);
}
