// Settings: the third face, beside Info and Station. Everything that used to
// be a dialog of its own (project settings, deadline reminders, backup,
// runners, usage, budget, routing, account security) is a part of one page,
// with a list of the parts on the left. The dialogs draw themselves inline
// here (InlineDialogContext) and stay dialogs nowhere else.
//
// Loaded on demand (App's React.lazy).

import { useMemo } from "react";

import { AuditPanel } from "../audit/AuditPanel";
import { useSignOut, useTeamServer, useViewer } from "../components/SignIn";
import { BackupModal } from "../components/BackupModal";
import { InlineDialogContext } from "../components/DialogFrame";
import { NotifySettingsModal } from "../components/NotifySettingsModal";
import { ProjectSettings } from "../components/ProjectSettings";
import { setProjectMode } from "../game/stageSkin";
import { useI18n } from "../i18n";
import { RunnersModal } from "../runners/RunnersModal";
import { useWorkstationInUse, useWorkstationVisible } from "../runners/workstation";
import { BudgetSettings } from "../runs/BudgetSettings";
import { RoutingSettings } from "../runs/RoutingSettings";
import { RunNotifySettings } from "../runs/RunToasts";
import { useApp, usePreference } from "../store";
import { UsageModal } from "../usage/UsageModal";
import type { SettingsSection } from "./openSettings";

interface Part {
  id: SettingsSection;
  group: "project" | "you" | "agents" | "server";
}

/** Every part, in the order the list shows them; which are offered depends on
 * the workstation and on the server (below). */
const PARTS: readonly Part[] = [
  { id: "project", group: "project" },
  { id: "notifications", group: "project" },
  { id: "backup", group: "project" },
  { id: "appearance", group: "you" },
  { id: "copilot", group: "you" },
  { id: "workstation", group: "agents" },
  { id: "runners", group: "agents" },
  { id: "usage", group: "agents" },
  { id: "budget", group: "agents" },
  { id: "routing", group: "agents" },
  { id: "server", group: "server" },
  { id: "account", group: "server" },
  { id: "audit", group: "server" },
];

/** The switch that shows or hides everything else in the Agents group. */
function WorkstationPart() {
  const { t } = useI18n();
  const enabled = usePreference("agentWorkstation");
  const inUse = useWorkstationInUse();
  const on = enabled || inUse;
  return (
    <section className="settings-page-pane" aria-label={t("workstation.label")}>
      <h3>{t("workstation.label")}</h3>
      <div className="settings-section">
        <p className="settings-hint">{t("workstation.hint")}</p>
        <div className="settings-row">
          {/* Once this workspace uses it, it stays: a runner or a run would
              turn it straight back on. */}
          <button
            type="button"
            aria-pressed={on}
            disabled={inUse}
            onClick={() => useApp.getState().updateUIPreference("agentWorkstation", !on)}
          >
            {on ? t("workstation.disable") : t("workstation.enable")}
          </button>
        </div>
        {inUse && <p className="settings-hint">{t("workstation.inUse")}</p>}
      </div>
    </section>
  );
}

/** Signing out from the account part; the top bar keeps its own button. */
function SignOutPart() {
  const { t } = useI18n();
  const viewer = useViewer();
  const signOut = useSignOut();
  if (!viewer) return null;
  return (
    <section className="settings-page-pane" aria-label={t("settingsPage.signOut")}>
      <h3>{t("settingsPage.signOut")}</h3>
      <div className="settings-section">
        <div className="settings-row">
          <span>{t("settingsPage.signedInAs", { name: viewer.name || viewer.role })}</span>
          <button type="button" onClick={signOut}>
            {t("settingsPage.signOut")}
          </button>
        </div>
      </div>
    </section>
  );
}

