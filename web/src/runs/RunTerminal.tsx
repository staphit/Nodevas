import { useEffect, useRef, useState } from "react";
import { FitAddon } from "@xterm/addon-fit";
import { Terminal, type ITheme } from "@xterm/xterm";
import "@xterm/xterm/css/xterm.css";
import {
  connectTerminal,
  runFinal,
  type Run,
  type TerminalConnection,
  type TerminalState,
} from "../api";
import { useI18n } from "../i18n";

// Loaded on demand (see RunModeTabs): xterm is the bulk of this chunk and
// only interactive runs need it.

const PHONE_QUERY = "(max-width: 640px)";
const FONT_SIZE = { phone: 11, wide: 13 } as const;

// ANSI colours tuned per background: the stock xterm palette assumes a dark
// screen, and its yellow and white vanish on a light one.
const DARK_ANSI: ITheme = {
  black: "#2b3640",
  red: "#e08a88",
  green: "#7fbd85",
  yellow: "#d9a441",
  blue: "#6cb2e0",
  magenta: "#c79be8",
  cyan: "#62b2c1",
  white: "#d5dde3",
  brightBlack: "#6f7d88",
  brightRed: "#f0a5a3",
  brightGreen: "#9bd6a0",
  brightYellow: "#ecc06a",
  brightBlue: "#8fc8ee",
  brightMagenta: "#dbb8f3",
  brightCyan: "#8dd0dc",
  brightWhite: "#f4f7f9",
};
const LIGHT_ANSI: ITheme = {
  black: "#17212b",
  red: "#b3302d",
  green: "#2f6f34",
  yellow: "#8a5d00",
  blue: "#1f5f9f",
  magenta: "#7340c4",
  cyan: "#1d6d7c",
  white: "#8e99a3",
  brightBlack: "#5a6672",
  brightRed: "#cf4643",
  brightGreen: "#3b8541",
  brightYellow: "#a06c00",
  brightBlue: "#2a71b8",
  brightMagenta: "#8a55d9",
  brightCyan: "#258497",
  brightWhite: "#66727f",
};

function token(style: CSSStyleDeclaration, name: string, fallback: string): string {
  return style.getPropertyValue(name).trim() || fallback;
}

/** The terminal's colours from the app's CSS tokens, for the current theme. */
export function terminalTheme(): ITheme {
  const dark = document.documentElement.dataset.theme === "dark";
  const style = getComputedStyle(document.documentElement);
  const accent = token(style, "--accent", dark ? "#62b2c1" : "#28798a");
  return {
    ...(dark ? DARK_ANSI : LIGHT_ANSI),
    background: token(style, "--surface-2", dark ? "#171d22" : "#f7f9fb"),
    foreground: token(style, "--text", dark ? "#e7edf1" : "#17212b"),
    cursor: accent,
    cursorAccent: token(style, "--surface-2", dark ? "#171d22" : "#f7f9fb"),
    selectionBackground: /^#[0-9a-f]{6}$/i.test(accent) ? `${accent}55` : accent,
  };
}

/** Bytes for the keys a phone keyboard lacks. Arrows follow the terminal's
 * cursor-key mode, as a real keyboard's would. */
export function keySequence(key: TermKey, applicationCursor: boolean): string {
  const arrow = (code: string) => (applicationCursor ? `\x1bO${code}` : `\x1b[${code}`);
  switch (key) {
    case "esc":
      return "\x1b";
    case "tab":
      return "\t";
    case "shiftTab":
      return "\x1b[Z";
    case "ctrlC":
      return "\x03";
    case "up":
      return arrow("A");
    case "down":
      return arrow("B");
    case "right":
      return arrow("C");
    case "left":
      return arrow("D");
    case "enter":
      return "\r";
  }
}

export type TermKey =
  | "esc"
  | "tab"
  | "shiftTab"
  | "ctrlC"
  | "left"
  | "up"
  | "down"
  | "right"
  | "enter";

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

/**
 * An interactive run's terminal: the agent's own TUI, relayed from the PTY on
 * the runner. Typing goes back to it unless the hub says this viewer may only
 * watch, or the run has ended.
 */
export default function RunTerminal({ run }: { run: Run }) {
  const { t } = useI18n();
  const host = useRef<HTMLDivElement>(null);
  const termRef = useRef<Terminal | null>(null);
  const connRef = useRef<TerminalConnection | null>(null);
  const [state, setState] = useState<TerminalState>("connecting");
  const [readonly, setReadonly] = useState(false);
  const [liveStatus, setLiveStatus] = useState("");

  const final = runFinal(run.status) || runFinal(liveStatus);
  const status = runFinal(liveStatus) ? liveStatus : final ? run.status : liveStatus || run.status;
  const canType = state === "open" && !readonly && !final;

  // Read once at attach: whether this run had already ended decides whether
  // the socket retries, and later flips reach it through setFinal.
  const initialFinal = useRef(final);

  useEffect(() => {
    const element = host.current;
    if (!element) return;
    const phone = window.matchMedia?.(PHONE_QUERY);
    const fontSize = () => (phone?.matches ? FONT_SIZE.phone : FONT_SIZE.wide);
    element.style.setProperty("--run-term-font-size", `${fontSize()}px`);

    const term = new Terminal({
      fontFamily: getComputedStyle(document.documentElement).getPropertyValue("--mono").trim() ||
        "Consolas, monospace",
      fontSize: fontSize(),
      cursorBlink: true,
      scrollback: 5000,
      theme: terminalTheme(),
      allowProposedApi: false,
    });
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.open(element);
    termRef.current = term;

    const conn = connectTerminal(
      run.id,
      run.project,
      {
        // The hub replays the scrollback on every attach: start from a blank
        // screen so a reconnect does not print it twice.
        onReset: () => term.reset(),
        onOutput: (data) => term.write(data),
        onStatus: setLiveStatus,
        onReadonly: setReadonly,
        onState: setState,
      },
      { final: initialFinal.current },
    );
    connRef.current = conn;

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
      term.options.theme = terminalTheme();
    });
    themeObserver.observe(document.documentElement, { attributes: true, attributeFilter: ["data-theme"] });

    return () => {
      cancelAnimationFrame(frame);
      observer?.disconnect();
      themeObserver.disconnect();
      phone?.removeEventListener?.("change", onPhoneChange);
      for (const disposable of disposables) disposable.dispose();
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
    </div>
  );
}

const KNOWN = new Set(["queued", "offered", "declined", "running", "succeeded", "failed", "cancelled"]);

function statusText(t: (key: string) => string, status: string): string {
  return KNOWN.has(status) ? t(`runs.status.${status}`) : status;
}
