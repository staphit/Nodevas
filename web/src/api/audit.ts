import { AuthError, notifyUnauthorized, req, RequestError, UnauthorizedError } from "./http";

/**
 * The server's audit trail and its health, for administrators
 * (internal/httpapi/system/audit.go). Every route here is admin-only on the
 * server; a member gets 403 whatever the UI shows.
 */

/** One row of the trail. Every string can carry what a caller typed (an
 * address at a refused sign-in, a name, a node title), so it is shown as text
 * and never as markup or a link. */
export interface AuditEntry {
  /** RFC 3339. */
  at: string;
  /** The actor's name; "" when nobody was signed in (a refused sign-in). */
  actor: string;
  /** The actor's account ID. */
  id: string;
  /** The stored action: a dotted name ("auth.signin.failed") or, for a write
   * the server recorded on its own, the method and path ("PUT notify/settings"). */
  op: string;
  target: string;
  ip: string;
  /** Free-form, already sanitised by the server; null when there is none. */
  detail: Record<string, unknown> | null;
}

/** "server" is the events that belong to no project (sign-ins, accounts,
 * device links); "project" is the active project's writes. */
export type AuditScope = "server" | "project";

export interface AuditQuery {
  scope?: AuditScope;
  /** The server caps it (1000) and defaults it (200). */
  limit?: number;
}

export type AuditHealthStatus = "healthy" | "degraded" | "unconfigured";

/** audit.Health: whether rows are reaching the database now, and whether
 * every row that fell back to the process log has been reconciled. */
export interface AuditHealth {
  status: AuditHealthStatus;
  writeStatus: AuditHealthStatus;
  /** Rows written to the log because the database refused them, since start. */
  fallbackEvents: number;
  acknowledgedFallbackEvents: number;
  unreconciledEvents: number;
  lastFailureAt?: string;
  lastError?: string;
}

/** server.AbuseStats. A server without the guard answers only
 * `active_buckets: 0`, so everything else is optional. */
export interface AuditLimiterStats {
  active_buckets: number;
  max_entries?: number;
  rejected_total?: number;
  rejected_by_class?: Record<string, number>;
  semaphores?: Record<string, { in_use: number; capacity: number }>;
}

const HEALTH_STATUSES: readonly string[] = ["healthy", "degraded", "unconfigured"];

function isHealth(value: unknown): value is AuditHealth {
  if (typeof value !== "object" || value === null) return false;
  const body = value as Record<string, unknown>;
  return HEALTH_STATUSES.includes(body.status as string) && typeof body.writeStatus === "string";
}

/**
 * GET /api/audit/health answers 503 when the trail is degraded or not
 * configured, with the health itself as the body. req() would turn that into
 * a bare "503", so this one reads the body on 503 too.
 */
async function getAuditHealth(): Promise<AuditHealth> {
  const controller = new AbortController();
  const timeout = window.setTimeout(() => controller.abort(), 30_000);
  let res: Response;
  try {
    res = await fetch("/api/audit/health", { signal: controller.signal });
  } catch (error) {
    if (error instanceof DOMException && error.name === "AbortError") throw new Error("請求逾時");
    throw error;
  } finally {
    window.clearTimeout(timeout);
  }
  if (res.status === 401) {
    notifyUnauthorized();
    throw new UnauthorizedError();
  }
  const body: unknown = await res.json().catch(() => null);
  if ((res.ok || res.status === 503) && isHealth(body)) return body;
  const message =
    body && typeof (body as { error?: unknown }).error === "string"
      ? (body as { error: string }).error
      : `${res.status}`;
  if (res.status === 403) throw new AuthError(403, message);
  throw new RequestError(res.status, message);
}

export const auditApi = {
  getAudit: ({ scope = "server", limit }: AuditQuery = {}) => {
    const params = new URLSearchParams();
    if (scope === "server") params.set("scope", "server");
    if (limit) params.set("limit", String(limit));
    const query = params.toString();
    return req<{ entries: AuditEntry[] }>(`/api/audit${query ? `?${query}` : ""}`);
  },
  getAuditHealth,
  /** States that every fallback through `expectedFallbackEvents` has been
   * reconciled. A 409 (ConflictError) means writes have not recovered yet
   * (`audit_writes_degraded`) or a newer fallback arrived
   * (`audit_fallback_count_changed`): refresh and look again. */
  acknowledgeAuditHealth: (expectedFallbackEvents: number) =>
    req<AuditHealth>("/api/audit/health/acknowledge", {
      method: "POST",
      body: JSON.stringify({ expectedFallbackEvents }),
    }),
  getAuditLimiter: () => req<AuditLimiterStats>("/api/audit/limiter"),
};
