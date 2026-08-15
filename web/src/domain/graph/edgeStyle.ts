/**
 * Edge relation and line [B-06].
 *
 * What an edge *means* and how it is *drawn* are two independent fields:
 * a required dependency may be dashed for emphasis, and a deprecated one keeps
 * its line while dropping out of every dependency computation. `line: ""`
 * means "whatever the relation implies", so an edge only stores a line once
 * the user overrides that default.
 */

import type { EdgeLine, EdgeRelation, GraphEdge } from "../../types";

export const RELATION_LABELS: Record<EdgeRelation, string> = {
  "": "必要",
  optional: "選用",
  deprecated: "棄用",
};

export const LINE_LABELS: Record<Exclude<EdgeLine, "">, string> = {
  solid: "實線",
  dashed: "虛線",
  dotted: "點線",
};

/** The line a relation is drawn with when the edge does not override it. */
export const DEFAULT_LINE: Record<EdgeRelation, Exclude<EdgeLine, "">> = {
  "": "solid",
  optional: "dashed",
  deprecated: "dashed",
};

export function edgeRelation(edge: GraphEdge | undefined | null): EdgeRelation {
  if (!edge) return "";
  return edge.relation ?? "";
}

/** The line actually drawn: the stored override, else the relation's default. */
export function edgeLine(
  edge: GraphEdge | undefined | null,
): Exclude<EdgeLine, ""> {
  if (edge?.line) return edge.line;
  return DEFAULT_LINE[edgeRelation(edge)];
}

/**
 * Whether the edge can hold its target back. Only required edges do; optional
 * and deprecated ones are relations without power over the lifecycle.
 */
export function edgeBlocks(edge: GraphEdge | undefined | null): boolean {
  return edgeRelation(edge) === "";
}

/**
 * Whether the edge counts as a dependency at all. A deprecated edge is kept
 * for the record: it is drawn, but no analysis reads it.
 */
export function edgeIsPrerequisite(edge: GraphEdge | undefined | null): boolean {
  return edgeRelation(edge) !== "deprecated";
}

/** The fields to write for a relation/line pair, with defaults left unset. */
export function edgeStylePatch(
  relation: EdgeRelation,
  line: EdgeLine,
): Pick<GraphEdge, "relation" | "line"> {
	return {
    ...(relation ? { relation } : { relation: undefined }),
    ...(line && line !== DEFAULT_LINE[relation]
      ? { line }
      : { line: undefined }),
	};
}
