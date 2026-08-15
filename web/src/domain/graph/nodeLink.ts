/**
 * Node links [A-04].
 *
 * A document can point at another node the way a wiki does:
 *
 *   [[node-0006]]                  same project
 *   [[Story/node-0012]]            another project in the same workspace
 *   [[Story/node-0012|主線大綱]]     with the text to show
 *
 * The target is `<project>/<node id>`, split at the *last* slash, because a
 * project name can be a nested path ("Game mechanic/systems"). An empty
 * project means "the project this document belongs to".
 *
 * Links never leave the workspace: there is no host, scheme or `..`, so a link
 * can only ever address something the person already has open.
 */

export interface NodeLink {
  /** Project path, or "" for the document's own project. */
  project: string;
  nodeId: string;
  /** Text to show; the node's title when the link does not carry one. */
  label: string;
  /** Source offsets of the whole `[[…]]`, for editors that decorate it. */
  start: number;
  end: number;
}

/** Matches one `[[target]]` or `[[target|label]]`. Global: clone before use. */
const LINK_PATTERN = /\[\[([^\]|\n]+?)(?:\|([^\]\n]*))?\]\]/g;

export function nodeLinkPattern(): RegExp {
  return new RegExp(LINK_PATTERN.source, "g");
}

/** Splits "Story/node-0012" into its project and node parts. */
export function splitLinkTarget(target: string): { project: string; nodeId: string } {
  const trimmed = target.trim().replace(/^\/+|\/+$/g, "");
  const slash = trimmed.lastIndexOf("/");
  if (slash < 0) return { project: "", nodeId: trimmed };
  return {
    project: trimmed.slice(0, slash).trim(),
    nodeId: trimmed.slice(slash + 1).trim(),
  };
}

export function parseNodeLinks(source: string): NodeLink[] {
  const links: NodeLink[] = [];
  const pattern = nodeLinkPattern();
  for (let match = pattern.exec(source); match; match = pattern.exec(source)) {
    const { project, nodeId } = splitLinkTarget(match[1]);
    if (!nodeId) continue;
    links.push({
      project,
      nodeId,
      label: (match[2] ?? "").trim() || nodeId,
      start: match.index,
      end: match.index + match[0].length,
    });
  }
  return links;
}

/**
 * Writes a link. The project is left out when the target is in the document's
 * own project, so moving a whole project does not break its internal links.
 */
export function formatNodeLink(options: {
  project: string;
  nodeId: string;
  label?: string;
  /** The project the link is written in. */
  currentProject?: string;
}): string {
  const target =
    options.project && options.project !== options.currentProject
      ? `${options.project}/${options.nodeId}`
      : options.nodeId;
  const label = options.label?.trim();
  return label && label !== options.nodeId ? `[[${target}|${label}]]` : `[[${target}]]`;
}
