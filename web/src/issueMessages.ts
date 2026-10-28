/**
 * The hub's validation findings, in the reader's language.
 *
 * The hub validates a graph in English only (internal/engine/validate.go) and
 * sends no code with a finding, so the messages it is known to write are
 * matched here and read from the catalog. A message this list does not know
 * stays exactly as the hub wrote it.
 */

import { translate } from "./i18n";
import type { Language } from "./preferences";
import type { Issue } from "./types";

/** A Go %q string: quoted, with backslash escapes. */
const Q = String.raw`"((?:[^"\\]|\\.)*)"`;

interface Rule {
  re: RegExp;
  key: string;
  /** The catalog value each capture group fills, in order. */
  names: string[];
}

const rule = (pattern: string, key: string, ...names: string[]): Rule => ({
  re: new RegExp(`^${pattern}$`),
  key: `issue.msg.${key}`,
  names,
});

const RULES: Rule[] = [
  rule("graph is null", "graphNull"),
  rule("version must be at least 1", "version"),
  rule(String.raw`node at index (\d+) is null`, "nodeNull", "index"),
  rule("node with empty id", "emptyId"),
  rule(`invalid node id ${Q}`, "invalidId", "id"),
  rule(`duplicate node id ${Q}`, "duplicateId", "id"),
  rule(`invalid priority ${Q}`, "invalidPriority", "value"),
  rule(`invalid write_access ${Q}`, "invalidWriteAccess", "value"),
  rule(
    "effects never run on a (.+) node: it has no status change of its own; move them to a node before it",
    "effectsNeverRun",
    "type",
  ),
  rule(
    String.raw`(\d+) node\(s\) use types this project does not define \((.*)\); they behave as Task\. Pick a type for them, or add a custom type with that name\.`,
    "unknownKinds",
    "count",
    "kinds",
  ),
  rule("syntax error: (.*)", "syntax", "detail"),
  rule(`unknown node ${Q}`, "unknownNode", "id"),
  rule("node depends on itself", "selfDependency"),
  rule(`flag ${Q} is not declared in graph flags`, "undeclaredFlag", "flag"),
  rule(String.raw`edge at index (\d+) is null`, "edgeNull", "index"),
  rule(`edge from unknown node ${Q}`, "edgeFromUnknown", "id"),
  rule(`edge to unknown node ${Q}`, "edgeToUnknown", "id"),
  rule(`position references unknown node ${Q}`, "positionUnknown", "id"),
  rule("position must contain finite coordinates", "positionFinite"),
  rule(`gate placement references unknown node ${Q}`, "gateUnknown", "id"),
  rule("gate placement must contain both x and y coordinates", "gateXY"),
  rule("gate placement must contain finite coordinates", "gateFinite"),
  rule("gate ratio must be a finite number between 0 and 1", "gateRatio"),
  rule(`label placement references unknown edge ${Q}`, "labelUnknown", "edge"),
  rule("label ratio must be a finite number between 0 and 1", "labelRatio"),
  rule(`wire vertices reference unknown edge or gate ${Q}`, "wireUnknown", "wire"),
  rule(`wire ${Q} has too many vertices`, "wireTooMany", "wire"),
  rule(`wire ${Q} vertices must contain finite coordinates`, "wireFinite", "wire"),
  rule(String.raw`too many custom plan statuses \(maximum (\d+)\)`, "planStatusTooMany", "max"),
  rule(`invalid custom plan status id ${Q}`, "planStatusInvalidId", "id"),
  rule(`custom plan status ${Q} has an empty or oversized label`, "planStatusLabel", "id"),
  rule(`duplicate plan status ${Q}`, "planStatusDuplicate", "id"),
  rule(`duplicate custom plan status label ${Q}`, "planStatusDuplicateLabel", "label"),
  rule(`plan references unknown node ${Q}`, "planUnknown", "id"),
  rule(`invalid plan date ${Q}; want YYYY-MM-DD`, "planDate", "date"),
  rule(`invalid plan status ${Q}`, "planStatus", "status"),
  rule(String.raw`duplicate (\S+) plan milestone`, "planDuplicateMilestone", "status"),
  rule("planned progress precedes planned start", "planProgressBeforeStart"),
  rule("planned completion precedes planned progress", "planDoneBeforeProgress"),
  rule("planned completion precedes planned start", "planDoneBeforeStart"),
  rule(`node style references unknown node ${Q}`, "styleUnknown", "id"),
  rule(`unknown shape ${Q}`, "styleShape", "shape"),
  rule("node style has invalid dimensions", "styleDimensions"),
  rule(`invalid canvas decoration id ${Q}`, "decorationInvalidId", "id"),
  rule(`duplicate canvas decoration id ${Q}`, "decorationDuplicateId", "id"),
  rule(`canvas decoration ${Q} has invalid geometry`, "decorationGeometry", "id"),
  rule(
    "dependency cycle through the visual wires: (.*); repair the projection to match requires",
    "wireCycle",
    "cycle",
  ),
  rule("dependency cycle: (.*)", "cycle", "cycle"),
  rule("node is not connected to anything", "disconnected"),
];

/** What a Go %q wrote, unquoted; the raw text when it is not valid JSON. */
function unquote(raw: string): string {
  try {
    return JSON.parse(`"${raw}"`) as string;
  } catch {
    return raw;
  }
}

/** `"epic" ×2, "spike" ×1` → 「epic」×2、「spike」×1 for zh-TW. */
function localizeKinds(raw: string, language: Language): string {
  if (language === "en") return raw;
  const parts = [...raw.matchAll(new RegExp(`${Q} ×(\\d+)|…`, "g"))].map((match) =>
    match[0] === "…" ? "…" : `「${unquote(match[1])}」×${match[2]}`,
  );
  return parts.length > 0 ? parts.join("、") : raw;
}

/** A cycle `a -> b -> a`, drawn with arrows. */
const localizeCycle = (raw: string) => raw.split(" -> ").join(" → ");

/** The hub's validation message in language, or msg itself when it is not one this list knows. */
export function localizeIssueMessage(msg: string, language: Language): string {
  if (language === "en") return msg;
  for (const { re, key, names } of RULES) {
    const match = re.exec(msg);
    if (!match) continue;
    const values: Record<string, string> = {};
    names.forEach((name, index) => {
      const raw = match[index + 1] ?? "";
      if (name === "kinds") values[name] = localizeKinds(raw, language);
      else if (name === "cycle") values[name] = localizeCycle(raw);
      else values[name] = unquote(raw);
    });
    return translate(key, language, values);
  }
  return msg;
}

/** issue with its message in language. */
export function localizeIssue(issue: Issue, language: Language): Issue {
  const msg = localizeIssueMessage(issue.msg, language);
  return msg === issue.msg ? issue : { ...issue, msg };
}
