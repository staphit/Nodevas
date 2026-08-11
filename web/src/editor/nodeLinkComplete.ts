/**
 * `/` completion for a node's named links [B-04].
 *
 * The node's own links (edited under 標籤 in the metadata form) are its
 * shorthand vocabulary: typing `/角色設定` and accepting the completion writes
 * the full `[[project/node|label]]` into the document. The links themselves
 * live in `graph.yaml`; this only inserts text.
 */

import {
  autocompletion,
  type Completion,
  type CompletionContext,
  type CompletionResult,
} from "@codemirror/autocomplete";
import type { Extension } from "@codemirror/state";
import type { NodeLinkRef } from "../types";
import { formatNodeLink } from "../domain/graph/nodeLink";

/** Reads the links of the node being edited at completion time. */
export type NodeLinkSource = () => {
  links: NodeLinkRef[];
  currentProject: string;
};

export function nodeLinkCompletionSource(source: NodeLinkSource) {
  return (context: CompletionContext): CompletionResult | null => {
    // Only after a `/` that starts a word: a path like `src/index.ts` and a
    // date like `2026/08` must not turn into a link menu.
    const match = context.matchBefore(/(^|\s)\/[^\s/\\]*/);
    if (!match) return null;
    const slash = match.text.indexOf("/");
    const from = match.from + slash;
    const typed = match.text.slice(slash + 1);
    if (!context.explicit && typed.length === 0 && match.text.trim() !== "/") {
      return null;
    }
    const { links, currentProject } = source();
    if (links.length === 0) return null;
    const options: Completion[] = links.map((link) => ({
      label: `/${link.label}`,
      detail:
        link.project && link.project !== currentProject
          ? `${link.project} / ${link.node}`
          : link.node,
      type: "keyword",
      apply: formatNodeLink({
        project: link.project ?? currentProject,
        nodeId: link.node,
        label: link.label,
        currentProject,
      }),
    }));
    return { from, options, filter: true, validFor: /^\/[^\s/\\]*$/ };
  };
}

export function nodeLinkCompletion(source: NodeLinkSource): Extension {
  return autocompletion({
    override: [nodeLinkCompletionSource(source)],
    activateOnTyping: true,
    icons: false,
  });
}
