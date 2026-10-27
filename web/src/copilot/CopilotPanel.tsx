/**
 * The copilot side sheet: conversation picker, mode, spoken replies, the
 * transcript and the composer. Full width under 640px.
 */

import { forwardRef, useEffect, useId, useImperativeHandle, useRef, type KeyboardEvent } from "react";
import type { CopilotMode } from "../api";
import { confirmAction } from "../components/ConfirmDialog";
import { formatLocalizedDateTime, useI18n } from "../i18n";
import { IconClose, IconPlus, IconTrash } from "../icons";
import { Composer } from "./Composer";
import { useCopilot } from "./copilotStore";
import { IconSpeaker, IconSpeakerOff } from "./icons";
import { modelLabel } from "./modelLabel";
import {
  availableModes,
  changeMode,
  cycleMode,
  decide,
  deleteConversation,
  newConversation,
  openConversation,
  sendTurn,
  stopTurn,
} from "./session";
import type { SpeechRecognitionState } from "./speech";
import { Transcript } from "./Transcript";

/** Stored timestamps may be Unix seconds or milliseconds. */
function toDate(value: number): Date {
  return new Date(value < 1e12 ? value * 1000 : value);
}

export const CopilotPanel = forwardRef<
  HTMLTextAreaElement,
  { speech: SpeechRecognitionState; ttsSupported: boolean; onClose: () => void }
