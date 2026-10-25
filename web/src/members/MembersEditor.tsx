import { useCallback, useEffect, useState } from "react";
import { api, type MemberRole, type ProjectAccess, type ProjectMembers } from "../api";
import { confirmAction } from "../components/ConfirmDialog";
import { EmptyState } from "../components/InteractionPrimitives";
import type { SettingsNotify } from "../components/settings/notify";
import { useI18n } from "../i18n";
import { useApp } from "../store";

const ROLES: MemberRole[] = ["owner", "editor", "viewer"];

function errorText(error: unknown): string {
  if (error && typeof error === "object") {
    const conflict = error as { serverMessage?: unknown };
    if (typeof conflict.serverMessage === "string" && conflict.serverMessage) {
      return conflict.serverMessage;
    }
    if (error instanceof Error) return error.message;
  }
  return String(error);
}

/**
 * Who may reach this project. Open (no members) means every signed-in
 * account; adding the first member restricts it to the list, with whoever
 * did so as its first owner.
 */
export function MembersEditor({ notify }: { notify: SettingsNotify }) {
  const { t } = useI18n();
  const project = useApp((s) => s.activeProject) ?? "";
  const [access, setAccess] = useState<ProjectAccess | null>(null);
  const [list, setList] = useState<ProjectMembers | null>(null);
  const [user, setUser] = useState("");
  const [role, setRole] = useState<MemberRole>("editor");
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    try {
      const [nextAccess, nextList] = await Promise.all([
        api.projectAccess(project),
        api.listMembers(project),
      ]);
      setAccess(nextAccess);
      setList(nextList);
    } catch (error) {
      notify.onError(errorText(error));
    }
  }, [notify, project]);

  useEffect(() => {
    void load();
  }, [load]);

  const run = async (action: () => Promise<ProjectMembers>, done?: string) => {
    setBusy(true);
    try {
      setList(await action());
      setAccess(await api.projectAccess(project));
      notify.onError(null);
      if (done) notify.onNotice(done);
    } catch (error) {
      notify.onError(errorText(error));
    } finally {
      setBusy(false);
    }
  };

  const canManage = access?.canManage ?? false;
  const members = list?.members ?? [];

  const add = async () => {
    const who = user.trim();
    if (!who) return;
    if (!list?.restricted) {
      const ok = await confirmAction({
        title: t("members.restrictTitle"),
        description: t("members.restrictDescription"),
        confirmLabel: t("members.restrictConfirm"),
      });
      if (!ok) return;
    }
    await run(() => api.setMember(project, who, role), t("members.added", { name: who }));
    setUser("");
  };

  const open = async () => {
    const ok = await confirmAction({
      title: t("members.openTitle"),
      description: t("members.openDescription"),
      confirmLabel: t("members.openConfirm"),
      tone: "danger",
    });
    if (ok) await run(() => api.openProjectToEveryone(project), t("members.opened"));
  };

  return (
    <section className="settings-section members-editor">
      <p className="settings-hint">
        {list?.restricted ? t("members.restrictedIntro") : t("members.openIntro")}
      </p>
      {access && (
        <p className="members-own-role" data-role={access.role}>
          {t("members.yourRole", { role: t(`members.role.${access.role || "none"}`) })}
          {access.admin && <small> · {t("members.adminNote")}</small>}
        </p>
      )}

      {members.length > 0 ? (
        <ul className="settings-list members-list">
          {members.map((member) => (
            <li key={member.userId}>
              <span className="members-name">{member.name || member.userId}</span>
              {canManage ? (
                <select
                  value={member.role}
                  disabled={busy}
                  aria-label={t("members.roleFor", { name: member.name })}
                  onChange={(event) =>
                    void run(() =>
                      api.setMemberRole(project, member.userId, event.target.value as MemberRole),
                    )
                  }
                >
                  {ROLES.map((r) => (
                    <option key={r} value={r}>
                      {t(`members.role.${r}`)}
                    </option>
                  ))}
                </select>
              ) : (
                <span className="members-role">{t(`members.role.${member.role}`)}</span>
              )}
              {canManage && (
                <button
                  type="button"
                  className="danger"
                  disabled={busy}
                  aria-label={t("members.removeAria", { name: member.name })}
                  onClick={() => void run(() => api.removeMember(project, member.userId))}
                >
                  {t("members.remove")}
                </button>
              )}
            </li>
          ))}
        </ul>
      ) : (
        <EmptyState title={t("members.noneTitle")} description={t("members.noneHint")} />
      )}

      {canManage && (
        <form
          className="members-add"
          onSubmit={(event) => {
            event.preventDefault();
            void add();
          }}
        >
          <input
            value={user}
            onChange={(event) => setUser(event.target.value)}
            placeholder={t("members.addPlaceholder")}
            aria-label={t("members.addAria")}
            autoComplete="off"
          />
          <select
            value={role}
            onChange={(event) => setRole(event.target.value as MemberRole)}
            aria-label={t("members.newRoleAria")}
          >
            {ROLES.map((r) => (
              <option key={r} value={r}>
                {t(`members.role.${r}`)}
              </option>
            ))}
          </select>
          <button type="submit" disabled={busy || !user.trim()}>
            {t("members.add")}
          </button>
        </form>
      )}

      {canManage && list?.restricted && (
        <button type="button" className="members-open" disabled={busy} onClick={() => void open()}>
          {t("members.open")}
        </button>
      )}

      <p className="settings-hint members-roles-help">{t("members.rolesHelp")}</p>
    </section>
  );
}
