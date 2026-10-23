/**
 * A tool waiting for the person's say-so. Inline in the transcript, answered
 * with a button, with 1/2/3 while the card has focus, or by voice (handled by
 * the panel's speech handler through `parseVoiceDecision`).
 */

import { useEffect, useId, useRef, type KeyboardEvent } from "react";
import type { CopilotDecision } from "../api";
import { useI18n } from "../i18n";
import type { PermissionRequest } from "./copilotStore";
import { keyToDecision } from "./decisions";

export function PermissionCard({
  request,
  autoFocus,
  onDecide,
}: {
  request: PermissionRequest;
  autoFocus?: boolean;
  onDecide: (decision: CopilotDecision) => void;
}) {
  const { t } = useI18n();
  const titleId = useId();
  const cardRef = useRef<HTMLDivElement>(null);

  // Focus the card, not a button: Enter must not approve by accident.
  useEffect(() => {
    if (!autoFocus) return;
    const active = document.activeElement as HTMLElement | null;
    // Leave someone mid-sentence in the composer alone.
    if (active?.matches("textarea[data-copilot-input]") && (active as HTMLTextAreaElement).value) {
      return;
    }
    cardRef.current?.focus();
  }, [autoFocus]);

  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.ctrlKey || event.metaKey || event.altKey || request.submitting) return;
    const decision = keyToDecision(event.key);
    if (!decision) return;
    event.preventDefault();
    event.stopPropagation();
    onDecide(decision);
  };

  const busy = Boolean(request.submitting);

  return (
    <div
      ref={cardRef}
      className={`copilot-permission copilot-kind-border-${request.kind}`}
      role="group"
      aria-labelledby={titleId}
      tabIndex={-1}
      onKeyDown={onKeyDown}
      data-request-id={request.requestId}
    >
      <div className="copilot-permission-head">
        <span id={titleId} className="copilot-permission-title">
          {t("copilot.permission.title", { tool: request.title || request.name })}
        </span>
        <span className={`copilot-badge copilot-kind-${request.kind}`}>
          {t(`copilot.kind.${request.kind}`)}
        </span>
      </div>
      {request.summary && <p className="copilot-permission-summary">{request.summary}</p>}
      {request.input && (
        <details className="copilot-tool-detail">
          <summary>{t("copilot.tool.input")}</summary>
          <pre>{request.input}</pre>
        </details>
      )}
      <div className="copilot-permission-actions">
        <button type="button" className="primary" disabled={busy} onClick={() => onDecide("allow")}>
          {t("copilot.permission.allow")} <kbd>1</kbd>
        </button>
        <button type="button" disabled={busy} onClick={() => onDecide("allow_session")}>
          {t("copilot.permission.allowSession")} <kbd>2</kbd>
        </button>
        <button type="button" className="danger" disabled={busy} onClick={() => onDecide("deny")}>
          {t("copilot.permission.deny")} <kbd>3</kbd>
        </button>
      </div>
      <p className="copilot-permission-hint">{t("copilot.permission.voiceHint")}</p>
    </div>
  );
}
