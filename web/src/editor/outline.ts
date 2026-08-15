/**
 * Document outline.
 *
 * Headings are parsed from the markdown source rather than from the rendered
 * HTML, so the outline is identical in live, source and preview mode — and it
 * knows the line number, which is what jumping needs.
 */

export interface OutlineEntry {
  /** 1-6, from the number of `#`. */
  level: number;
  text: string;
  /** 1-based line number, as CodeMirror counts them. */
  line: number;
  /** Character offset of the line start, for moving the caret. */
  from: number;
}

const ATX = /^(#{1,6})\s+(.*?)(?:\s+#+)?\s*$/;
const FENCE = /^(?:```|~~~)/;

export function parseOutline(source: string): OutlineEntry[] {
  const entries: OutlineEntry[] = [];
  let offset = 0;
  let inFence = false;

  source.split("\n").forEach((line, index) => {
    if (FENCE.test(line.trim())) inFence = !inFence;
    else if (!inFence) {
      const match = ATX.exec(line);
      if (match && match[2].trim()) {
        entries.push({
          level: match[1].length,
          text: match[2].trim(),
          line: index + 1,
          from: offset,
        });
      }
    }
    offset += line.length + 1;
  });

  return entries;
}

/** Smallest heading level present, so the outline is not indented for nothing. */
export function outlineBaseLevel(entries: OutlineEntry[]): number {
  return entries.reduce((base, entry) => Math.min(base, entry.level), 6);
}
