import type { PageFormat } from "../../api";

/** What a new subpage can be stored as, and how its tab is labelled. */
export const PAGE_FORMATS: readonly (readonly [PageFormat, string, string])[] = [
  ["md", "Markdown", ".md"],
  ["txt", "純文字", ".txt"],
  ["html", "HTML", ".html"],
  ["docx", "Word", ".docx"],
];

export const PAGE_FORMAT_LABEL: Record<PageFormat, string> = {
  md: "MD",
  txt: "TXT",
  html: "HTML",
  docx: "DOCX",
};

/** Markdown and Word share one editor: a .docx page is edited as Markdown. */
export function editsAsMarkdown(format: PageFormat): boolean {
  return format === "md" || format === "docx";
}

/** Safe in text and in a quoted attribute; `&` goes first so nothing that the
 * later rules write is escaped twice. */
export function escapeHTMLText(text: string): string {
  return text
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&#39;");
}

/** Writes an uploaded file into the page using that format's own syntax. */
export function attachmentSnippet(
  format: PageFormat,
  uploaded: { name: string; url: string },
  isImage: boolean,
): string {
  if (format === "html") {
    return isImage
      ? `<img src="${escapeHTMLText(uploaded.url)}" alt="${escapeHTMLText(uploaded.name)}">\n`
      : `<a href="${escapeHTMLText(uploaded.url)}">${escapeHTMLText(uploaded.name)}</a>\n`;
  }
  if (format === "txt") {
    return `${uploaded.name} ${uploaded.url}\n`;
  }
  return `${isImage ? "!" : ""}[${isImage ? "" : "📎 "}${uploaded.name}](${uploaded.url})\n`;
}
