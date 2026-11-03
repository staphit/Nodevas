/**
 * Sets the interface's text size: every fixed font size in the stylesheets
 * is multiplied by --font-scale, so one property resizes all the text and
 * nothing else — layout and pointer coordinates stay as they are.
 */
export function applyTextScale(scale: number, root: HTMLElement = document.documentElement): void {
  root.style.setProperty("--font-scale", String(scale));
}
