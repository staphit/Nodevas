import { useMemo, useRef } from "react";
import { markdown, markdownLanguage } from "@codemirror/lang-markdown";
import { html as htmlLanguage } from "@codemirror/lang-html";
import { HighlightStyle, syntaxHighlighting } from "@codemirror/language";
import { tags } from "@lezer/highlight";
import { EditorView } from "@codemirror/view";
import type { Extension } from "@codemirror/state";

import type { PageFormat } from "../../api";
import { livePreview, type NodeLinkHandler } from "../../editor/livePreview";
import { nodeLinkCompletion } from "../../editor/nodeLinkComplete";
import { remoteCarets } from "../../editor/remoteCarets";
import type { NodeLinkRef } from "../../types";

/**
 * How the markdown is shown while editing [B-04]:
 * - `live`: rendered as you type; the caret's line shows its raw markers
 * - `source`: plain markdown, nothing hidden
 * - `preview`: read-only render of the whole document
 */
export type EditorMode = "live" | "source" | "preview";

// Editorial rendering inside the editor: headings get real sizes.
const mdHighlight = HighlightStyle.define([
  { tag: tags.heading1, fontSize: "1.55em", fontWeight: "700", textDecoration: "none" },
  { tag: tags.heading2, fontSize: "1.32em", fontWeight: "700", textDecoration: "none" },
  { tag: tags.heading3, fontSize: "1.15em", fontWeight: "600", textDecoration: "none" },
  { tag: tags.strong, fontWeight: "700" },
  { tag: tags.emphasis, fontStyle: "italic" },
  { tag: tags.monospace, fontFamily: "var(--mono)" },
  { tag: tags.quote, color: "var(--text-2)", fontStyle: "italic" },
  { tag: tags.processingInstruction, color: "var(--text-2)", opacity: "0.6" },
  { tag: tags.link, color: "var(--st-progress-fg)" },
]);

/** The CodeMirror extensions for one document, chosen by its file format. */
export function useEditorExtensions({
  format,
  editorMode,
  links,
  currentProject,
  onOpenNodeLink,
}: {
  format: PageFormat;
  editorMode: EditorMode;
  links: NodeLinkRef[];
  currentProject: string;
  onOpenNodeLink: NodeLinkHandler;
}): Extension[] {
  // Read through refs: the completion source is created once with the
  // extensions, but must see the node's current links every time it runs.
  const nodeLinks = useRef<NodeLinkRef[]>([]);
  nodeLinks.current = links;
  const currentProjectRef = useRef("");
  currentProjectRef.current = currentProject;

  return useMemo(() => {
    // Other people's carets are drawn whatever the file is: a plain-text page
    // is as shared as a markdown one.
    const base = [EditorView.lineWrapping, remoteCarets()];
    if (format === "html") {
      return [htmlLanguage(), ...base];
    }
    if (format === "txt") {
      return base;
    }
    return [
      // GFM base: task lists, tables and strikethrough are part of what the
      // documents already use.
      markdown({ base: markdownLanguage }),
      syntaxHighlighting(mdHighlight),
      ...base,
      nodeLinkCompletion(() => ({
        links: nodeLinks.current,
        currentProject: currentProjectRef.current,
      })),
      ...(editorMode === "live"
        ? [
            livePreview({
              onOpenNodeLink,
            }),
          ]
        : []),
    ];
  }, [format, editorMode]);
}
