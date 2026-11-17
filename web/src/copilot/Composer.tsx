/**
 * Text input, push-to-talk mic and send/stop. Enter sends, Shift+Enter breaks
 * the line; an IME composition (zhuyin, pinyin) keeps Enter for itself.
 *
 * Typing comes first: the switch above the box says what sending does, "Make
 * nodes" (a draft to preview; the default) or "Ask" (the chat). With an open
 * draft on screen, Make nodes changes that draft, and Enter in the empty box
 * creates it. Speech fills the box to check, unless the person chose to send
 * when they stop talking.
 */

import { forwardRef, useEffect, useLayoutEffect, useRef, useState, type KeyboardEvent, type PointerEvent } from "react";
import { useI18n } from "../i18n";
import { canvasSelectionIds } from "../state/documentSlice";
import { useApp } from "../store";
import { activeDraftId, makePlaceholderKey, useCopilot, type CopilotIntent } from "./copilotStore";
import { canCreate, createDraft } from "./draft/draftApi";
import { IconMic, IconSend, IconStop } from "./icons";
import type { SpeechRecognitionState } from "./speech";

/** The box grows with its text up to this many lines, then scrolls. */
export const COMPOSER_MAX_ROWS = 8;

/** A press held longer than this is push-to-talk; shorter is a toggle click. */
const HOLD_MS = 350;

export const Composer = forwardRef<
  HTMLTextAreaElement,
  {
    running: boolean;
    speech: SpeechRecognitionState;
    onSend: (text: string) => void;
    onStop: () => void;
  }
