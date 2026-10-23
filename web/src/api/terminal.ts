import { getProjectOverride, notifyUnauthorized } from "./http";
import { runFinal } from "./runs";

// Mirrors internal/runnerwire/terminal.go (W2.5). An interactive run's agent
// TUI runs in a PTY on the runner; the hub relays its bytes to every browser
// attached to GET /api/runs/:id/terminal (WebSocket):
//
//   hub → browser  binary  terminal output; the scrollback first, on every attach
//   hub → browser  text    {"type":"status","status":"running"} | {"type":"readonly"}
//   browser → hub  binary  keystrokes (UTF-8)
//   browser → hub  text    {"type":"resize","cols":120,"rows":40}

/** runnerwire.MaxTermChunk: one input chunk at most. */
export const MAX_TERM_CHUNK = 64 << 10;
/** runnerwire.MaxTermCols / MaxTermRows: a resize is clamped to these. */
export const MAX_TERM_COLS = 500;
export const MAX_TERM_ROWS = 200;

export type TerminalState = "connecting" | "open" | "reconnecting" | "closed";

export interface TerminalHandlers {
  /** A (re)attach: the hub replays the scrollback next, onto an empty screen. */
  onReset: () => void;
  onOutput: (data: Uint8Array) => void;
  onStatus?: (status: string) => void;
  /** True when the hub says this viewer may watch but not type. */
  onReadonly?: (readonly: boolean) => void;
  onState?: (state: TerminalState) => void;
}

export interface TerminalOptions {
  /** The run is already final: attach once for the scrollback, never retry. */
  final?: boolean;
  /** How long a resize waits for the next one before it is sent. */
  resizeDelayMs?: number;
}

export interface TerminalConnection {
  /** Sends keystrokes; false when they cannot be (closed, read-only, ended). */
  sendInput: (data: string | Uint8Array) => boolean;
  /** Reports the terminal's size; debounced, and repeated after a reconnect. */
  resize: (cols: number, rows: number) => void;
  /** The run reached a final status: stop reconnecting. */
  setFinal: (final: boolean) => void;
  close: () => void;
}

/** The terminal socket's URL. A WebSocket carries no custom headers, so the
 * project goes in the query (the server reads ?project= before the header the
 * REST calls send). */
export function terminalSocketURL(runId: string, project = ""): string {
  const proto = location.protocol === "https:" ? "wss" : "ws";
  const scope = project || getProjectOverride();
  const query = scope ? `?project=${encodeURIComponent(scope)}` : "";
  return `${proto}://${location.host}/api/runs/${encodeURIComponent(runId)}/terminal${query}`;
}

const encoder = new TextEncoder();

function clamp(value: number, max: number): number {
  return Math.max(1, Math.min(max, Math.floor(value) || 1));
}

/**
 * Attaches to a run's terminal; reconnects with backoff while the run is not
 * final. Every attach starts with onReset, then the hub's scrollback replay.
 */
export function connectTerminal(
  runId: string,
  project: string,
  handlers: TerminalHandlers,
  options: TerminalOptions = {},
): TerminalConnection {
  const resizeDelay = options.resizeDelayMs ?? 120;
  let ws: WebSocket | null = null;
  let closed = false;
  let final = Boolean(options.final);
  let readonly = false;
  let retry = 500;
  let retryTimer: ReturnType<typeof setTimeout> | null = null;
  let resizeTimer: ReturnType<typeof setTimeout> | null = null;
  let size: { cols: number; rows: number } | null = null;
  let sentSize = "";

  const setState = (state: TerminalState) => handlers.onState?.(state);
  const isOpen = () => ws !== null && ws.readyState === WebSocket.OPEN;

  const sendSize = () => {
    // A watcher's window must not resize the PTY everyone else is using.
    if (!size || !isOpen() || readonly || final) return;
    const key = `${size.cols}x${size.rows}`;
    if (key === sentSize) return;
    sentSize = key;
    ws!.send(JSON.stringify({ type: "resize", cols: size.cols, rows: size.rows }));
  };

  const open = () => {
    retryTimer = null;
    if (closed) return;
    setState(retry === 500 && !ws ? "connecting" : "reconnecting");
    const socket = new WebSocket(terminalSocketURL(runId, project));
    socket.binaryType = "arraybuffer";
    ws = socket;
    socket.onopen = () => {
      if (ws !== socket) return;
      retry = 500;
      sentSize = "";
      readonly = false;
      handlers.onReadonly?.(false);
      handlers.onReset();
      setState("open");
      sendSize();
    };
    socket.onmessage = (message: MessageEvent) => {
      if (ws !== socket) return;
      const data = message.data;
      if (typeof data === "string") {
        let control: { type?: string; status?: string };
        try {
          control = JSON.parse(data);
        } catch {
          return;
        }
        if (control.type === "readonly") {
          readonly = true;
          handlers.onReadonly?.(true);
        } else if (control.type === "status" && typeof control.status === "string") {
          if (runFinal(control.status)) final = true;
          handlers.onStatus?.(control.status);
        }
        return;
      }
      // Checked by tag, not instanceof: a buffer from another realm (a test
      // environment, a worker) is still a buffer.
      if (Object.prototype.toString.call(data) === "[object ArrayBuffer]") {
        handlers.onOutput(new Uint8Array(data as ArrayBuffer));
      } else if (ArrayBuffer.isView(data)) {
        handlers.onOutput(new Uint8Array(data.buffer, data.byteOffset, data.byteLength));
      }
    };
    socket.onclose = (event: CloseEvent) => {
      if (ws !== socket) return;
      ws = null;
      if (closed) return;
      // StatusPolicyViolation: the session expired or the origin is refused;
      // retrying in a loop would not change that.
      if (event.code === 1008) {
        if (event.reason?.includes("authorization expired")) notifyUnauthorized();
        setState("closed");
        return;
      }
      if (final) {
        setState("closed");
        return;
      }
      setState("reconnecting");
      retry = Math.min(retry * 1.5, 10_000);
      const delay = Math.round(retry * (0.8 + Math.random() * 0.4));
      retryTimer = setTimeout(open, delay);
    };
  };

  open();

  return {
    sendInput: (data) => {
      if (!isOpen() || readonly || final) return false;
      const bytes = typeof data === "string" ? encoder.encode(data) : data;
      for (let at = 0; at < bytes.length; at += MAX_TERM_CHUNK) {
        ws!.send(bytes.subarray(at, at + MAX_TERM_CHUNK));
      }
      return true;
    },
    resize: (cols, rows) => {
      size = { cols: clamp(cols, MAX_TERM_COLS), rows: clamp(rows, MAX_TERM_ROWS) };
      if (resizeTimer) clearTimeout(resizeTimer);
      resizeTimer = setTimeout(() => {
        resizeTimer = null;
        sendSize();
      }, resizeDelay);
    },
    setFinal: (next) => {
      final = final || next;
      if (final && retryTimer) {
        clearTimeout(retryTimer);
        retryTimer = null;
        if (!ws) setState("closed");
      }
    },
    close: () => {
      closed = true;
      if (retryTimer) clearTimeout(retryTimer);
      if (resizeTimer) clearTimeout(resizeTimer);
      retryTimer = resizeTimer = null;
      const socket = ws;
      ws = null;
      socket?.close();
    },
  };
}
