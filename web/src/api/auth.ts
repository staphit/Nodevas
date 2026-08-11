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

export const authApi = {
  getAuthStatus: () =>
    req<{ mode: "local" | "accounts"; authenticated: boolean; actor: Actor }>(
      "/api/auth/status",
    ),
  /** Always resolves 202 whether or not the PIN exists, so the caller cannot
   * turn it into an oracle for which PINs are real. */
  requestOtp: (pin: string) =>
    authPost<{ ok: boolean }>("/api/auth/otp/request", { pin }),
  login: (pin: string, otp: string) =>
    authPost<{ ok: boolean; actor: Actor }>("/api/auth/login", { pin, otp }),
  logout: () => req<{ ok: boolean }>("/api/auth/logout", { method: "POST" }),
};
