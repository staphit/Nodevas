/**
 * How far the node drawer reaches into a pane's toolbar.
 *
 * The drawer is laid over the panes' right side rather than beside them, and
 * a pane's toolbar keeps Problems, the timeline orientation switch, ? and ⋯ at
 * its right end — exactly under a 580px drawer at 1280px. The pane pads its
 * toolbar by this much (10a-canvas-tools.css) so those stay clickable while a
 * node is open.
 */

import { useLayoutEffect, useState, type RefObject } from "react";
import { restingRect, type Area } from "./boardVisibleArea";

/**
 * The width of `bar` covered from its right edge by a side drawer. A drawer
 * that is not beside the bar (a phone's sheet below it) or that covers the
 * whole bar (no padding brings that back) counts for nothing.
 */
export function drawerCover(bar: Area, drawers: readonly Area[]): number {
  let cover = 0;
  for (const drawer of drawers) {
    const beside = drawer.top < bar.bottom && drawer.bottom > bar.top;
    const reaches = drawer.left < bar.right && drawer.right > bar.left;
    if (!beside || !reaches || drawer.left <= bar.left) continue;
    cover = Math.max(cover, bar.right - drawer.left);
  }
  return Math.round(cover);
}

/**
 * The pane's current drawer cover, in px. The drawer is a sibling of the
 * panes in the stage; it comes and goes (and its lazy fallback is swapped for
 * it) as a child of the stage, and changes width while it is dragged or
 * collapsed to its rail.
 */
export function useDrawerCover(paneRef: RefObject<HTMLElement | null>): number {
  const [cover, setCover] = useState(0);
  useLayoutEffect(() => {
    const pane = paneRef.current;
    const stage = pane?.parentElement;
    if (!pane || !stage) return;
    const drawers = () =>
      Array.from(stage.children).filter(
        (child): child is HTMLElement => child instanceof HTMLElement && child.classList.contains("drawer"),
      );
    const measure = () => {
      const bar = pane.querySelector<HTMLElement>(":scope > .board-title");
      setCover(bar ? drawerCover(bar.getBoundingClientRect(), drawers().map(restingRect)) : 0);
    };
    const sizes = new ResizeObserver(measure);
    const watch = () => {
      sizes.disconnect();
      sizes.observe(pane);
      for (const drawer of drawers()) sizes.observe(drawer);
    };
    const children = new MutationObserver(() => {
      watch();
      measure();
    });
    children.observe(stage, { childList: true });
    watch();
    measure();
    window.addEventListener("resize", measure);
    return () => {
      children.disconnect();
      sizes.disconnect();
      window.removeEventListener("resize", measure);
    };
  }, [paneRef]);
  return cover;
}
