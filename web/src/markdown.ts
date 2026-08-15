/**
 * Markdown → safe HTML.
 *
 * One renderer for every read-only view (document preview, history snapshots),
 * so what a snapshot looks like is exactly what the document will look like
 * after restoring it.
 */

import DOMPurify from "dompurify";
import { marked, type TokenizerAndRendererExtension } from "marked";
import { splitLinkTarget } from "./domain/graph/nodeLink";

function escapeAttribute(value: string): string {
  return value
    .replaceAll("&", "&amp;")
    .replaceAll('"', "&quot;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;");
}

/**
 * `[[project/node-id|label]]` becomes an anchor the app handles itself. It is
 * not a URL: the href stays empty so nothing navigates away, and the target
 * travels in data attributes for the click handler.
 */
const nodeLinkExtension: TokenizerAndRendererExtension = {
  name: "nodeLink",
  level: "inline",
  start(source: string) {
    return source.indexOf("[[");
  },
  tokenizer(source: string) {
    const match = /^\[\[([^\]|\n]+?)(?:\|([^\]\n]*))?\]\]/.exec(source);
    if (!match) return undefined;
    const { project, nodeId } = splitLinkTarget(match[1]);
    if (!nodeId) return undefined;
    return {
      type: "nodeLink",
      raw: match[0],
      project,
      nodeId,
      label: (match[2] ?? "").trim() || nodeId,
    };
  },
  renderer(token) {
    const link = token as unknown as {
      project: string;
      nodeId: string;
      label: string;
    };
    const target = link.project ? `${link.project}/${link.nodeId}` : link.nodeId;
    return (
      `<a class="node-link" href="#" data-node-link="${escapeAttribute(target)}"` +
      ` data-node-link-project="${escapeAttribute(link.project)}"` +
      ` data-node-link-node="${escapeAttribute(link.nodeId)}">` +
      `${escapeAttribute(link.label)}</a>`
    );
  },
};

marked.use({ extensions: [nodeLinkExtension] });

/** Shared HTML cleaning: also used to preview an HTML subpage. */
export function sanitizeHTML(html: string): string {
  const sanitized = DOMPurify.sanitize(html, {
    USE_PROFILES: { html: true },
    ADD_ATTR: ["data-node-link", "data-node-link-project", "data-node-link-node"],
    FORBID_TAGS: ["style", "iframe", "object", "embed", "form", "input", "button", "textarea", "select"],
    FORBID_ATTR: ["srcdoc"],
  });
  const document = new DOMParser().parseFromString(sanitized, "text/html");
  for (const element of document.body.querySelectorAll<HTMLElement>("[style]")) {
    const style = element.getAttribute("style")?.trim() ?? "";
    if (element.tagName !== "SPAN" || !/^color:\s*#[0-9a-f]{6}\s*;?$/i.test(style)) {
      element.removeAttribute("style");
    }
  }
  for (const anchor of document.body.querySelectorAll<HTMLAnchorElement>("a")) {
    anchor.rel = "noopener noreferrer";
  }
  return document.body.innerHTML;
}

export function renderMarkdown(source: string): string {
  return sanitizeHTML(marked.parse(source, { async: false }) as string);
}
