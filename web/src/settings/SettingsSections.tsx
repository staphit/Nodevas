// The General, This project and Account sections of Settings. Agents has a
// file of its own (AgentsSection). Each section is a card: what is changed
// often on its first layer, the rest as rows that open in place.

import { AuditPanel } from "../audit/AuditPanel";
import { BackupModal } from "../components/BackupModal";
import { NotifySettingsModal } from "../components/NotifySettingsModal";
import { ProjectSettings } from "../components/ProjectSettings";
import { useSignOut, useTeamServer, useViewer } from "../components/SignIn";
import { localizedPlanStatusLabel, useI18n } from "../i18n";
import { RunNotifySettings } from "../runs/RunToasts";
import { useApp } from "../store";
import { isBuiltinKind } from "../domain/graph/nodeTypes";
import type { SettingsPage, SettingsRow } from "./openSettings";
import { listValue, SettingsDisclosure } from "./SettingsDisclosure";

export interface SectionProps {
  row: SettingsRow | null;
  /** Opens a row, closing the one open; closes it when it is the open one. */
  toggleRow: (row: SettingsRow) => void;
  /** Goes to a row of another section. */
  go: (page: SettingsPage, row: SettingsRow | null) => void;
  /** A part's close, inline: back to the board. */
  leave: () => void;
}

export function GeneralSection({ row, toggleRow, leave }: SectionProps) {
  const { t } = useI18n();
  const reminders = useApp((s) => s.notifySettings);
  return (
    <section className="settings-page-pane settings-card" aria-label={t("settingsPage.page.general")}>
      <h2>{t("settingsPage.page.general")}</h2>
      <section className="settings-subsection" aria-label={t("settingsPage.part.appearance")}>
        <h3>{t("settingsPage.part.appearance")}</h3>
        <ProjectSettings tab="appearance" />
      </section>
      <SettingsDisclosure label={t("settingsPage.layout")} open={row === "layout"} onToggle={() => toggleRow("layout")}>
        <ProjectSettings tab="layout" />
      </SettingsDisclosure>
      <section className="settings-subsection" aria-label={t("settingsPage.part.notifications")}>
        <h3>{t("settingsPage.part.notifications")}</h3>
        <RunNotifySettings />
      </section>
      <SettingsDisclosure
        label={t("notify.title")}
        value={reminders ? (reminders.enabled ? t("settingsPage.on") : t("settingsPage.off")) : ""}
        open={row === "reminders"}
        onToggle={() => toggleRow("reminders")}
      >
        <NotifySettingsModal onClose={leave} />
      </SettingsDisclosure>
      <SettingsDisclosure
        label={t("settingsPage.part.copilot")}
        open={row === "copilot"}
        onToggle={() => toggleRow("copilot")}
      >
        <ProjectSettings tab="copilot" />
      </SettingsDisclosure>
    </section>
  );
}

export function ProjectSection({ row, toggleRow, go, leave }: SectionProps) {
  const { t, language } = useI18n();
  const activeProject = useApp((s) => s.activeProject);
  const graph = useApp((s) => s.graph);
  const statuses = (graph?.ui?.customStatuses ?? []).map((status) => status.label);
  const plans = graph?.ui?.planStatuses ?? [];
  const milestones = plans.map((plan) => localizedPlanStatusLabel(plan.id, plans, language));
  const people = (graph?.users ?? []).map((user) => user.name || user.id);
  const customTypes = (graph?.nodeTypes ?? [])
    .filter((type) => !isBuiltinKind(type.id))
    .map((type) => type.label ?? type.id);
  return (
    <section className="settings-page-pane settings-card" aria-label={t("settingsPage.page.project")}>
      <h2>
        {t("settingsPage.page.project")}
        {activeProject && <small>{activeProject}</small>}
      </h2>
      <SettingsDisclosure
        label={t("settings.workflow")}
        value={listValue(statuses) || t("settingsPage.builtInOnly")}
        open={row === "statuses"}
        onToggle={() => toggleRow("statuses")}
      >
        <ProjectSettings tab="workflow" />
      </SettingsDisclosure>
      <SettingsDisclosure
        label={t("settings.nodeTypes")}
        value={listValue(customTypes) || t("settingsPage.builtInOnly")}
        open={row === "nodeTypes"}
        onToggle={() => toggleRow("nodeTypes")}
      >
        <ProjectSettings tab="nodeTypes" />
      </SettingsDisclosure>
      <SettingsDisclosure
        label={t("settings.milestones")}
        value={listValue(milestones) || t("settingsPage.builtInOnly")}
        open={row === "milestones"}
        onToggle={() => toggleRow("milestones")}
      >
        <ProjectSettings tab="milestones" />
      </SettingsDisclosure>
      <SettingsDisclosure
        label={t("settings.people")}
        value={listValue(people) || t("settingsPage.none")}
        open={row === "people"}
        onToggle={() => toggleRow("people")}
      >
        <ProjectSettings tab="people" onInvite={() => go("account", "server")} />
      </SettingsDisclosure>
      <SettingsDisclosure
        label={t("settingsPage.part.backup")}
        open={row === "backup"}
        onToggle={() => toggleRow("backup")}
      >
        <BackupModal onClose={leave} />
      </SettingsDisclosure>
    </section>
  );
}

export function AccountSection({ row, toggleRow }: SectionProps) {
  const { t } = useI18n();
  const viewer = useViewer();
  const teamServer = useTeamServer();
  const signOut = useSignOut();
  const admin = Boolean(teamServer && viewer?.role === "admin");
  return (
    <section className="settings-page-pane settings-card" aria-label={t("settingsPage.page.account")}>
      <h2>{t("settingsPage.page.account")}</h2>
      {!teamServer && <p className="settings-hint">{t("settingsPage.standalone")}</p>}
      {teamServer && viewer && (
        <section className="settings-subsection" aria-label={t("settingsPage.part.account")}>
          <h3>{t("settingsPage.part.account")}</h3>
          {/* Signing out from here; the top bar keeps its own way too. */}
          <div className="settings-row">
            <span>{t("settingsPage.signedInAs", { name: viewer.name || viewer.role })}</span>
            <button type="button" onClick={signOut}>
              {t("settingsPage.signOut")}
            </button>
          </div>
          <ProjectSettings tab="account" />
        </section>
      )}
      {admin && (
        <>
          <SettingsDisclosure
            label={t("settingsPage.part.server")}
            open={row === "server"}
            onToggle={() => toggleRow("server")}
          >
            <ProjectSettings tab="server" />
          </SettingsDisclosure>
          <SettingsDisclosure
            label={t("settingsPage.part.audit")}
            open={row === "audit"}
            onToggle={() => toggleRow("audit")}
          >
            <AuditPanel />
          </SettingsDisclosure>
        </>
      )}
    </section>
  );
}
