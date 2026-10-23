import { authPost, req } from "./http";

/** Who the server thinks this browser is. */
export interface Actor {
  id: string;
  name: string;
  role: "admin" | "member";
}

/** What /api/auth/status says about this server and this browser. */
export interface AuthStatus {
  mode: "local" | "accounts";
  authenticated: boolean;
  actor: Actor;
}

export const authApi = {
  getAuthStatus: () => req<AuthStatus>("/api/auth/status"),
  /** Always resolves 202 whether or not the email is registered, so the caller
   * cannot turn it into an oracle for which addresses have accounts. */
  requestOtp: (email: string) =>
    authPost<{ ok: boolean }>("/api/auth/otp/request", { email }),
  login: (email: string, otp: string) =>
    authPost<{ ok: boolean; actor: Actor }>("/api/auth/login", { email, otp }),
  logout: () => req<{ ok: boolean }>("/api/auth/logout", { method: "POST" }),
};
