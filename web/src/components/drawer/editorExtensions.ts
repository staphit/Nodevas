import { useMemo, useRef } from "react";
import { markdown, markdownLanguage } from "@codemirror/lang-markdown";
import { html as htmlLanguage } from "@codemirror/lang-html";
import { HighlightStyle, syntaxHighlighting } from "@codemirror/language";
import { tags } from "@lezer/highlight";
import { EditorView } from "@codemirror/view";
import { EditorState, type Extension } from "@codemirror/state";

import type { PageFormat } from "../../api";
import { translate, useI18n } from "../../i18n";
import type { Language } from "../../preferences";
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

/**
 * The words CodeMirror draws itself (the Ctrl+F panel, the fold gutter,
 * go-to-line, completion, screen-reader announcements), keyed by its own
 * English phrase. `$` is CodeMirror's placeholder and stays as it is.
 */
const CODEMIRROR_PHRASE_KEYS: Readonly<Record<string, string>> = {
  "Find": "editor.cm.find",
  "Replace": "editor.cm.replaceField",
  "next": "editor.cm.next",
  "previous": "editor.cm.previous",
  "all": "editor.cm.all",
  "match case": "editor.cm.matchCase",
  "regexp": "editor.cm.regexp",
  "by word": "editor.cm.byWord",
  "replace": "editor.cm.replace",
  "replace all": "editor.cm.replaceAll",
  "close": "editor.cm.close",
  "current match": "editor.cm.currentMatch",
  "on line": "editor.cm.onLine",
  "replaced match on line $": "editor.cm.replacedOnLine",
  "replaced $ matches": "editor.cm.replacedCount",
  "Go to line": "editor.cm.goToLine",
  "go": "editor.cm.go",
  "Fold line": "editor.cm.foldLine",
  "Unfold line": "editor.cm.unfoldLine",
  "Folded lines": "editor.cm.foldedLines",
  "Unfolded lines": "editor.cm.unfoldedLines",
  "folded code": "editor.cm.foldedCode",
  "unfold": "editor.cm.unfold",
  "to": "editor.cm.to",
  "Completions": "editor.cm.completions",
  "Control character": "editor.cm.controlCharacter",
  "Selection deleted": "editor.cm.selectionDeleted",
};

/** CodeMirror's phrases in the interface language. */
export function editorPhrases(language: Language): Extension {
  const phrases: Record<string, string> = {};
  for (const [phrase, key] of Object.entries(CODEMIRROR_PHRASE_KEYS)) {
    phrases[phrase] = translate(key, language);
  }
  return EditorState.phrases.of(phrases);
}

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
  const { language } = useI18n();

  return useMemo(() => {
    // Other people's carets are drawn whatever the file is: a plain-text page
    // is as shared as a markdown one.
    const base = [EditorView.lineWrapping, remoteCarets(), editorPhrases(language)];
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
  }, [format, editorMode, language]);
}
