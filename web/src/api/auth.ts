import { authPost, req } from "./http";

/**
 * Who the server thinks this browser is.
 *
 * "visitor" is not an account: it is one shared read-only credential, so every
 * visitor carries the same id. The server refuses their writes on its own — see
 * withVisitorReadOnly — and the role is here so the UI can stop offering what
 * would be refused, not so it can be the thing that stops it.
 */
export interface Actor {
  id: string;
  name: string;
  role: "admin" | "member" | "visitor";
}

/** What /api/auth/status says about this server and this browser. */
export interface AuthStatus {
  mode: "local" | "accounts";
  authenticated: boolean;
  actor: Actor;
  /** True when the shared visitor credential is enabled, so the sign-in form
   * can offer its entry. An older server omits it; treat that as false. */
  visitor?: boolean;
}

export const authApi = {
  getAuthStatus: () => req<AuthStatus>("/api/auth/status"),
  /** Always resolves 202 whether or not the email is registered, so the caller
   * cannot turn it into an oracle for which addresses have accounts. */
  requestOtp: (email: string) =>
    authPost<{ ok: boolean }>("/api/auth/otp/request", { email }),
  login: (email: string, otp: string) =>
    authPost<{ ok: boolean; actor: Actor }>("/api/auth/login", { email, otp }),
  /** The shared read-only credential: a visitor PIN plus a fixed passcode that
   * is never mailed. */
  visitorLogin: (pin: string, passcode: string) =>
    authPost<{ ok: boolean; actor: Actor }>("/api/auth/visitor", { pin, passcode }),
  logout: () => req<{ ok: boolean }>("/api/auth/logout", { method: "POST" }),
};
