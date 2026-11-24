import { useEffect, useRef, useState } from "react";
import { FitAddon } from "@xterm/addon-fit";
import { Terminal } from "@xterm/xterm";
import "@xterm/xterm/css/xterm.css";
import {
  connectTerminal,
  runFinal,
  type Run,
  type TerminalConnection,
  type TerminalState,
} from "../api";
import { useI18n } from "../i18n";
import {
  hasScreenMenuPrompt,
  keySequence,
  MENU_LINES_ABOVE,
  MENU_LINES_BELOW,
  terminalTheme,
  type TermKey,
} from "./terminalKeys";

export { keySequence, terminalTheme, type TermKey };

// Loaded on demand (see RunModeTabs / RunDialog): xterm is the bulk of this
// chunk and only interactive runs need it.

const PHONE_QUERY = "(max-width: 640px)";
const FONT_SIZE = { phone: 11, wide: 13 } as const;

const KEYS: { key: TermKey; label: string; wide?: boolean }[] = [
  { key: "esc", label: "Esc" },
  { key: "tab", label: "Tab" },
  { key: "shiftTab", label: "⇧Tab", wide: true },
  { key: "ctrlC", label: "Ctrl-C", wide: true },
  { key: "left", label: "←" },
  { key: "up", label: "↑" },
  { key: "down", label: "↓" },
  { key: "right", label: "→" },
  { key: "enter", label: "Enter", wide: true },
];

function binaryStringBytes(data: string): Uint8Array {
  const bytes = new Uint8Array(data.length);
  for (let index = 0; index < data.length; index += 1) bytes[index] = data.charCodeAt(index) & 0xff;
  return bytes;
}

/** Reads the visible tail lines of an xterm active buffer safely. */
/** The screen's lines around the cursor, and the cursor's index among them
 * (hasScreenMenuPrompt reads only that neighbourhood, §3.1). */
function readCursorLines(term: Terminal): { lines: string[]; cursorRow?: number } {
  const active = term.buffer?.active;
  if (!active || typeof active.getLine !== "function" || !active.length) return { lines: [] };
  const cursor =
    typeof active.baseY === "number" && typeof active.cursorY === "number"
      ? active.baseY + active.cursorY
      : active.length - 1;
  const row = Math.min(Math.max(cursor, 0), active.length - 1);
  const start = Math.max(0, row - MENU_LINES_ABOVE);
  const end = Math.min(active.length - 1, row + MENU_LINES_BELOW);
  const lines: string[] = [];
  for (let i = start; i <= end; i += 1) {
    const line = active.getLine(i);
    lines.push(line && typeof line.translateToString === "function" ? line.translateToString(true) : "");
  }
  return { lines, cursorRow: row - start };
}

export interface TerminalModeReader {
  bracketedPaste: () => boolean;
  applicationCursor: () => boolean;
}

export interface RunTerminalProps {
  run: Run;
  /** Exposes the live connection so RunComposer shares the same WebSocket. */
  onConnection?: (conn: TerminalConnection | null) => void;
  /** Reports changes to the connection's read-only state. */
  onReadonlyChange?: (readonly: boolean) => void;
  /** Reports whether the terminal screen currently shows a CLI menu/trust prompt. */
  onScreenAsksChange?: (asking: boolean) => void;
  /** Exposes readers for xterm's bracketedPasteMode and applicationCursorKeysMode. */
  onModes?: (reader: TerminalModeReader | null) => void;
  /** Hide the built-in phone key row when RunDialog supplies its own composer key row. */
  hideKeys?: boolean;
}

/**
 * An interactive run's terminal: the agent's own TUI, relayed from the PTY on
 * the runner. Typing goes back to it unless the hub says this viewer may only
 * watch, or the run has ended.
 */