>(function CopilotPanel({ speech, ttsSupported, onClose }, forwardedRef) {
  const { t } = useI18n();
  const inputRef = useRef<HTMLTextAreaElement>(null);
  useImperativeHandle(forwardedRef, () => inputRef.current as HTMLTextAreaElement, []);
  // Opening the panel puts the cursor in the composer.
  useEffect(() => {
    inputRef.current?.focus();
  }, []);
  const headingId = useId();
  const conversations = useCopilot((s) => s.conversations);
  const activeId = useCopilot((s) => s.activeId);
  const mode = useCopilot((s) => s.mode);
  const status = useCopilot((s) => s.status);
  const speakReplies = useCopilot((s) => s.speakReplies);
  const setSpeakReplies = useCopilot((s) => s.setSpeakReplies);
  const items = useCopilot((s) => s.items);
  const pending = useCopilot((s) => s.pending);
  const running = useCopilot((s) => s.running);
  const notice = useCopilot((s) => s.notice);
  const setNotice = useCopilot((s) => s.setNotice);
  // Recomputed each render (it follows `status`); the current mode stays
  // listed even when the server no longer offers it, so the select is honest.
  const offered = availableModes();
  const modes = offered.includes(mode) ? offered : [...offered, mode];

  const onKeyDown = (event: KeyboardEvent<HTMLElement>) => {
    if (event.key === "Escape" && !event.defaultPrevented) {
      event.preventDefault();
      onClose();
      return;
    }
    // Shift+Tab cycles the mode, as in Claude Code — from the composer or the
    // panel itself, so Shift+Tab still moves focus back among the controls.
    if (event.key === "Tab" && event.shiftKey && !event.ctrlKey && !event.altKey && !event.metaKey) {
      const target = event.target as HTMLElement;
      if (target === event.currentTarget || target.matches("textarea[data-copilot-input]")) {
        event.preventDefault();
        cycleMode();
      }
    }
  };

  const removeActive = async () => {
    const conversation = conversations.find((item) => item.id === activeId);
    if (!conversation) return;
    const confirmed = await confirmAction({
      title: t("copilot.conversation.deleteTitle"),
      description: t("copilot.conversation.deleteBody", {
        title: conversation.title || t("copilot.conversation.untitled"),
      }),
      confirmLabel: t("copilot.conversation.delete"),
      tone: "danger",
    });
    if (confirmed) await deleteConversation(conversation.id);
  };

  const speechError = speech.errorKey ? t(speech.errorKey) : null;
  const label = modelLabel(status, t);

  return (
    <aside
      id="copilot-panel"
      className="copilot-panel"
      role="dialog"
      aria-modal="false"
      aria-labelledby={headingId}
      tabIndex={-1}
      onKeyDown={onKeyDown}
    >
      <header className="copilot-header">
        <div className="copilot-header-row">
          <h2 id={headingId} className="copilot-heading">
            {t("copilot.title")}
          </h2>
          {label && (
            <span className="copilot-model" title={label}>
              {label}
            </span>
          )}
          <button
            type="button"
            className="copilot-icon-btn"
            aria-pressed={speakReplies}
            disabled={!ttsSupported}
            onClick={() => setSpeakReplies(!speakReplies)}
            aria-label={t("copilot.speakReplies")}
            title={ttsSupported ? t("copilot.speakReplies") : t("copilot.speakUnsupported")}
          >
            {speakReplies ? <IconSpeaker size={16} /> : <IconSpeakerOff size={16} />}
          </button>
          <button
            type="button"
            className="copilot-icon-btn"
            onClick={onClose}
            aria-label={t("copilot.close")}
            title={`${t("copilot.close")} (Esc)`}
          >
            <IconClose size={16} />
          </button>
        </div>
        <div className="copilot-header-row">
          <select
            className="copilot-select copilot-conversations"
            aria-label={t("copilot.conversation.label")}
            value={activeId ?? ""}
            disabled={running}
            onChange={(event) => {
              const id = event.target.value;
              if (id) void openConversation(id);
              else newConversation();
            }}
          >
            <option value="">{t("copilot.conversation.new")}</option>
            {conversations.map((conversation) => (
              <option key={conversation.id} value={conversation.id}>
                {conversation.title || t("copilot.conversation.untitled")} ·{" "}
                {formatLocalizedDateTime(toDate(conversation.updatedAt || conversation.createdAt))}
              </option>
            ))}
          </select>
          <button
            type="button"
            className="copilot-icon-btn"
            onClick={newConversation}
            disabled={running || !activeId}
            aria-label={t("copilot.conversation.new")}
            title={t("copilot.conversation.new")}
          >
            <IconPlus size={16} />
          </button>
          <button
            type="button"
            className="copilot-icon-btn"
            onClick={() => void removeActive()}
            disabled={running || !activeId}
            aria-label={t("copilot.conversation.delete")}
            title={t("copilot.conversation.delete")}
          >
            <IconTrash size={16} />
          </button>
        </div>
        <div className="copilot-header-row">
          <label className="copilot-mode">
            <span>{t("copilot.mode.label")}</span>
            <select
              className="copilot-select"
              value={mode}
              title={t("copilot.mode.cycleHint")}
              onChange={(event) => void changeMode(event.target.value as CopilotMode)}
            >
              {modes.map((option) => (
                <option key={option} value={option}>
                  {t(`copilot.mode.${option}`)}
                </option>
              ))}
            </select>
          </label>
          <span className={`copilot-mode-hint copilot-mode-${mode}`}>
            {t(`copilot.mode.${mode}.hint`)}
          </span>
        </div>
      </header>
      {(notice || speechError) && (
        <div className="copilot-banner" role="status">
          <span>{notice ?? speechError}</span>
          {notice && (
            <button
              type="button"
              className="copilot-icon-btn"
              onClick={() => setNotice(null)}
              aria-label={t("copilot.dismiss")}
            >
              <IconClose size={13} />
            </button>
          )}
        </div>
      )}
      <Transcript
        items={items}
        pending={pending}
        interim={speech.interim}
        listening={speech.listening}
        onDecide={(requestId, decision) => void decide(requestId, decision)}
      />
      <Composer
        ref={inputRef}
        running={running}
        speech={speech}
        onSend={(text) => void sendTurn(text, "text")}
        onStop={() => void stopTurn()}
      />
    </aside>
  );
});
