/**
 * The node rows of one explorer list, windowed when the list is long.
 *
 * A 2000-node project rendered every row — about 25k DOM nodes — while a
 * sidebar shows a few dozen. Past WINDOW_FROM rows, only those in or near the
 * explorer's scrolled view are mounted, each placed absolutely in a box as
 * tall as the whole list, so the scrollbar and drop targets stay where they
 * were. A shorter list renders every row as before.
 *
 * What windowing would otherwise break is kept: each row says where it sits
 * (aria-posinset / aria-setsize), the arrow keys and Home/End walk the whole
 * list (mounting the row they reach before focusing it), the focused row
 * stays mounted however far the list scrolls, and the open node is brought
 * into view when it changes.
 */

import {
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  type CSSProperties,
  type FocusEvent,
  type KeyboardEvent,
  type ReactNode,
} from "react";
import type { GraphNode } from "../../types";

/** Shorter lists are cheap enough to render whole. */
export const WINDOW_FROM = 80;
/** Rows mounted beyond each edge of the view, so a scroll does not flash blank. */
const OVERSCAN = 8;
/** Until a row has been measured: the stylesheet's minimum row height. */
const ROW_FALLBACK_H = 36;

/** What a row's <li> carries; the tree's renderNode spreads it on. */
export interface NodeRowProps {
  "data-row": number;
  "aria-posinset": number;
  "aria-setsize": number;
  style?: CSSProperties;
  onKeyDown: (event: KeyboardEvent<HTMLElement>) => void;
  onFocus: (event: FocusEvent<HTMLElement>) => void;
  onBlur: (event: FocusEvent<HTMLElement>) => void;
}

/** The nearest ancestor that scrolls vertically, or null for the window. */
function scrollParent(element: HTMLElement): HTMLElement | null {
  for (let node = element.parentElement; node; node = node.parentElement) {
    const overflow = getComputedStyle(node).overflowY;
    if (overflow === "auto" || overflow === "scroll") return node;
  }
  return null;
}

type Range = { first: number; last: number };

/** The rows of `count` that overlap the scrolled view, plus the overscan. */
function visibleRange(box: HTMLElement, count: number, rowHeight: number): Range {
  const scroller = scrollParent(box);
  const top = box.getBoundingClientRect().top;
  const viewTop = scroller ? Math.max(0, scroller.getBoundingClientRect().top) : 0;
  const viewBottom = scroller
    ? Math.min(window.innerHeight, scroller.getBoundingClientRect().top + scroller.clientHeight)
    : window.innerHeight;
  const first = Math.max(0, Math.floor((viewTop - top) / rowHeight) - OVERSCAN);
  const last = Math.min(count - 1, Math.ceil((viewBottom - top) / rowHeight) + OVERSCAN);
  return { first, last };
}

/** The focusable control inside a row, or the row itself. */
function focusTarget(row: Element | null): HTMLElement | null {
  if (!row) return null;
  return row.querySelector<HTMLElement>("button, a[href], [tabindex]") ?? (row as HTMLElement);
}