export default function RunTerminal({
  run,
  onConnection,
  onReadonlyChange,
  onScreenAsksChange,
  onModes,
  hideKeys = false,
}: RunTerminalProps) {
  const { t } = useI18n();
  const host = useRef<HTMLDivElement>(null);
  const termRef = useRef<Terminal | null>(null);
  const connRef = useRef<TerminalConnection | null>(null);
  const [state, setState] = useState<TerminalState>("connecting");
  const [readonly, setReadonly] = useState(() => Boolean(run.viewOnly));
  const [liveStatus, setLiveStatus] = useState("");

  // Keep latest callbacks in refs so prop identity changes never reconnect the socket.
  const callbacksRef = useRef({ onConnection, onReadonlyChange, onScreenAsksChange, onModes });
  callbacksRef.current = { onConnection, onReadonlyChange, onScreenAsksChange, onModes };

  const final = runFinal(run.status) || runFinal(liveStatus);
  const status = runFinal(liveStatus) ? liveStatus : final ? run.status : liveStatus || run.status;
  const canType = state === "open" && !readonly && !final;

  // Read once at attach: whether this run had already ended decides whether
  // the socket retries, and later flips reach it through setFinal.
  const initialFinal = useRef(final);
  const initialViewOnly = useRef(Boolean(run.viewOnly));

  useEffect(() => {
    const element = host.current;
    if (!element) return;
    const phone = window.matchMedia?.(PHONE_QUERY);
    const fontSize = () => (phone?.matches ? FONT_SIZE.phone : FONT_SIZE.wide);
    element.style.setProperty("--run-term-font-size", `${fontSize()}px`);

    const term = new Terminal({
      fontFamily:
        getComputedStyle(document.documentElement).getPropertyValue("--mono").trim() ||
        "Consolas, monospace",
      fontSize: fontSize(),
      cursorBlink: true,
      scrollback: 5000,
      theme: terminalTheme(element),
      allowProposedApi: false,
    });
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.open(element);
    termRef.current = term;

    let lastScreenAsks = false;
    const inspectScreen = () => {
      const { lines, cursorRow } = readCursorLines(term);
      const next = hasScreenMenuPrompt(lines, cursorRow);
      if (next !== lastScreenAsks) {
        lastScreenAsks = next;
        callbacksRef.current.onScreenAsksChange?.(next);
      }
    };

    const conn = connectTerminal(
      run.id,
      run.project,
      {
        // The hub replays the scrollback on every attach: start from a blank
        // screen so a reconnect does not print it twice.
        onReset: () => {
          term.reset();
          if (lastScreenAsks) {
            lastScreenAsks = false;
            callbacksRef.current.onScreenAsksChange?.(false);
          }
        },
        onOutput: (data) => {
          // Pass a callback when xterm supports it; also inspect immediately for synchronous fakes.
          if (term.write.length >= 2) {
            term.write(data, inspectScreen);
          } else {
            term.write(data);
            inspectScreen();
          }
        },
        onStatus: setLiveStatus,
        onReadonly: (next) => {
          setReadonly(next);
          callbacksRef.current.onReadonlyChange?.(next);
        },
        onState: setState,
      },
      { final: initialFinal.current, viewOnly: initialViewOnly.current },
    );
    connRef.current = conn;
    callbacksRef.current.onConnection?.(conn);
    callbacksRef.current.onModes?.({
      bracketedPaste: () => Boolean(term.modes?.bracketedPasteMode),
      applicationCursor: () => Boolean(term.modes?.applicationCursorKeysMode),
    });

    const fitNow = () => {
      // A hidden tab measures 0×0; fitting to that would shrink the PTY.
      if (element.clientWidth === 0 || element.clientHeight === 0) return;
      try {
        fit.fit();
      } catch {
        /* not rendered yet */
      }
    };
    const disposables = [
      term.onData((data) => conn.sendInput(data)),
      term.onBinary((data) => conn.sendInput(binaryStringBytes(data))),
      term.onResize(({ cols, rows }) => conn.resize(cols, rows)),
    ];
    fitNow();
    conn.resize(term.cols, term.rows);

    let frame = 0;
    const observer =
      typeof ResizeObserver === "undefined"
        ? null
        : new ResizeObserver(() => {
            cancelAnimationFrame(frame);
            frame = requestAnimationFrame(fitNow);
          });
    observer?.observe(element);

    const onPhoneChange = () => {
      term.options.fontSize = fontSize();
      element.style.setProperty("--run-term-font-size", `${fontSize()}px`);
      fitNow();
    };
    phone?.addEventListener?.("change", onPhoneChange);

    const themeObserver = new MutationObserver(() => {
      term.options.theme = terminalTheme(element);
    });
    themeObserver.observe(document.documentElement, { attributes: true, attributeFilter: ["data-theme"] });

    return () => {
      cancelAnimationFrame(frame);
      observer?.disconnect();
      themeObserver.disconnect();
      phone?.removeEventListener?.("change", onPhoneChange);
      for (const disposable of disposables) disposable.dispose();
      callbacksRef.current.onConnection?.(null);
      callbacksRef.current.onModes?.(null);
      callbacksRef.current.onScreenAsksChange?.(false);
      conn.close();
      connRef.current = null;
      termRef.current = null;
      term.dispose();
    };
  }, [run.id, run.project]);

  useEffect(() => {
    connRef.current?.setFinal(final);
  }, [final]);

  useEffect(() => {
    if (run.viewOnly) {
      connRef.current?.setViewOnly(true);
      setReadonly(true);
    }
  }, [run.viewOnly]);

  useEffect(() => {
    const term = termRef.current;
    if (term) term.options.disableStdin = !canType;
  }, [canType]);

  const sendKey = (key: TermKey) => {
    const term = termRef.current;
    const sequence = keySequence(key, Boolean(term?.modes.applicationCursorKeysMode));
    connRef.current?.sendInput(sequence);
  };

  const connectionLabel = final
    ? t("terminal.state.ended")
    : t(`terminal.state.${state}`);

  return (
    <div className={`run-term${final ? " is-ended" : ""}`} data-state={state}>
      <div className="run-term-bar" role="status" aria-live="polite">
        <span className={`run-term-dot run-term-dot-${final ? "ended" : state}`} aria-hidden="true" />
        <span className="run-term-conn">{connectionLabel}</span>
        <span className="run-term-sep" aria-hidden="true">
          ·
        </span>
        <span className="run-term-status">{t("terminal.statusLine", { status: statusText(t, status) })}</span>
        {readonly && (
          <span className="run-term-readonly" title={t("terminal.readonlyHint")}>
            {t("terminal.readonly")}
          </span>
        )}
      </div>
      <div className="run-term-screen">
        {/* Scrolls sideways on a phone, so a TUI keeps a usable width
            without the page itself overflowing. */}
        <div className="run-term-scroll">
          <div
            ref={host}
            className="run-term-host"
            role="region"
            aria-label={t("terminal.label")}
            data-testid="run-terminal"
          />
        </div>
        {final && (
          <div className="run-term-ended" role="status">
            {t("terminal.ended", { status: statusText(t, status) })}
          </div>
        )}
      </div>
      {!hideKeys && (
        <div className="run-term-keys" role="toolbar" aria-label={t("terminal.keys")}>
          {KEYS.map(({ key, label, wide }) => (
            <button
              key={key}
              type="button"
              className={wide ? "wide" : undefined}
              disabled={!canType}
              aria-label={t(`terminal.key.${key}`)}
              // Keep the focus (and a phone's keyboard) on the terminal.
              onPointerDown={(event) => event.preventDefault()}
              onClick={() => sendKey(key)}
            >
              {label}
            </button>
          ))}
        </div>
      )}
    </div>
  );
}

const KNOWN = new Set(["queued", "offered", "declined", "running", "succeeded", "failed", "cancelled"]);

function statusText(t: (key: string) => string, status: string): string {
  return KNOWN.has(status) ? t(`runs.status.${status}`) : status;
}
