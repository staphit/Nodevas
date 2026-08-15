import type { Verifier } from "./verify";

/** Optimistic-lock failure: server refused to overwrite a newer disk version. */
export class ConflictError extends Error {
  diskRev: string;
  diskContent: string;
  constructor(diskRev: string, diskContent: string) {
    super("conflict");
    this.diskRev = diskRev;
    this.diskContent = diskContent;
  }
}

/** Raised when the server has accounts on and this browser is not signed in. */
export class UnauthorizedError extends Error {
  constructor() {
    super("需要登入");
  }
}

/**
 * A sign-in failure carries the status code because the form reacts to it:
 * 401 means the PIN or the passcode was wrong, 429 means throttled, 503 means
 * the server has no mail transport and no passcode can ever arrive.
 */
export class AuthError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

/** Listeners notified when a request comes back 401, so the app can show the
 * login screen instead of a wall of failures. */
const unauthorizedListeners = new Set<() => void>();

export function onUnauthorized(listener: () => void): () => void {
  unauthorizedListeners.add(listener);
  return () => unauthorizedListeners.delete(listener);
}

/** For the few calls that cannot go through req() — the file downloads, whose
 * reply is not JSON — and still have to behave like every other call on 401. */
export function notifyUnauthorized(): void {
  for (const listener of unauthorizedListeners) listener();
}

/** Reads the CSRF cookie the login response set. It is deliberately not
 * HttpOnly: the check is that its value comes back in a header, which a
 * cross-site page cannot set. */
export function csrfToken(): string {
  const match = /(?:^|;\s*)nodevas_csrf=([^;]*)/.exec(document.cookie);
  return match ? decodeURIComponent(match[1]) : "";
}

const unsafeMethods = new Set(["POST", "PUT", "PATCH", "DELETE"]);

/**
 * When set, every project-scoped request carries this project name in the
 * X-Nodevas-Project header, and the server answers about that project instead
 * of its own active one.
 *
 * This exists for sessions that may not move the server's active project —
 * switching it is a write to state every other browser shares, so a visitor
 * (and a member, on an admin-only route) is refused. Refusing them the READ
 * would be wrong though: which project this browser is looking at is this
 * browser's business, and the server already resolves it per request. An
 * explicit ?project= in a URL still wins; the server checks the query first.
 */
let projectOverride = "";

export function setProjectOverride(name: string): void {
  projectOverride = name;
}

/**
 * `verify` is opt-in per endpoint. The Go structs and `types.ts` are hand-synced
 * with nothing that fails a build when they drift, so the responses the whole
 * app is built on are checked here rather than surfacing as an `undefined` deep
 * in a component with nothing pointing back at the rename that caused it.
 */
export async function req<T>(
  url: string,
  init?: RequestInit,
  verify?: Verifier<T>,
): Promise<T> {
  const headers = new Headers(init?.headers);
  if (projectOverride && !headers.has("X-Nodevas-Project")) {
    headers.set("X-Nodevas-Project", projectOverride);
  }
  if (
    init?.body != null &&
    !(init.body instanceof FormData) &&
    !headers.has("Content-Type")
  ) {
    headers.set("Content-Type", "application/json");
  }
  const method = (init?.method ?? "GET").toUpperCase();
  if (unsafeMethods.has(method)) {
    const token = csrfToken();
    if (token) headers.set("X-CSRF-Token", token);
  }
  const controller = new AbortController();
  const timeout = window.setTimeout(() => controller.abort(), 30_000);
  let res: Response;
  try {
    res = await fetch(url, {
      ...init,
      headers,
      signal: init?.signal ?? controller.signal,
    });
  } catch (error) {
    if (error instanceof DOMException && error.name === "AbortError") {
      throw new Error("請求逾時");
    }
    throw error;
  } finally {
    window.clearTimeout(timeout);
  }
  if (res.status === 401) {
    notifyUnauthorized();
    throw new UnauthorizedError();
  }
  if (res.status === 409) {
    const body = await res.json().catch(() => ({}));
    throw new ConflictError(body.diskRev ?? "", body.diskContent ?? "");
  }
  if (!res.ok) {
    let msg = `${res.status}`;
    try {
      const body = await res.json();
      if (body.error) msg = body.error;
    } catch {
      /* keep status text */
    }
    // 403 keeps its status on the error. "You may not" is a fact callers act
    // on — the project switch falls back to per-request scoping on it — and a
    // bare message string cannot be told apart from any other failure.
    if (res.status === 403) throw new AuthError(403, msg);
    throw new Error(msg);
  }
  const body = await res.json();
  return verify ? verify(body, url) : (body as T);
}

/**
 * Sign-in posts deliberately bypass req(). Its 401 branch replaces the
 * server's text with "需要登入" and notifies the unauthorized listeners, which
 * is right for a session that expired mid-session but wrong here: there is no
 * session yet, and the form needs the server's own wording plus the status.
 */
export async function authPost<T>(url: string, body: unknown): Promise<T> {
  const headers = new Headers({ "Content-Type": "application/json" });
  // Sent when present; before the first sign-in there is no CSRF cookie yet.
  const token = csrfToken();
  if (token) headers.set("X-CSRF-Token", token);
  const controller = new AbortController();
  const timeout = window.setTimeout(() => controller.abort(), 30_000);
  let res: Response;
  try {
    res = await fetch(url, {
      method: "POST",
      headers,
      body: JSON.stringify(body),
      signal: controller.signal,
    });
  } catch (error) {
    if (error instanceof DOMException && error.name === "AbortError") {
      throw new AuthError(0, "請求逾時");
    }
    throw new AuthError(0, "無法連線到伺服器");
  } finally {
    window.clearTimeout(timeout);
  }
  if (!res.ok) {
    let message = `${res.status}`;
    try {
      const parsed = await res.json();
      if (parsed && typeof parsed.error === "string" && parsed.error !== "") {
        message = parsed.error;
      }
    } catch {
      /* a non-JSON body leaves the status code as the message */
    }
    throw new AuthError(res.status, message);
  }
  return res.json() as Promise<T>;
}
