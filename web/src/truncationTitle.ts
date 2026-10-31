// Text cut with an ellipsis (or clamped to a few lines) must still be readable
// whole: on hover or keyboard focus, a cut element that has no tooltip of its
// own gets its full text as its title, and loses that title again once the
// text fits. One listener on the document covers every view, including ones
// that are added later, so no truncated value is left without a tooltip.

const AUTO = "data-auto-title";
/** How far up from the hovered node a cut ancestor is looked for. */
const MAX_DEPTH = 4;

const lineClamped = (style: CSSStyleDeclaration) => {
  const clamp = style.getPropertyValue("-webkit-line-clamp");
  return clamp !== "" && clamp !== "none";
};

// Sub-pixel layout can report a 1px difference for text that fits.
const isCut = (el: HTMLElement, style: CSSStyleDeclaration) =>
  lineClamped(style) ? el.scrollHeight > el.clientHeight + 1 : el.scrollWidth > el.clientWidth + 1;

function sync(start: EventTarget | null) {
  let el = start instanceof Element ? start : null;
  for (let depth = 0; el && el !== document.body && depth < MAX_DEPTH; depth += 1, el = el.parentElement) {
    if (!(el instanceof HTMLElement)) continue;
    const own = el.hasAttribute("title") && !el.hasAttribute(AUTO);
    // The component's own tooltip is the nearest one and already shows.
    if (own) return;
    const style = getComputedStyle(el);
    if (style.textOverflow !== "ellipsis" && !lineClamped(style)) continue;
    if (isCut(el, style)) {
      const text = (el.textContent ?? "").replace(/\s+/g, " ").trim();
      if (text && el.getAttribute("title") !== text) {
        el.setAttribute("title", text);
        el.setAttribute(AUTO, "");
      }
    } else if (el.hasAttribute(AUTO)) {
      el.removeAttribute("title");
      el.removeAttribute(AUTO);
    }
    return;
  }
}

/** Starts filling in titles for cut text under `root`; returns the undo. */
export function installTruncationTitles(root: Document): () => void {
  const onEvent = (event: Event) => sync(event.target);
  root.addEventListener("pointerover", onEvent, true);
  root.addEventListener("focusin", onEvent, true);
  return () => {
    root.removeEventListener("pointerover", onEvent, true);
    root.removeEventListener("focusin", onEvent, true);
  };
}
