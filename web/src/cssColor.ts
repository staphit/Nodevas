/**
 * A stored colour goes straight into CSS custom properties on every reader's
 * board, where `url(https://…)` would fetch a remote image (the reader's IP
 * and when they looked). Only colour-shaped values pass: hex, a name or a
 * colour function, with no ':', '/', quotes or escapes. The hub refuses the
 * rest on save (internal/store/graph_validate.go); this covers older files.
 */
const COLOR = /^[#a-zA-Z0-9(),.% -]*$/;

export function safeCssColor(value: string | undefined): string | undefined {
  if (!value || value.length > 32 || !COLOR.test(value) || /url\(|image/i.test(value)) return undefined;
  return value;
}
