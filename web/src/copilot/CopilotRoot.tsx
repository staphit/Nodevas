/**
 * Mounted once by App: the floating mic button, the panel, the global
 * shortcuts (Ctrl+Shift+Space toggles the panel, hold Alt+Space to talk) and
 * the speech wiring shared by all of them. Renders nothing when the server has
 * no copilot.
 */

import { Suspense, useCallback, useEffect, useRef } from "react";
import { languageLocale, useI18n } from "../i18n";
import { lazyWithRetry } from "../lazyWithRetry";
import { useApp } from "../store";
import { useCopilot } from "./copilotStore";
import { parseVoiceDecision } from "./decisions";
import { IconMic } from "./icons";
import { OPEN_KEYS, RunnerHelp } from "./RunnerHelp";
import { decide, openUnread, projectChanged, refreshCopilotStatus, sendTurn } from "./session";
import { cancelSpeech, useSpeechRecognition, useSpeechSynthesis } from "./speech";

// The panel (and the markdown renderer it pulls in) loads on first open.
const CopilotPanel = lazyWithRetry(() =>
  import("./CopilotPanel").then((module) => ({ default: module.CopilotPanel })),
);

/** Editable targets other than the copilot input keep Alt+Space for themselves. */
function isForeignEditable(target: EventTarget | null): boolean {
  const element = target as HTMLElement | null;
  if (!element || typeof element.closest !== "function") return false;
  if (element.matches?.("textarea[data-copilot-input]")) return false;
  return Boolean(
    element.isContentEditable ||
      element.closest("input, textarea, select, [contenteditable='true'], .cm-editor"),
  );
}

