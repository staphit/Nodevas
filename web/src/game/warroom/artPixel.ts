// One art pixel in CSS pixels, on the device's pixel grid: the war room and
// the office-style previews (Settings) both size their sprites with it.

import { useEffect, useState } from "react";

/** Art pixels per CSS pixel aimed for; the real size rounds to whole device
 * pixels. */
export const ART_PIXEL = 3;

/** One art pixel in CSS pixels: the nearest whole number of device pixels
 * to `target` CSS pixels, so at a ratio of 1.25 or 1.5 (or a browser zoom)
 * every art pixel is the same width instead of alternating 4 and 5. */
export function artPixel(ratio: number, target = ART_PIXEL): number {
  const dpr = Number.isFinite(ratio) && ratio > 0 ? ratio : 1;
  return Math.max(1, Math.round(target * dpr)) / dpr;
}

function devicePixelRatio(): number {
  return typeof window === "undefined" ? 1 : window.devicePixelRatio || 1;
}

/** artPixel for this screen, followed across a zoom or a move to another
 * monitor. */
export function useArtPixel(target = ART_PIXEL): number {
  const [px, setPx] = useState(() => artPixel(devicePixelRatio(), target));
  useEffect(() => {
    if (typeof window === "undefined" || !window.matchMedia) return;
    let list: MediaQueryList | null = null;
    const watch = () => {
      list?.removeEventListener?.("change", watch);
      setPx(artPixel(devicePixelRatio(), target));
      list = window.matchMedia(`(resolution: ${devicePixelRatio()}dppx)`);
      list.addEventListener?.("change", watch);
    };
    watch();
    return () => list?.removeEventListener?.("change", watch);
  }, [target]);
  return px;
}
