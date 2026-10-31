/**
 * A workspace commit's subject in the reader's language.
 *
 * The hub writes the subject from the paths a commit touched and the kind of
 * each change, always in English and never from content (store/history_git.go
 * commitMessage): "Auth API: edit page; Billing: create; graph: edit", at most
 * six groups then "and N more", cut with "…" past 200 characters. This reads
 * that grammar back and words it in the interface language. A part it does
 * not recognise — an agent's own message — is shown as written.
 */

import { translate } from "../i18n";
import type { Language } from "../preferences";

/** Each change kind the hub writes, and its message key. */
const KINDS: Record<string, string> = {
  create: "explorer.commit.kind.create",
  delete: "explorer.commit.kind.delete",
  edit: "explorer.commit.kind.edit",
  move: "explorer.commit.kind.move",
  restore: "explorer.commit.kind.restore",
  pages: "explorer.commit.kind.pages",
  "add page": "explorer.commit.kind.addPage",
  "edit page": "explorer.commit.kind.editPage",
  "delete page": "explorer.commit.kind.deletePage",
  "convert page": "explorer.commit.kind.convertPage",
  "outside edit": "explorer.commit.kind.outsideEdit",
};

const ELLIPSIS = "…";

/** One "label: kind, kind" group, or null when it is not one the hub wrote. */
function groupText(part: string, cut: boolean, language: Language): string | null {
  const more = /^and (\d+) more$/.exec(part);
  if (more) return translate("explorer.commit.more", language, { count: more[1] });
  const at = part.lastIndexOf(": ");
  if (at <= 0) return null;
  const label = part.slice(0, at);
  const kinds = part.slice(at + 2).split(", ");
  const words: string[] = [];
  for (const [index, kind] of kinds.entries()) {
    const key = KINDS[kind];
    if (key) {
      words.push(translate(key, language));
      continue;
    }
    // Only the very end of a subject cut at its limit may be part of a word.
    if (!(cut && index === kinds.length - 1)) return null;
    words.push(kind + ELLIPSIS);
  }
  return translate("explorer.commit.group", language, {
    label: label === "graph" ? translate("explorer.commit.graph", language) : label,
    kinds: words.join(translate("explorer.commit.kindSeparator", language)),
  });
}

export function commitSubjectText(subject: string, language: Language): string {
  if (subject === "update") return translate("explorer.commit.update", language);
  const cut = subject.endsWith(ELLIPSIS);
  const parts = (cut ? subject.slice(0, -ELLIPSIS.length) : subject).split("; ");
  const out: string[] = [];
  for (const [index, part] of parts.entries()) {
    const last = index === parts.length - 1;
    const text = groupText(part, cut && last, language);
    if (text !== null) out.push(text);
    // The cut may also fall inside the last group's label.
    else if (cut && last && index > 0) out.push(part + ELLIPSIS);
    else return subject;
  }
  const joined = out.join(translate("explorer.commit.separator", language));
  // A cut that fell between words leaves the last kind whole: say it was cut.
  return cut && !joined.endsWith(ELLIPSIS) ? joined + ELLIPSIS : joined;
}