export function CopilotRoot() {
  const { language, t } = useI18n();
  const serverState = useCopilot((s) => s.serverState);
  const open = useCopilot((s) => s.open);
  const setOpen = useCopilot((s) => s.setOpen);
  const running = useCopilot((s) => s.running);
  const lastReply = useCopilot((s) => s.lastReply);
  const speakReplies = useCopilot((s) => s.speakReplies);
  const firstPending = useCopilot((s) => s.pending.find((request) => !request.submitting)?.requestId);
  const activeProject = useApp((s) => s.activeProject);
  // A follow-up (a plan run ended) waits in one of this project's conversations.
  const unread = useCopilot((s) => s.conversations.some((conversation) => conversation.unread));
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const fabRef = useRef<HTMLButtonElement>(null);
  const returnFocusRef = useRef<HTMLElement | null>(null);
  const lang = languageLocale(language);

  // A final transcript answers a pending permission request if there is one,
  // and is a new turn otherwise.
  const onFinal = useCallback(
    (text: string) => {
      const copilot = useCopilot.getState();
      const request = copilot.pending.find((item) => !item.submitting);
      if (request) {
        const decision = parseVoiceDecision(text);
        if (decision) void decide(request.requestId, decision);
        else copilot.setNotice(t("copilot.permission.voiceUnclear", { text }));
        return;
      }
      if (copilot.running) {
        copilot.setNotice(t("copilot.busy"));
        return;
      }
      copilot.setOpen(true);
      void sendTurn(text, "voice");
    },
    [t],
  );
  const speech = useSpeechRecognition({ lang, onFinal });
  const tts = useSpeechSynthesis(lang);
  const speak = tts.speak;

  useEffect(() => {
    void refreshCopilotStatus();
  }, []);

  useEffect(() => {
    if (serverState === "ready" && activeProject) projectChanged();
  }, [serverState, activeProject]);

  // Spoken replies: the final text of each turn; a new turn cancels the old one.
  const spokenRef = useRef<string | null>(null);
  useEffect(() => {
    if (!lastReply || spokenRef.current === lastReply.turnId) return;
    spokenRef.current = lastReply.turnId;
    if (speakReplies) speak(lastReply.text);
  }, [lastReply, speakReplies, speak]);
  useEffect(() => {
    if (running || !speakReplies) cancelSpeech();
  }, [running, speakReplies]);

  // A turn started by voice is answered by voice: listen for the decision.
  useEffect(() => {
    if (!firstPending || !speech.supported || speech.listening) return;
    if (useCopilot.getState().lastSource !== "voice") return;
    speech.start();
    // Only when a new request appears, not whenever listening stops.
  }, [firstPending]);

  // The panel focuses its composer when it mounts; give focus back on close.
  useEffect(() => {
    if (!open) return;
    returnFocusRef.current = document.activeElement as HTMLElement | null;
    return () => {
      const target = returnFocusRef.current;
      returnFocusRef.current = null;
      if (target && target.isConnected && target !== document.body) target.focus();
      else fabRef.current?.focus();
    };
  }, [open]);

  // Global shortcuts.
  const speechRef = useRef(speech);
  speechRef.current = speech;
  useEffect(() => {
    if (serverState !== "ready") return;
    let talking = false;
    const onKeyDown = (event: KeyboardEvent) => {
      const space = event.code === "Space" || event.key === " ";
      if (!space) return;
      if ((event.ctrlKey || event.metaKey) && event.shiftKey && !event.altKey) {
        event.preventDefault();
        if (!event.repeat) useCopilot.getState().toggleOpen();
        return;
      }
      if (event.altKey && !event.ctrlKey && !event.metaKey && !event.shiftKey) {
        if (isForeignEditable(event.target)) return;
        event.preventDefault();
        if (event.repeat || talking) return;
        useCopilot.getState().setOpen(true);
        const current = speechRef.current;
        if (!current.supported) {
          inputRef.current?.focus();
          return;
        }
        talking = true;
        if (!current.listening) current.start();
      }
    };
    const onKeyUp = (event: KeyboardEvent) => {
      if (!talking) return;
      if (event.code === "Space" || event.key === " " || event.key === "Alt") {
        talking = false;
        speechRef.current.stop();
      }
    };
    const onBlur = () => {
      if (talking) {
        talking = false;
        speechRef.current.stop();
      }
    };
    window.addEventListener("keydown", onKeyDown);
    window.addEventListener("keyup", onKeyUp);
    window.addEventListener("blur", onBlur);
    return () => {
      window.removeEventListener("keydown", onKeyDown);
      window.removeEventListener("keyup", onKeyUp);
      window.removeEventListener("blur", onBlur);
    };
  }, [serverState]);

  // On, but without a model yet: this person's runner (which holds the key)
  // is offline, the browser's model still has to be downloaded, or nothing
  // free is set up. The button stays, and opens what to do about it.
  const waitingKey = OPEN_KEYS[serverState];
  if (waitingKey) {
    return open ? (
      <RunnerHelp onClose={() => setOpen(false)} />
    ) : (
      <button
        ref={fabRef}
        type="button"
        className="copilot-fab is-offline"
        onClick={() => setOpen(true)}
        aria-label={`${t("copilot.fab.label")} · ${t(waitingKey)}`}
        title={t(waitingKey)}
      >
        <IconMic size={20} />
        <span className="copilot-fab-label">{t("copilot.fab.label")}</span>
      </button>
    );
  }
  if (serverState !== "ready") return null;

  return (
    <>
      {!open && (
        <button
          ref={fabRef}
          type="button"
          className={`copilot-fab${speech.listening ? " is-listening" : ""}${running ? " is-running" : ""}`}
          onClick={() => (unread ? void openUnread() : setOpen(true))}
          aria-label={`${t("copilot.fab.label")} · ${
            unread ? `${t("copilot.open")} · ${t("copilot.followup.unread")}` : t("copilot.open")
          }`}
          aria-expanded={false}
          aria-controls="copilot-panel"
          title={[
            t("common.withHint", { label: t("copilot.open"), hint: "Ctrl+Shift+Space" }),
            t("common.withHint", { label: t("copilot.pushToTalk"), hint: "Alt+Space" }),
          ].join(" · ")}
        >
          <IconMic size={20} />
          <span className="copilot-fab-label">{t("copilot.fab.label")}</span>
          {unread && <span className="copilot-fab-badge" aria-hidden data-testid="copilot-unread" />}
        </button>
      )}
      {open && (
        <Suspense fallback={null}>
          <CopilotPanel
            ref={inputRef}
            speech={speech}
            ttsSupported={tts.supported}
            onClose={() => setOpen(false)}
          />
        </Suspense>
      )}
    </>
  );
}
