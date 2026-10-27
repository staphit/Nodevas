// Whether the game views may move. A person who asked their system for
// reduced motion gets still sprites (the CSS keys off data-motion as well as
// the media query, so effects started from script stop too), and a page in a
// background tab pauses every animation.

import { useEffect, useState } from "react";

const QUERY = "(prefers-reduced-motion: reduce)";

function prefersReduced(): boolean {
  return typeof window !== "undefined" && Boolean(window.matchMedia?.(QUERY).matches);
}

export function useReducedMotion(): boolean {
  const [reduced, setReduced] = useState(prefersReduced);
  useEffect(() => {
    const list = window.matchMedia?.(QUERY);
    if (!list) return;
    const onChange = () => setReduced(list.matches);
    onChange();
    list.addEventListener?.("change", onChange);
    return () => list.removeEventListener?.("change", onChange);
  }, []);
  return reduced;
}

export function usePageHidden(): boolean {
  const [hidden, setHidden] = useState(() => typeof document !== "undefined" && document.hidden);
  useEffect(() => {
    const onChange = () => setHidden(document.hidden);
    document.addEventListener("visibilitychange", onChange);
    return () => document.removeEventListener("visibilitychange", onChange);
  }, []);
  return hidden;
}
