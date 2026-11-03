/** One field of a file's front matter, its value flattened to text. */
export interface FrontField {
  key: string;
  value: string;
}

const FENCE = /^---[ \t]*\r?\n([\s\S]*?)\r?\n---[ \t]*(?:\r?\n|$)/;

/**
 * Splits YAML front matter off a Markdown file: the simple shapes a node
 * file uses (`key: value`, and `key:` followed by `- item` lines, shown
 * joined). A file without a leading fence is returned whole.
 */
export function splitFrontMatter(text: string): { fields: FrontField[] | null; body: string } {
  const match = FENCE.exec(text);
  if (!match) return { fields: null, body: text };
  const fields: FrontField[] = [];
  for (const line of match[1].split(/\r?\n/)) {
    const item = /^\s+-\s*(.*)$/.exec(line);
    if (item && fields.length > 0) {
      const last = fields[fields.length - 1];
      const value = unquote(item[1]);
      last.value = last.value ? `${last.value}, ${value}` : value;
      continue;
    }
    const pair = /^([^\s:#][^:]*):\s*(.*)$/.exec(line);
    if (pair) fields.push({ key: pair[1].trim(), value: unquote(pair[2]) });
  }
  return { fields, body: text.slice(match[0].length).replace(/^\s*\n/, "") };
}

function unquote(value: string): string {
  const v = value.trim();
  if (v === "[]" || v === "{}" || v === "null" || v === "~") return "";
  if (/^\[.*\]$/.test(v)) return v.slice(1, -1).split(",").map((s) => unquote(s)).filter(Boolean).join(", ");
  if ((v.startsWith('"') && v.endsWith('"')) || (v.startsWith("'") && v.endsWith("'"))) return v.slice(1, -1);
  return v;
}
