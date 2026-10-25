/**
 * The conversation log: user bubbles, assistant markdown, tool cards and
 * notices, then any permission cards and the live interim transcript.
 */

import { useEffect, useLayoutEffect, useMemo, useRef, type MouseEvent } from "react";
import { useI18n } from "../i18n";
import { renderMarkdown } from "../markdown";
import { reportError, useApp } from "../store";
import type { PermissionRequest, TranscriptItem } from "./copilotStore";
import { PermissionCard } from "./PermissionCard";

type ToolItem = Extract<TranscriptItem, { kind: "tool" }>;

function AssistantBubble({ text, streaming }: { text: string; streaming?: boolean }) {
  const html = useMemo(() => renderMarkdown(text, { remoteMedia: false }), [text]);
  return (
    <div className={`copilot-bubble copilot-bubble-assistant${streaming ? " is-streaming" : ""}`}>
      <div
        className="copilot-markdown"
        aria-busy={streaming || undefined}
        // renderMarkdown sanitizes with DOMPurify.
        dangerouslySetInnerHTML={{ __html: html }}
      />
      {streaming && <span className="copilot-caret" aria-hidden />}
    </div>
  );
}

export function ToolCard({ item }: { item: ToolItem }) {
  const { t } = useI18n();
  const kind = item.client ? "ui" : item.toolKind;
  return (
    <div className={`copilot-tool is-${item.status}`}>
      <div className="copilot-tool-head">
        <span className="copilot-tool-title">{item.title || item.name}</span>
        {kind && (
          <span className={`copilot-badge copilot-kind-${kind}`}>{t(`copilot.kind.${kind}`)}</span>
        )}
        <span className={`copilot-badge copilot-status-${item.status}`}>
          {t(`copilot.tool.status.${item.status}`)}
        </span>
      </div>
      {item.title && item.title !== item.name && (
        <code className="copilot-tool-name">{item.name}</code>
      )}
      {item.input && (
        <details className="copilot-tool-detail">
          <summary>{t("copilot.tool.input")}</summary>
          <pre>{item.input}</pre>
        </details>
      )}
      {item.output && (
        <details className="copilot-tool-detail" open={item.status === "error" || undefined}>
          <summary>{t("copilot.tool.output")}</summary>
          <pre>{item.output}</pre>
        </details>
      )}
    </div>
  );
}

function Item({ item }: { item: TranscriptItem }) {
  const { t } = useI18n();
  switch (item.kind) {
    case "user":
      return (
        <div className="copilot-bubble copilot-bubble-user">
          {item.source === "voice" && (
            <span className="copilot-voice-mark" role="img" aria-label={t("copilot.spoken")}>
              🎤
            </span>
          )}
          <span className="copilot-user-text">{item.text}</span>
        </div>
      );
    case "assistant":
      return <AssistantBubble text={item.text} streaming={item.streaming} />;
    case "tool":
      return <ToolCard item={item} />;
    case "notice":
      return (
        <div
          className={`copilot-notice${item.tone === "error" ? " is-error" : ""}`}
          role={item.tone === "error" ? "alert" : undefined}
        >
          {item.text}
        </div>
      );
  }
}

/** Distance from the bottom, in px, still counted as "at the bottom". */
const STICK_THRESHOLD = 48;

export function Transcript({
  items,
  pending,
  interim,
  listening,
  onDecide,
}: {
  items: TranscriptItem[];
  pending: PermissionRequest[];
  interim: string;
  listening: boolean;
  onDecide: (requestId: string, decision: "allow" | "allow_session" | "deny") => void;
}) {
  const { t } = useI18n();
  const scrollRef = useRef<HTMLDivElement>(null);
  const stickRef = useRef(true);

  const onScroll = () => {
    const element = scrollRef.current;
    if (!element) return;
    stickRef.current =
      element.scrollHeight - element.scrollTop - element.clientHeight < STICK_THRESHOLD;
  };

  // Follow new output unless the reader scrolled up to look at something.
  useLayoutEffect(() => {
    const element = scrollRef.current;
    if (element && stickRef.current) element.scrollTop = element.scrollHeight;
  }, [items, pending, interim]);

  // A new permission request is something to answer now: bring it into view.
  const firstPending = pending[0]?.requestId;
  useEffect(() => {
    if (!firstPending) return;
    stickRef.current = true;
    const element = scrollRef.current;
    if (element) element.scrollTop = element.scrollHeight;
  }, [firstPending]);

  // `[[node]]` links in assistant replies open the node, like in a document.
  const onClick = (event: MouseEvent<HTMLDivElement>) => {
    const anchor = (event.target as HTMLElement).closest<HTMLAnchorElement>("a[data-node-link]");
    if (!anchor) return;
    event.preventDefault();
    void useApp
      .getState()
      .openNodeLink({
        project: anchor.dataset.nodeLinkProject ?? "",
        nodeId: anchor.dataset.nodeLinkNode ?? "",
      })
      .catch(reportError);
  };

  const empty = items.length === 0 && pending.length === 0 && !interim;

  return (
    <div
      ref={scrollRef}
      className="copilot-transcript"
      role="log"
      aria-live="polite"
      aria-relevant="additions text"
      aria-label={t("copilot.transcript")}
      onScroll={onScroll}
      onClick={onClick}
    >
      {empty && <p className="copilot-empty">{t("copilot.empty")}</p>}
      {items.map((item) => (
        <Item key={item.id} item={item} />
      ))}
      {pending.map((request, index) => (
        <PermissionCard
          key={request.requestId}
          request={request}
          autoFocus={index === 0}
          onDecide={(decision) => onDecide(request.requestId, decision)}
        />
      ))}
      {listening && (
        <div className="copilot-bubble copilot-bubble-user is-interim" aria-live="off">
          <span className="copilot-voice-mark" aria-hidden>
            🎤
          </span>
          <span className="copilot-user-text">{interim || t("copilot.listening")}</span>
        </div>
      )}
    </div>
  );
}
