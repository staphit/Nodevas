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

/** Mirrors runnerwire.StepUpStatus (GET /api/auth/step-up): whether this
 * session recently re-proved who it is with a fresh email passcode, and until
 * when. A loopback server without accounts reports `required: false`. */
export interface StepUpStatus {
  required: boolean;
  active: boolean;
  /** RFC3339; absent when not active. */
  until?: string;
}

export const authApi = {
  getAuthStatus: () => req<AuthStatus>("/api/auth/status"),
  /** Always resolves 202 whether or not the email is registered, so the caller
   * cannot turn it into an oracle for which addresses have accounts. */
  requestOtp: (email: string) =>
    authPost<{ ok: boolean }>("/api/auth/otp/request", { email }),
  login: (email: string, otp: string) =>
    authPost<{ ok: boolean; actor: Actor }>("/api/auth/login", { email, otp }),
  getStepUp: () => req<StepUpStatus>("/api/auth/step-up"),
  /** Mails a step-up passcode to the signed-in account. Through authPost,
   * not req(): a refusal here must not read as an expired session. */
  requestStepUp: async (): Promise<void> => {
    try {
      await authPost<unknown>("/api/auth/step-up/request", {});
    } catch (error) {
      // A 202/204 with no JSON body: the request itself went through.
      if (error instanceof SyntaxError) return;
      throw error;
    }
  },
  /** 400/401 for a wrong or expired code, 429 when throttled. authPost keeps
   * a wrong code from signing the whole browser out. */
  verifyStepUp: (code: string) => authPost<StepUpStatus>("/api/auth/step-up", { code }),
  logout: () => req<{ ok: boolean }>("/api/auth/logout", { method: "POST" }),
};
