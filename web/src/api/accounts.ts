import { req } from "./http";

// Mirrors internal/httpapi/system/accounts.go: the server's accounts, for its
// administrators. Every change needs a fresh step-up (403 otherwise). A server
// without accounts answers 404.

export type ServerRole = "admin" | "member";

export interface ServerAccount {
  id: string;
  name: string;
  email: string;
  role: ServerRole;
  createdAt: string;
  /** Signs in with an authenticator app as well as the mailed passcode. */
  twoFactor: boolean;
}

/** --max-active-users: how many people may be signed in at once (max 0: no
 * limit) and how many are. Invitations are never refused for it; sign-in is. */
export interface ServerSeats {
  active: number;
  max: number;
}

export interface ServerAccounts {
  accounts: ServerAccount[];
  /** The caller's own account ID. */
  self: string;
  /** Whether this server can send mail at all. */
  mailConfigured: boolean;
  signInUrl: string;
  seats: ServerSeats;
}

/** sent; unconfigured: the server has no outgoing mail; failed: the relay
 * refused it or never answered. The account exists either way. */
export type InviteEmailStatus = "sent" | "unconfigured" | "failed";

export interface InviteResult {
  account: ServerAccount;
  email: { status: InviteEmailStatus; to: string };
  signInUrl: string;
  seats: ServerSeats;
}

export const accountsApi = {
  listServerAccounts: () => req<ServerAccounts>("/api/auth/accounts"),
  inviteServerAccount: (name: string, email: string, role: ServerRole) =>
    req<InviteResult>("/api/auth/accounts", {
      method: "POST",
      body: JSON.stringify({ name, email, role }),
    }),
  setServerAccountRole: (id: string, role: ServerRole) =>
    req<ServerAccounts>("/api/auth/accounts/role", {
      method: "PUT",
      body: JSON.stringify({ id, role }),
    }),
  removeServerAccount: (id: string) =>
    req<ServerAccounts>(`/api/auth/accounts?id=${encodeURIComponent(id)}`, {
      method: "DELETE",
    }),
};
