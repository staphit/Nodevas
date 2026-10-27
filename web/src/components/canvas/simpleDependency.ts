/**
 * The simple dependency, as the editing UI presents it.
 *
 * Drawing A → B makes B depend on A: the edge is a required relation and A is
 * appended to B's `requires`. Several prerequisites are joined with `and`, so
 * "B depends on A and C" means B waits until all of them are done. That is the
 * default, and the UI shows it as plain "Depends on / Blocks" wording.
 *
 * Anything else — OR/XOR/NAND/NOR, `not`, flags, nested groups, optional or
 * deprecated relations, standalone logic gates — is an advanced condition. The
 * UI keeps those behind a disclosure, but opens it (or shows a badge) whenever
 * the data already holds one, so nothing stored is ever hidden.
 *
 * Presentation only: nothing here changes what is written or how it is read.
 */

import { topLevelOp } from "../../domain/graph/requires";
import { edgeRelation } from "../../domain/graph/edgeStyle";
import type { GraphEdge } from "../../types";

const PLAIN_ALL_OF =
  /^[A-Za-z_][A-Za-z0-9_.-]*(?:\s+and\s+[A-Za-z_][A-Za-z0-9_.-]*)*$/i;

const KEYWORDS = new Set([
  "and",
  "or",
  "xor",
  "nand",
  "nor",
  "not",
  "all",
  "any",
  "flag",
  "true",
  "false",
]);

/**
 * Whether a `requires` expression is the default "all of these" condition: an
 * empty expression, one node reference, or references joined only by `and`.
 */
export function isPlainAllOfRequirement(requires: string | undefined): boolean {
  const expression = requires?.trim() ?? "";
  if (!expression) return true;
  if (!PLAIN_ALL_OF.test(expression)) return false;
  return expression
    .split(/\s+and\s+/i)
    .every((reference) => !KEYWORDS.has(reference.toLowerCase()));
}

/**
 * Whether a node's incoming dependencies use anything beyond the default:
 * a non-AND condition, or a prerequisite that is optional or deprecated.
 */
export function hasAdvancedCondition(
  requires: string | undefined,
  incoming: readonly GraphEdge[],
): boolean {
  return (
    !isPlainAllOfRequirement(requires) ||
    incoming.some((edge) => edgeRelation(edge) !== "")
  );
}

/**
 * The badge to show for a non-default condition: the top-level operator when
 * it has a label of its own, otherwise "custom" (flags, nested groups, …).
 * `null` for the default condition, which needs no badge.
 */
export function advancedConditionKind(
  requires: string | undefined,
): "or" | "xor" | "nand" | "nor" | "not" | "custom" | null {
  if (isPlainAllOfRequirement(requires)) return null;
  const operator = topLevelOp(requires);
  if (
    operator === "or" ||
    operator === "xor" ||
    operator === "nand" ||
    operator === "nor" ||
    operator === "not"
  ) {
    return operator;
  }
  return "custom";
}
