// Agent chat with nobody in it: what it is, in one sentence, and how to get
// an agent here. On this computer's own server that is one command to copy
// (nodevas setup-agent wires every agent installed here), or the MCP server
// line by hand; on a team server, agents come through a runner on each
// person's machine. The docs link is the full guide.

import { useEffect, useRef, useState } from "react";

import { useTeamServer } from "../components/SignIn";
import { useI18n } from "../i18n";
import { openSettings } from "../settings/openSettings";
import { useApp } from "../store";

export const MCP_DOCS_URL = "https://github.com/staphit/Nodevas/blob/master/docs/mcp.md";

/** Where `nodevas` talks to when no --server is given. */
export const DEFAULT_SERVER = "http://127.0.0.1:5666";

/** A shell word: as it is when it is plain, double-quoted otherwise. */
export function shellWord(value: string): string {
  if (/^[\w./:@-]+$/.test(value)) return value;
  return `"${value.replace(/(["\\$`])/g, "\\$1")}"`;
}

/** The one command that connects this computer's agents to the project. */
export function setupCommand(project: string, origin: string): string {
  const parts = ["nodevas", "setup-agent"];
  if (origin && origin !== DEFAULT_SERVER) parts.push("--server", shellWord(origin));
  if (project) parts.push("--project", shellWord(project));
  return parts.join(" ");
}

/** The MCP server line, for an agent configured by hand. */
export function mcpCommand(project: string, origin: string, agent = "claude"): string {
  const parts = ["nodevas", "mcp", "--server", shellWord(origin || DEFAULT_SERVER)];
  if (project) parts.push("--project", shellWord(project));
  parts.push("--actor", `mcp:${agent}`);
  return parts.join(" ");
}

const COPIED_MS = 2000;

function Snippet({ text, onCopied }: { text: string; onCopied: () => void }) {
  const { t } = useI18n();
  const [copied, setCopied] = useState(false);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
  useEffect(() => () => {
    if (timer.current) clearTimeout(timer.current);
  }, []);
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(text);
    } catch {
      return; // no clipboard here: the text stays selectable
    }
    setCopied(true);
    onCopied();
    if (timer.current) clearTimeout(timer.current);
    timer.current = setTimeout(() => setCopied(false), COPIED_MS);
  };
  return (
    <div className="agent-chat-snippet">
      <pre>
        <code>{text}</code>
      </pre>
      <button type="button" className="agent-chat-copy" onClick={() => void copy()}>
        {copied ? t("agents.empty.copied") : t("agents.empty.copy")}
      </button>
    </div>
  );
}

export function AgentChatEmpty() {
  const { t } = useI18n();
  const team = useTeamServer();
  const project = useApp((s) => s.activeProject);
  const origin = typeof location === "undefined" ? DEFAULT_SERVER : location.origin;
  const [said, setSaid] = useState("");
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
  useEffect(() => () => {
    if (timer.current) clearTimeout(timer.current);
  }, []);
  const announce = () => {
    setSaid(t("agents.empty.copied"));
    if (timer.current) clearTimeout(timer.current);
    timer.current = setTimeout(() => setSaid(""), COPIED_MS);
  };

  return (
    <div className="agent-chat-empty">
      <p>{t("agents.empty.what")}</p>
      {team ? (
        <>
          <p>{t("agents.empty.remote")}</p>
          <button type="button" className="ap-btn" onClick={() => openSettings("runners", { add: true })}>
            {t("agents.empty.addRunner")}
          </button>
        </>
      ) : (
        <>
          <p>{t("agents.empty.setup")}</p>
          <Snippet text={setupCommand(project, origin)} onCopied={announce} />
          <details className="agent-chat-manual">
            <summary>{t("agents.empty.manual")}</summary>
            <Snippet text={mcpCommand(project, origin)} onCopied={announce} />
          </details>
        </>
      )}
      <a className="agent-chat-docs" href={MCP_DOCS_URL} target="_blank" rel="noopener noreferrer">
        {t("agents.empty.docs")}
      </a>
      <span className="visually-hidden" aria-live="polite">
        {said}
      </span>
    </div>
  );
}
