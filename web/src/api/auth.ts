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
 * session recently re-proved who it is, and until when. A loopback server
 * without accounts reports `required: false`. */
export interface StepUpStatus {
  required: boolean;
  active: boolean;
  /** RFC3339; absent when not active. */
  until?: string;
  /** How this session steps up: a mailed passcode, or a code from the
   * account's authenticator app. Absent on a server without accounts (and
   * on older hubs, which only know email). */
  method?: StepUpMethod;
}

export type StepUpMethod = "email" | "totp";

/** POST /api/auth/login: either signed in, or — for an account with an
 * authenticator — waiting for its code (`secondFactor: "totp"`). The pending
 * sign-in's ticket is an HttpOnly cookie the page never sees. */
export interface LoginResult {
  ok: boolean;
  actor?: Actor;
  secondFactor?: "totp";
  /** RFC3339: when the pending sign-in dies. */
  expiresAt?: string;
}

/** GET /api/auth/totp: the signed-in account's own authenticator. */
export interface TotpState {
  enabled: boolean;
  /** Begun and not yet confirmed. */
  pending: boolean;
  enabledAt?: string;
  recoveryCodesLeft: number;
}

/** POST /api/auth/totp/begin: what the authenticator app enrolls from. */
export interface TotpEnrollment {
  /** base32, no padding: what a person types when the QR cannot be scanned. */
  secret: string;
  /** otpauth://totp/… */
  uri: string;
  /** A PNG data URL of the QR code, drawn by the server. */
  qr: string;
  account: string;
  issuer: string;
  digits: number;
  period: number;
}

export const authApi = {
  getAuthStatus: () => req<AuthStatus>("/api/auth/status"),
  /** Always resolves 202 whether or not the email is registered, so the caller
   * cannot turn it into an oracle for which addresses have accounts. */
  requestOtp: (email: string) =>
    authPost<{ ok: boolean }>("/api/auth/otp/request", { email }),
  login: (email: string, otp: string) =>
    authPost<LoginResult>("/api/auth/login", { email, otp }),
  /** The second step of a two-factor sign-in: an authenticator code or a
   * recovery code. 401 with code SIGNIN_EXPIRED means start over. */
  loginTotp: (code: string) =>
    authPost<{ ok: boolean; actor: Actor }>("/api/auth/login/totp", { code }),
  getStepUp: () => req<StepUpStatus>("/api/auth/step-up"),
  /** Mails a step-up passcode to the signed-in account — or, for an account
   * with an authenticator, sends nothing and says "totp". Through authPost,
   * not req(): a refusal here must not read as an expired session. */
  requestStepUp: async (): Promise<StepUpMethod> => {
    try {
      const reply = await authPost<{ method?: StepUpMethod } | null>("/api/auth/step-up/request", {});
      return reply?.method === "totp" ? "totp" : "email";
    } catch (error) {
      // A 202/204 with no JSON body: the request itself went through.
      if (error instanceof SyntaxError) return "email";
      throw error;
    }
  },
  /** 400/401 for a wrong or expired code, 429 when throttled. authPost keeps
   * a wrong code from signing the whole browser out. */
  verifyStepUp: (code: string) => authPost<StepUpStatus>("/api/auth/step-up", { code }),
  logout: () => req<{ ok: boolean }>("/api/auth/logout", { method: "POST" }),
  getTotp: () => req<TotpState>("/api/auth/totp"),
  /** 403 STEP_UP_REQUIRED until the session has stepped up. */
  beginTotp: () => authPost<TotpEnrollment>("/api/auth/totp/begin", {}),
  /** Returns the recovery codes: the only time they are ever shown. */
  confirmTotp: (code: string) =>
    authPost<{ enabled: boolean; recoveryCodes: string[] }>("/api/auth/totp/confirm", { code }),
  disableTotp: (code: string) => authPost<{ enabled: boolean }>("/api/auth/totp/disable", { code }),
  regenerateRecoveryCodes: (code: string) =>
    authPost<{ recoveryCodes: string[] }>("/api/auth/totp/recovery-codes", { code }),
};