export function NodeRows({
  nodes,
  renderNode,
  activeNodeId,
}: {
  nodes: GraphNode[];
  renderNode: (node: GraphNode, row: NodeRowProps) => ReactNode;
  activeNodeId?: string | null;
}) {
  const count = nodes.length;
  const windowed = count > WINDOW_FROM;
  const boxRef = useRef<HTMLUListElement>(null);
  const [rowHeight, setRowHeight] = useState(ROW_FALLBACK_H);
  const [range, setRange] = useState<Range>({ first: 0, last: Math.min(count, 40) - 1 });
  /** The row holding keyboard focus: never unmounted while it does. */
  const [focused, setFocused] = useState<number | null>(null);
  /** A row the arrow keys moved to, focused once it is mounted. */
  const [pendingFocus, setPendingFocus] = useState<number | null>(null);
  /** Where the last keyboard move started, to find its list's rows again. */
  const listRef = useRef<HTMLElement | null>(null);
  const activeIndex = activeNodeId ? nodes.findIndex((node) => node.id === activeNodeId) : -1;

  // Recomputed after every render as well as on scroll: anything above the
  // list (a folder opening, the tools unfolding) moves it without a scroll.
  useLayoutEffect(() => {
    if (!windowed || !boxRef.current) return;
    const next = visibleRange(boxRef.current, count, rowHeight);
    setRange((current) =>
      current.first === next.first && current.last === next.last ? current : next,
    );
    const measured = boxRef.current
      .querySelector(":scope > [data-row]")
      ?.getBoundingClientRect().height;
    if (measured && Math.abs(measured - rowHeight) > 0.5) setRowHeight(measured);
  });

  useEffect(() => {
    const box = boxRef.current;
    if (!windowed || !box) return;
    let frame = 0;
    const update = () => {
      cancelAnimationFrame(frame);
      frame = requestAnimationFrame(() => {
        const next = visibleRange(box, count, rowHeight);
        setRange((current) =>
          current.first === next.first && current.last === next.last ? current : next,
        );
      });
    };
    const scroller = scrollParent(box);
    const target: HTMLElement | Window = scroller ?? window;
    target.addEventListener("scroll", update, { passive: true });
    window.addEventListener("resize", update);
    const observer =
      typeof ResizeObserver === "undefined" || !scroller ? null : new ResizeObserver(update);
    observer?.observe(scroller!);
    return () => {
      cancelAnimationFrame(frame);
      target.removeEventListener("scroll", update);
      window.removeEventListener("resize", update);
      observer?.disconnect();
    };
  }, [windowed, count, rowHeight]);

  const rowElement = (index: number): Element | null => {
    const list = windowed ? boxRef.current : listRef.current;
    return list?.querySelector(`:scope > [data-row="${index}"]`) ?? null;
  };

  useEffect(() => {
    if (pendingFocus === null) return;
    focusTarget(rowElement(pendingFocus))?.focus();
    setPendingFocus(null);
  });

  // The open node, brought into view when it changes — mounted first if the
  // window did not reach it.
  const lastRevealed = useRef<string | null>(null);
  useEffect(() => {
    if (!activeNodeId || activeIndex < 0) {
      lastRevealed.current = activeNodeId ?? null;
      return;
    }
    if (lastRevealed.current === activeNodeId) return;
    const row = windowed
      ? boxRef.current?.querySelector(`:scope > [data-row="${activeIndex}"]`)
      : null;
    if (windowed && !row) return; // mounted on the next render
    lastRevealed.current = activeNodeId;
    (row as (Element & { scrollIntoView?: Element["scrollIntoView"] }) | null)?.scrollIntoView?.({
      block: "nearest",
    });
  });

  const rowProps = (index: number): NodeRowProps => ({
    "data-row": index,
    "aria-posinset": index + 1,
    "aria-setsize": count,
    style: windowed
      ? { position: "absolute", top: index * rowHeight, left: 0, right: 0 }
      : undefined,
    onKeyDown: (event) => {
      if (event.altKey || event.ctrlKey || event.metaKey || event.shiftKey) return;
      const to =
        event.key === "ArrowDown"
          ? index + 1
          : event.key === "ArrowUp"
            ? index - 1
            : event.key === "Home"
              ? 0
              : event.key === "End"
                ? count - 1
                : null;
      if (to === null || to < 0 || to >= count || to === index) return;
      event.preventDefault();
      listRef.current = event.currentTarget.parentElement;
      setFocused(to);
      setPendingFocus(to);
    },
    onFocus: () => setFocused(index),
    onBlur: (event) => {
      if (event.currentTarget.contains(event.relatedTarget as Node | null)) return;
      setFocused((current) => (current === index ? null : current));
    },
  });

  if (!windowed) {
    return <>{nodes.map((node, index) => renderNode(node, rowProps(index)))}</>;
  }

  const mounted = new Set<number>();
  for (let index = range.first; index <= range.last; index += 1) mounted.add(index);
  for (const extra of [focused, pendingFocus, activeIndex]) {
    if (extra !== null && extra >= 0 && extra < count) mounted.add(extra);
  }
  return (
    <li className="node-rows-window" role="none">
      <ul
        ref={boxRef}
        className="node-list tree-node-list node-rows-box"
        style={{ position: "relative", height: count * rowHeight }}
      >
        {Array.from(mounted)
          .sort((a, b) => a - b)
          .map((index) => renderNode(nodes[index], rowProps(index)))}
      </ul>
    </li>
  );
}