>(function Composer({ running, speech, onSend, onStop }, ref) {
  const { t } = useI18n();
  const [text, setText] = useState("");
  const [fromVoice, setFromVoice] = useState(false);
  const pressRef = useRef<{ at: number; started: boolean } | null>(null);
  const boxRef = useRef<HTMLTextAreaElement | null>(null);
  const intent = useCopilot((state) => state.intent);
  const setIntent = useCopilot((state) => state.setIntent);
  const voiceAutoSend = useCopilot((state) => state.voiceAutoSend);
  const setVoiceAutoSend = useCopilot((state) => state.setVoiceAutoSend);
  const placeholderKey = useCopilot((state) =>
    state.intent === "ask" ? "copilot.placeholder.ask" : makePlaceholderKey(state),
  );
  const draftId = useCopilot((state) => (state.intent === "make" ? activeDraftId(state) : null));
  const selected = useApp((state) => canvasSelectionIds(state).length);
  // Text handed over from elsewhere (a draft's question chip, an example, a
  // spoken request) lands in the box, editable, and is not sent.
  const fill = useCopilot((state) => state.composerFill);
  useEffect(() => {
    if (!fill) return;
    setText(fill.text);
    setFromVoice(Boolean(fill.voice));
    boxRef.current?.focus();
  }, [fill]);

  // Grow with the text, up to COMPOSER_MAX_ROWS lines.
  useLayoutEffect(() => {
    const box = boxRef.current;
    if (!box) return;
    const style = getComputedStyle(box);
    const line = parseFloat(style.lineHeight) || 20;
    const padding = (parseFloat(style.paddingTop) || 0) + (parseFloat(style.paddingBottom) || 0);
    box.style.height = "auto";
    const wanted = box.scrollHeight;
    if (wanted > 0) box.style.height = `${Math.min(wanted, line * COMPOSER_MAX_ROWS + padding)}px`;
  }, [text]);

  const send = () => {
    const value = text.trim();
    if (!value || running) return;
    onSend(value);
    setText("");
    setFromVoice(false);
  };

  const onKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    if (event.key !== "Enter" || event.shiftKey || event.nativeEvent.isComposing) return;
    event.preventDefault();
    // Enter in the empty box creates the open draft, once it can be.
    if (!text.trim() && draftId && !running && canCreate(draftId)) {
      void createDraft(draftId);
      return;
    }
    send();
  };

  const chooseIntent = (next: CopilotIntent) => {
    setIntent(next);
    boxRef.current?.focus();
  };

  const onPointerDown = (event: PointerEvent<HTMLButtonElement>) => {
    if (!speech.supported || event.button !== 0) return;
    if (speech.listening) {
      speech.stop();
      pressRef.current = { at: Date.now(), started: false };
      return;
    }
    speech.start({ continuous: true });
    pressRef.current = { at: Date.now(), started: true };
  };

  const onPointerUp = () => {
    const press = pressRef.current;
    pressRef.current = null;
    if (press?.started && Date.now() - press.at >= HOLD_MS) speech.stop();
  };

  const micLabel = !speech.supported
    ? t("copilot.speech.unsupported")
    : speech.listening
      ? t("copilot.mic.stop")
      : t("copilot.mic.hold");

  const setRefs = (node: HTMLTextAreaElement | null) => {
    boxRef.current = node;
    if (typeof ref === "function") ref(node);
    else if (ref) ref.current = node;
  };

  return (
    <div className="copilot-composer-wrap">
      <div className="copilot-composer-top">
        <div className="copilot-intent" role="group" aria-label={t("copilot.intent.label")}>
          {(["make", "ask"] as const).map((value) => (
            <button
              key={value}
              type="button"
              className={`copilot-intent-option${intent === value ? " is-active" : ""}`}
              aria-pressed={intent === value}
              onClick={() => chooseIntent(value)}
            >
              {t(value === "make" ? "copilot.intent.make" : "copilot.intent.ask")}
            </button>
          ))}
        </div>
        {intent === "make" && selected > 0 && (
          <span className="copilot-selection-chip">{t("copilot.selectionChip", { n: selected })}</span>
        )}
        {fromVoice && text.trim() && <span className="copilot-voice-review">{t("copilot.voice.review")}</span>}
        {speech.supported && (
          <label className="copilot-autosend">
            <input
              type="checkbox"
              checked={voiceAutoSend}
              onChange={(event) => setVoiceAutoSend(event.target.checked)}
            />
            {t("copilot.voice.autoSend")}
          </label>
        )}
      </div>
      <div className="copilot-composer">
        <textarea
          ref={setRefs}
          data-copilot-input
          className={`copilot-input${fromVoice && text.trim() ? " is-from-voice" : ""}`}
          rows={2}
          value={text}
          placeholder={t(placeholderKey)}
          aria-label={t("copilot.input.label")}
          onChange={(event) => setText(event.target.value)}
          onKeyDown={onKeyDown}
        />
        <div className="copilot-composer-actions">
          {/* Disabled controls swallow hover, so the reason lives on a wrapper. */}
          <span className="copilot-mic-wrap" title={micLabel}>
            <button
              type="button"
              className={`copilot-mic${speech.listening ? " is-listening" : ""}`}
              disabled={!speech.supported}
              aria-label={micLabel}
              aria-pressed={speech.listening}
              onPointerDown={onPointerDown}
              onPointerUp={onPointerUp}
              onPointerCancel={onPointerUp}
              onPointerLeave={() => {
                if (pressRef.current?.started) onPointerUp();
              }}
              onClick={(event) => {
                // Keyboard activation (Enter/Space) has no pointer: toggle.
                if (event.detail !== 0) return;
                if (speech.listening) speech.stop();
                else speech.start({ continuous: true });
              }}
            >
              <IconMic size={17} />
            </button>
          </span>
          {running ? (
            <button
              type="button"
              className="copilot-stop"
              onClick={onStop}
              aria-label={t("copilot.stop")}
              title={t("copilot.stop")}
            >
              <IconStop size={16} />
            </button>
          ) : (
            <button
              type="button"
              className="copilot-send primary"
              onClick={send}
              disabled={!text.trim()}
              aria-label={t("copilot.send")}
              title={t("copilot.send")}
            >
              <IconSend size={16} />
            </button>
          )}
        </div>
      </div>
    </div>
  );
});
