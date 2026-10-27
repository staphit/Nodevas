import { useCallback, useEffect, useState } from "react";
import {
  api,
  type InviteResult,
  type ServerAccount,
  type ServerAccounts,
  type ServerRole,
} from "../api";
import { confirmAction } from "../components/ConfirmDialog";
import { InlineNotice } from "../components/InteractionPrimitives";
import type { SettingsNotify } from "../components/settings/notify";
import { useI18n } from "../i18n";
import { needsStepUp, useFreshStepUp } from "../stepup/freshStepUp";

const ROLES: ServerRole[] = ["member", "admin"];

function errorText(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

/** A sign-in name from an address's local part, in the characters an account
 * name may hold; "" when nothing usable is left. */
export function suggestedName(email: string): string {
  const local = email.trim().split("@")[0] ?? "";
  return local
    .replace(/[^\p{L}\p{N}._-]+/gu, "-")
    .replace(/^[^\p{L}\p{N}]+/u, "")
    .slice(0, 64);
}

/**
 * Members of this server: every account that can sign in, for the server's
 * administrators. They invite people by email (the person then signs in with
 * a passcode mailed to that address), change who is an administrator, and
 * remove accounts. Every change asks for a fresh step-up first.
 *
 * Shown only to administrators on a server with accounts; the server refuses
 * everyone else anyway.
 */
export function ServerMembersPanel({ notify }: { notify: SettingsNotify }) {
  const { t } = useI18n();
  const [data, setData] = useState<ServerAccounts | null>(null);
  const [name, setName] = useState("");
  const [email, setEmail] = useState("");
  const [role, setRole] = useState<ServerRole>("member");
  const [busy, setBusy] = useState(false);
  const [invited, setInvited] = useState<InviteResult | null>(null);
  const stepUp = useFreshStepUp();

  const load = useCallback(async () => {
    try {
      setData(await api.listServerAccounts());
    } catch (error) {
      notify.onError(errorText(error));
    }
  }, [notify]);

  useEffect(() => {
    void load();
  }, [load]);

  /** Runs a change, stepping up first when the hub wants it (or says so
   * by refusing). */
  const attempt = async (action: () => Promise<void>) => {
    setBusy(true);
    try {
      await action();
      notify.onError(null);
    } catch (failure) {
      if (await needsStepUp(failure)) {
        stepUp.ask(() => void attempt(action));
        return;
      }
      notify.onError(errorText(failure));
    } finally {
      setBusy(false);
    }
  };
  const change = (action: () => Promise<void>) => stepUp.run(() => void attempt(action));

  const invite = () => {
    const address = email.trim();
    const account = name.trim() || suggestedName(address);
    if (!address || !account) return;
    change(async () => {
      const result = await api.inviteServerAccount(account, address, role);
      setInvited(result);
      setName("");
      setEmail("");
      setRole("member");
      if (result.email.status === "sent") {
        notify.onNotice(t("serverMembers.invitedSent", { email: result.email.to }));
      }
      setData(await api.listServerAccounts());
    });
  };

  const setAccountRole = (account: ServerAccount, next: ServerRole) =>
    change(async () => {
      setData(await api.setServerAccountRole(account.id, next));
      notify.onNotice(t("serverMembers.roleChanged", { name: account.name, role: t(`serverMembers.role.${next}`) }));
    });

  const remove = async (account: ServerAccount) => {
    const ok = await confirmAction({
      title: t("serverMembers.removeTitle", { name: account.name }),
      description: t("serverMembers.removeDescription", { email: account.email || account.name }),
      confirmLabel: t("serverMembers.removeConfirm"),
      tone: "danger",
    });
    if (!ok) return;
    change(async () => {
      setData(await api.removeServerAccount(account.id));
      notify.onNotice(t("serverMembers.removed", { name: account.name }));
    });
  };

  const accounts = data?.accounts ?? [];
  const seats = data?.seats;

  return (
    <section className="settings-section server-members">
      <p className="settings-hint">{t("serverMembers.intro")}</p>
      {data && !data.mailConfigured && (
        <InlineNotice kind="warning">{t("serverMembers.noMail")}</InlineNotice>
      )}
      {seats && seats.max > 0 && (
        <p className="server-members-seats">
          {t("serverMembers.seats", { active: seats.active, max: seats.max })}
          {accounts.length > seats.max && <small> · {t("serverMembers.seatsHint")}</small>}
        </p>
      )}

      {accounts.length > 0 && (
        <ul className="settings-list members-list server-members-list">
          {accounts.map((account) => {
            const self = account.id === data?.self;
            return (
              <li key={account.id}>
                <span className="members-name">
                  {account.name}
                  {self && <small> · {t("serverMembers.you")}</small>}
                </span>
                <span className="server-members-email">{account.email || t("serverMembers.noEmail")}</span>
                <span
                  className="server-members-2fa"
                  data-on={account.twoFactor ? "true" : "false"}
                >
                  {account.twoFactor ? t("serverMembers.twoFactorOn") : t("serverMembers.twoFactorOff")}
                </span>
                {self ? (
                  <span className="members-role">{t(`serverMembers.role.${account.role}`)}</span>
                ) : (
                  <select
                    value={account.role}
                    disabled={busy}
                    aria-label={t("serverMembers.roleFor", { name: account.name })}
                    onChange={(event) => setAccountRole(account, event.target.value as ServerRole)}
                  >
                    {ROLES.map((r) => (
                      <option key={r} value={r}>
                        {t(`serverMembers.role.${r}`)}
                      </option>
                    ))}
                  </select>
                )}
                {!self && (
                  <button
                    type="button"
                    className="danger"
                    disabled={busy}
                    aria-label={t("serverMembers.removeAria", { name: account.name })}
                    onClick={() => void remove(account)}
                  >
                    {t("serverMembers.remove")}
                  </button>
                )}
              </li>
            );
          })}
        </ul>
      )}

      <form
        className="members-add server-members-invite"
        aria-label={t("serverMembers.inviteTitle")}
        onSubmit={(event) => {
          event.preventDefault();
          invite();
        }}
      >
        <input
          type="email"
          value={email}
          onChange={(event) => setEmail(event.target.value)}
          placeholder={t("serverMembers.emailPlaceholder")}
          aria-label={t("serverMembers.emailAria")}
          autoComplete="off"
        />
        <input
          value={name}
          onChange={(event) => setName(event.target.value)}
          placeholder={suggestedName(email) || t("serverMembers.namePlaceholder")}
          aria-label={t("serverMembers.nameAria")}
          autoComplete="off"
        />
        <select
          value={role}
          onChange={(event) => setRole(event.target.value as ServerRole)}
          aria-label={t("serverMembers.newRoleAria")}
        >
          {ROLES.map((r) => (
            <option key={r} value={r}>
              {t(`serverMembers.role.${r}`)}
            </option>
          ))}
        </select>
        <button type="submit" disabled={busy || !email.trim()}>
          {t("serverMembers.invite")}
        </button>
      </form>
      <p className="settings-hint">{t("serverMembers.nameHint")}</p>

      {invited && invited.email.status !== "sent" && (
        <InviteFallback result={invited} onDismiss={() => setInvited(null)} />
      )}

      {stepUp.dialog}
    </section>
  );
}

/** The invitation did not go out: the account exists, and this is what to
 * tell the person instead. */
function InviteFallback({ result, onDismiss }: { result: InviteResult; onDismiss: () => void }) {
  const { t } = useI18n();
  const message = t("serverMembers.tellThemText", { url: result.signInUrl, email: result.email.to });
  const [copied, setCopied] = useState(false);
  return (
    <div className="server-members-fallback" role="status">
      <InlineNotice kind="warning">
        {result.email.status === "unconfigured"
          ? t("serverMembers.inviteUnconfigured", { name: result.account.name })
          : t("serverMembers.inviteFailed", { name: result.account.name })}
      </InlineNotice>
      <p className="settings-hint">{t("serverMembers.tellThem")}</p>
      <blockquote className="server-members-message">{message}</blockquote>
      <div className="server-members-fallback-actions">
        <button
          type="button"
          onClick={() => {
            void navigator.clipboard
              ?.writeText(message)
              .then(() => setCopied(true))
              .catch(() => setCopied(false));
          }}
        >
          {copied ? t("serverMembers.copied") : t("serverMembers.copy")}
        </button>
        <button type="button" onClick={onDismiss}>
          {t("serverMembers.dismiss")}
        </button>
      </div>
    </div>
  );
}
