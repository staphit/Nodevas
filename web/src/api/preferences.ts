/**
 * The signed-in account's preferences (GET / PATCH /api/me/preferences): the
 * transport preferences/sync.ts runs on. The server keeps storage keys and the
 * schema's encoded strings, and answers every call with the whole merged set.
 */

import type {
  PreferenceChange,
  PreferenceTransport,
  RemotePreferences,
  SyncFailure,
} from "../preferences/sync";
import { AuthError, ConflictError, RequestError, UnauthorizedError, req } from "./http";

const PREFERENCES_URL = "/api/me/preferences";

/** Keeps the string values; a nil map (Go) or anything else reads as none. */
export function remotePreferences(body: unknown): RemotePreferences {
  const values: Record<string, string> = {};
  const raw = body && typeof body === "object" ? (body as { values?: unknown }).values : undefined;
  if (raw && typeof raw === "object" && !Array.isArray(raw)) {
    for (const [key, value] of Object.entries(raw as Record<string, unknown>)) {
      if (typeof value === "string") values[key] = value;
    }
  }
  return { values };
}

export const preferencesApi = {
  getPreferences: async (): Promise<RemotePreferences> =>
    remotePreferences(await req<unknown>(PREFERENCES_URL, { cache: "no-store" })),
  /** `keepalive` lets the request finish after the page has gone. */
  patchPreferences: async (change: PreferenceChange, keepalive = false): Promise<RemotePreferences> =>
    remotePreferences(
      await req<unknown>(PREFERENCES_URL, {
        method: "PATCH",
        body: JSON.stringify(change),
        keepalive,
      }),
    ),
};

/** How sync.ts treats a failed call; see SyncFailure. */
export function classifyPreferenceFailure(error: unknown): SyncFailure {
  if (error instanceof UnauthorizedError) return "signedOut";
  // 403: an agent's token, or a session the server keeps no preferences for.
  if (error instanceof AuthError) return error.status === 403 ? "unsupported" : "retry";
  if (error instanceof RequestError) {
    if (error.status === 404 || error.status === 405 || error.status === 501) return "unsupported";
    if (error.status === 408 || error.status === 429 || error.status >= 500) return "retry";
    return "refused";
  }
  if (error instanceof ConflictError) return "refused";
  // A page instead of JSON: a server from before preferences were kept.
  if (error instanceof SyntaxError) return "unsupported";
  // Unreachable or timed out.
  return "retry";
}

export const preferenceTransport: PreferenceTransport = {
  load: preferencesApi.getPreferences,
  save: (change, { keepalive }) => preferencesApi.patchPreferences(change, keepalive),
  classify: classifyPreferenceFailure,
};