export default function SettingsView() {
  const { t } = useI18n();
  const viewer = useViewer();
  const teamServer = useTeamServer();
  const workstation = useWorkstationVisible();
  const section = usePreference("settingsSection");
  const activeProject = useApp((s) => s.activeProject);

  const parts = useMemo(
    () =>
      PARTS.filter((part) => {
        // With the workstation off, its switch is all the Agents group holds.
        if (part.group === "agents") return part.id === "workstation" || workstation;
        if (part.id === "server" || part.id === "audit") return Boolean(teamServer && viewer?.role === "admin");
        if (part.id === "account") return Boolean(teamServer && viewer);
        return true;
      }),
    [workstation, teamServer, viewer],
  );
  // A part that is not offered (an Agents part with the workstation off, a
  // server part on a standalone install) opens the first part of its group,
  // so "adjust budget" with the workstation hidden lands on its switch.
  const wantedGroup = PARTS.find((part) => part.id === section)?.group;
  const current =
    parts.find((part) => part.id === section) ??
    parts.find((part) => part.group === wantedGroup) ??
    parts[0];
  const pick = (id: SettingsSection) => useApp.getState().updateUIPreference("settingsSection", id);
  // A dialog's close, inline: a part that sends you to a node goes back to
  // the board. Escape does nothing here; the mode switch leaves the page.
  const leave = () => setProjectMode("info");

  const groups: Part["group"][] = ["project", "you", "agents", "server"];
  return (
    <div className="settings-page" role="main" aria-label={t("mode.settings")}>
      <nav className="settings-page-nav" aria-label={t("settingsPage.parts")}>
        {groups.map((group) => {
          const items = parts.filter((part) => part.group === group);
          if (items.length === 0) return null;
          return (
            <div key={group} className="settings-page-group">
              <h2>
                {t(`settingsPage.group.${group}`)}
                {group === "project" && activeProject && <small>{activeProject}</small>}
              </h2>
              {items.map((part) => (
                <button
                  key={part.id}
                  type="button"
                  className={part.id === current.id ? "active" : ""}
                  aria-current={part.id === current.id ? "page" : undefined}
                  onClick={() => pick(part.id)}
                >
                  {t(`settingsPage.part.${part.id}`)}
                </button>
              ))}
            </div>
          );
        })}
      </nav>
      <div className="settings-page-body">
        <InlineDialogContext.Provider value={true}>
          {current.id === "project" && (
            <ProjectSettings onClose={leave} only={["workflow", "milestones", "people", "members"]} />
          )}
          {current.id === "appearance" && <ProjectSettings onClose={leave} only={["appearance"]} />}
          {current.id === "copilot" && <ProjectSettings onClose={leave} only={["copilot"]} />}
          {current.id === "server" && <ProjectSettings onClose={leave} only={["server"]} />}
          {current.id === "account" && (
            <>
              <ProjectSettings onClose={leave} only={["account"]} />
              <SignOutPart />
            </>
          )}
          {current.id === "notifications" && (
            <>
              <section className="settings-page-pane" aria-label={t("settingsPage.browserNotifications")}>
                <h3>{t("settingsPage.browserNotifications")}</h3>
                <RunNotifySettings />
              </section>
              <NotifySettingsModal onClose={leave} />
            </>
          )}
          {current.id === "backup" && <BackupModal onClose={leave} />}
          {current.id === "workstation" && <WorkstationPart />}
          {current.id === "runners" && <RunnersModal onClose={leave} />}
          {current.id === "usage" && <UsageModal onClose={leave} />}
          {current.id === "audit" && <AuditPanel />}
          {current.id === "budget" && (
            <section className="settings-page-pane" aria-label={t("budget.title")}>
              <h3>{t("budget.title")}</h3>
              <BudgetSettings />
            </section>
          )}
          {current.id === "routing" && (
            <section className="settings-page-pane" aria-label={t("routing.title")}>
              <h3>{t("routing.title")}</h3>
              <RoutingSettings />
            </section>
          )}
        </InlineDialogContext.Provider>
      </div>
    </div>
  );
}
