import { useMemo } from "react";

import type { PageFormat } from "../../api";
import type { NodeLinkHandler } from "../../editor/livePreview";
import { renderMarkdown, sanitizeHTML } from "../../markdown";
import { escapeHTMLText } from "./pageFormats";

/** The read-only render of the whole document, shown in preview mode. */
export function DocumentPreview({
  content,
  format,
  fontSize,
  onOpenNodeLink,
}: {
  content: string;
  format: PageFormat;
  fontSize: number;
  onOpenNodeLink: NodeLinkHandler;
}) {
  const previewHtml = useMemo(() => {
    // An HTML page is previewed as itself; plain text keeps its own line
    // breaks instead of being read as Markdown.
    if (format === "html") return sanitizeHTML(content);
    if (format === "txt") {
      return `<pre class="md-preview-plain">${escapeHTMLText(content)}</pre>`;
    }
    return renderMarkdown(content);
  }, [format, content]);

  return (
    <div
      className="md-preview"
      style={{ fontSize }}
      // Node links are internal navigation, not URLs: the anchor carries
      // its target in data attributes and the app opens it.
      onClick={(event) => {
        const anchor = (event.target as HTMLElement).closest<HTMLElement>(
          "a[data-node-link]",
        );
        if (!anchor) return;
        event.preventDefault();
        onOpenNodeLink({
          project: anchor.dataset.nodeLinkProject ?? "",
          nodeId: anchor.dataset.nodeLinkNode ?? "",
        });
      }}
      dangerouslySetInnerHTML={{ __html: previewHtml }}
    />
  );
}
