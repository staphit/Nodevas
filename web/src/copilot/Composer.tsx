/**
 * Text input, push-to-talk mic and send/stop. Enter sends, Shift+Enter breaks
 * the line; an IME composition (zhuyin, pinyin) keeps Enter for itself.
 */

import { forwardRef, useRef, useState, type KeyboardEvent, type PointerEvent } from "react";
import { useI18n } from "../i18n";
import { IconMic, IconSend, IconStop } from "./icons";
import type { SpeechRecognitionState } from "./speech";

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
  const pressRef = useRef<{ at: number; started: boolean } | null>(null);

  const send = () => {
    const value = text.trim();
    if (!value || running) return;
    onSend(value);
    setText("");
  };

  const onKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    if (event.key !== "Enter" || event.shiftKey || event.nativeEvent.isComposing) return;
    event.preventDefault();
    send();
  };

  const onPointerDown = (event: PointerEvent<HTMLButtonElement>) => {
    if (!speech.supported || event.button !== 0) return;
    if (speech.listening) {
      speech.stop();
      pressRef.current = { at: Date.now(), started: false };
      return;
    }
    speech.start();
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

  return (
    <div className="copilot-composer">
      <textarea
        ref={ref}
        data-copilot-input
        className="copilot-input"
        rows={2}
        value={text}
        placeholder={t("copilot.input.placeholder")}
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
              else speech.start();
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
  );
});
