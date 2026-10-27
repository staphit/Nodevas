/**
 * How the audit trail is sorted into kinds and searched. The kinds follow the
 * stored action names the server writes (internal/audit/log.go, audit.go, and
 * the method-and-path rows middleware.go records for every other write); see
 * the table in docs/operations.md.
 */

import type { AuditEntry } from "../api";

export type AuditCategory = "signIn" | "stepUp" | "twoFactor" | "devices" | "accounts" | "settings" | "other";

export const AUDIT_CATEGORIES: readonly AuditCategory[] = [
  "signIn",
  "stepUp",
  "twoFactor",
  "devices",
  "accounts",
  "settings",
  "other",
];

/** The settings writes the server records by method and path: who wrote
 * them, not what changed. */
const SETTINGS_WRITES = new Set([
  "PUT notify/settings",
  "PUT copilot/settings",
  "PUT remote/config",
  "PUT runs/budget",
  "PUT runs/routing",
  "PUT usage/prices",
]);

/** Which kind a stored action is. Checked most specific first: a step-up and
 * a second factor are "auth." too, but not a sign-in. */
export function auditCategory(op: string): AuditCategory {
  if (op.startsWith("auth.stepup")) return "stepUp";
  if (op.startsWith("auth.totp.")) return "twoFactor";
  if (op.startsWith("auth.")) return "signIn";
  if (op.startsWith("runner.")) return "devices";
  if (op.startsWith("account.") || /^(PUT|DELETE|POST) projects\/members(\/|$)/.test(op)) return "accounts";
  if (SETTINGS_WRITES.has(op)) return "settings";
  return "other";
}

/** A refused attempt: the server names those with a ".failed" suffix. */
export function auditFailed(op: string): boolean {
  return op.endsWith(".failed");
}

/** The detail as label/value pairs, each value plain text. Nested values are
 * JSON text, so nothing in them is ever read as markup. */
export function auditDetailPairs(detail: AuditEntry["detail"]): [string, string][] {
  if (!detail || typeof detail !== "object") return [];
  return Object.entries(detail).map(([key, value]) => [
    key,
    typeof value === "string" ? value : JSON.stringify(value) ?? String(value),
  ]);
}

/** Everything a person might search an entry by, lower-cased. */
function haystack(entry: AuditEntry): string {
  const detail = auditDetailPairs(entry.detail)
    .map(([key, value]) => `${key} ${value}`)
    .join(" ");
  return [entry.op, entry.actor, entry.id, entry.target, entry.ip, detail].join(" ").toLowerCase();
}

/** The entries of one kind ("all" for every kind) that contain every word
 * of the search, in any field. */
export function filterAudit(entries: AuditEntry[], category: AuditCategory | "all", search: string): AuditEntry[] {
  const words = search.toLowerCase().split(/\s+/).filter(Boolean);
  return entries.filter((entry) => {
    if (category !== "all" && auditCategory(entry.op) !== category) return false;
    if (words.length === 0) return true;
    const text = haystack(entry);
    return words.every((word) => text.includes(word));
  });
}
