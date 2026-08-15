/**
 * Opt-in, contextual onboarding tour.
 *
 * A welcome dialog asks once whether the user wants guidance at all
 * (`tourPrompt` preference). After opting in, each chapter — a short 2-4 step
 * spotlight sequence — plays the first time the user actually enters that
 * area: pointerdown on the region, or the node drawer opening. Finished (or
 * skipped) chapters are recorded in `tourChaptersDone` and never nag again.
 *
 * Spotlight mechanics are plain in-tree fixed overlays (no portal), same
 * pattern as the command palette and confirm dialogs. The darkening comes
 * from the highlight box's huge box-shadow, which leaves a "cutout" over the
 * active target; a separate transparent backdrop swallows clicks outside the
 * tour. Steps whose target is missing or has zero size are skipped in the
 * direction of travel, so a chapter never breaks when a pane is collapsed.
 */

import { useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";
import { useApp, usePreference } from "../../store";
import { TOUR_CHAPTERS, type TourChapter, type TourStep } from "./chapters";

const HIGHLIGHT_PAD = 8;
const POPOVER_GAP = 12;
const VIEWPORT_MARGIN = 8;
const WELCOME_DELAY = 800;
const POINTER_TRIGGER_DELAY = 300;
const DRAWER_TRIGGER_DELAY = 400;

interface Box {
  top: number;
  left: number;
  width: number;
  height: number;
}

/** The target's rect, or null when the step has no usable target. */
function measureStep(step: TourStep): Box | null {
  if (!step.target) return null;
  let el: Element | null = null;
  try {
    el = document.querySelector(step.target);
  } catch {
    return null;
  }
  if (!el) return null;
  const rect = el.getBoundingClientRect();
  if (rect.width <= 0 || rect.height <= 0) return null;
  return { top: rect.top, left: rect.left, width: rect.width, height: rect.height };
}

/** A step is usable when it needs no target, or its target is visible. */
function stepUsable(step: TourStep): boolean {
  return !step.target || measureStep(step) !== null;
}

/** Nearest usable step from `from` moving in `dir`, or null when none. */
function findStep(steps: TourStep[], from: number, dir: 1 | -1): number | null {
  for (let i = from; i >= 0 && i < steps.length; i += dir) {
    if (stepUsable(steps[i])) return i;
  }
  return null;
}

function boxesEqual(a: Box | null, b: Box | null): boolean {
  if (a === null || b === null) return a === b;
  return a.top === b.top && a.left === b.left && a.width === b.width && a.height === b.height;
}

export function OnboardingTour() {
  const tourPrompt = usePreference("tourPrompt");
  const chaptersDoneRaw = usePreference("tourChaptersDone");
  const updateUIPreference = useApp((s) => s.updateUIPreference);
  const activeTab = useApp((s) => s.activeTab);

  // Before the schema entry lands (or on a fresh profile) the store may hand
  // back undefined; treat that as "nothing done yet".
  const chaptersDone: string[] = Array.isArray(chaptersDoneRaw) ? chaptersDoneRaw : [];

  const [welcomeOpen, setWelcomeOpen] = useState(false);
  const [chapter, setChapter] = useState<TourChapter | null>(null);
  const [stepIndex, setStepIndex] = useState(0);
  const [targetBox, setTargetBox] = useState<Box | null>(null);
  const [popoverPos, setPopoverPos] = useState<{ top: number; left: number }>({
    top: 0,
    left: 0,
  });
  const popoverRef = useRef<HTMLDivElement | null>(null);
  const welcomeRef = useRef<HTMLDivElement | null>(null);
  const promptedRef = useRef(false);
  const pendingStartRef = useRef<number | null>(null);

  // Live mirrors for the document-level pointerdown listener, so it never
  // acts on a stale closure.
  const chapterRef = useRef(chapter);
  chapterRef.current = chapter;
  const welcomeOpenRef = useRef(welcomeOpen);
  welcomeOpenRef.current = welcomeOpen;
  const chaptersDoneRef = useRef(chaptersDone);
  chaptersDoneRef.current = chaptersDone;

  const clearPendingStart = useCallback(() => {
    if (pendingStartRef.current !== null) {
      window.clearTimeout(pendingStartRef.current);
      pendingStartRef.current = null;
    }
  }, []);

  /** Start a chapter at its first usable step; no-op when nothing is visible. */
  const startChapter = useCallback((next: TourChapter) => {
    const usable = findStep(next.steps, 0, 1);
    if (usable === null) return;
    setTargetBox(null); // don't flash the previous chapter's cutout
    setStepIndex(usable);
    setChapter(next);
  }, []);

  /** Record the chapter as seen (dedup) and dismiss the overlay. */
  const finishChapter = useCallback(() => {
    const current = chapterRef.current;
    if (current) {
      const done = chaptersDoneRef.current;
      if (!done.includes(current.id)) {
        updateUIPreference("tourChaptersDone", [...done, current.id]);
      }
    }
    setChapter(null);
  }, [updateUIPreference]);

  const next = useCallback(() => {
    const steps = chapterRef.current?.steps;
    if (!steps) return;
    setStepIndex((current) => {
      const target = findStep(steps, current + 1, 1);
      if (target === null) {
        finishChapter();
        return current;
      }
      return target;
    });
  }, [finishChapter]);

  const back = useCallback(() => {
    const steps = chapterRef.current?.steps;
    if (!steps) return;
    setStepIndex((current) => findStep(steps, current - 1, -1) ?? current);
  }, []);

  // ---------------------------------------------------------------------
  // Welcome dialog: prompt once per mount while the choice is still pending.
  // ---------------------------------------------------------------------
  useEffect(() => {
    if (promptedRef.current) return;
    promptedRef.current = true;
    if (tourPrompt !== "pending") return;
    const timer = window.setTimeout(() => setWelcomeOpen(true), WELCOME_DELAY);
    return () => window.clearTimeout(timer);
  }, [tourPrompt]);

  const acceptTour = useCallback(() => {
    updateUIPreference("tourPrompt", "accepted");
    setWelcomeOpen(false);
    const shell = TOUR_CHAPTERS.find((c) => c.id === "shell");
    if (shell) startChapter(shell);
  }, [updateUIPreference, startChapter]);

  const declineTour = useCallback(() => {
    updateUIPreference("tourPrompt", "declined");
    setWelcomeOpen(false);
  }, [updateUIPreference]);

  /**
   * Turn the whole tour off, not just this chapter.
   *
   * `tourPrompt` back to "declined" is what actually silences the contextual
   * triggers — every one of them requires "accepted" — and it is a stored
   * preference, so the choice survives the reload. Every chapter is marked done
   * as well: the flag and the list are read separately, and a later replay
   * clears both anyway, so leaving the list half-filled would only invite a
   * chapter to reappear if the two ever disagreed.
   */
  const skipEverything = useCallback(() => {
    clearPendingStart();
    updateUIPreference(
      "tourChaptersDone",
      TOUR_CHAPTERS.map((c) => c.id),
    );
    updateUIPreference("tourPrompt", "declined");
    setChapter(null);
    setWelcomeOpen(false);
  }, [updateUIPreference, clearPendingStart]);

  // ---------------------------------------------------------------------
  // Full replay (topbar ? button / command palette): forget every chapter
  // and let the user choose again from the welcome dialog.
  // ---------------------------------------------------------------------
  useEffect(() => {
    const onStart = () => {
      clearPendingStart();
      updateUIPreference("tourChaptersDone", []);
      updateUIPreference("tourPrompt", "pending");
      setChapter(null);
      setWelcomeOpen(true);
    };
    window.addEventListener("nodevas-tour-start", onStart);
    return () => window.removeEventListener("nodevas-tour-start", onStart);
  }, [updateUIPreference, clearPendingStart]);

  // ---------------------------------------------------------------------
  // Contextual trigger 1: first pointerdown inside a chapter's region.
  // Capture phase so collapsed panes / stopPropagation can't hide it; the
  // short delay lets the user's own click land before the spotlight opens.
  // ---------------------------------------------------------------------
  useEffect(() => {
    if (tourPrompt !== "accepted") return;
    const onPointerDown = (event: Event) => {
      if (chapterRef.current || welcomeOpenRef.current) return;
      if (pendingStartRef.current !== null) return;
      const target = event.target as Element | null;
      if (!target || typeof target.closest !== "function") return;
      for (const candidate of TOUR_CHAPTERS) {
        const trigger = candidate.trigger;
        if (typeof trigger === "string" || trigger.kind !== "pointerdown") continue;
        if (chaptersDoneRef.current.includes(candidate.id)) continue;
        if (!target.closest(trigger.selector)) continue;
        pendingStartRef.current = window.setTimeout(() => {
          pendingStartRef.current = null;
          if (chapterRef.current || welcomeOpenRef.current) return;
          if (chaptersDoneRef.current.includes(candidate.id)) return;
          startChapter(candidate);
        }, POINTER_TRIGGER_DELAY);
        return;
      }
    };
    document.addEventListener("pointerdown", onPointerDown, true);
    return () => {
      document.removeEventListener("pointerdown", onPointerDown, true);
      clearPendingStart();
    };
  }, [tourPrompt, startChapter, clearPendingStart]);

  // ---------------------------------------------------------------------
  // Contextual trigger 2: the node drawer opening (activeTab null → id).
  // ---------------------------------------------------------------------
  const prevTabRef = useRef(activeTab);
  useEffect(() => {
    const opened = prevTabRef.current === null && activeTab !== null;
    prevTabRef.current = activeTab;
    if (!opened || tourPrompt !== "accepted") return;
    const drawer = TOUR_CHAPTERS.find(
      (c) => typeof c.trigger !== "string" && c.trigger.kind === "drawer-open",
    );
    if (!drawer || chaptersDoneRef.current.includes(drawer.id)) return;
    if (chapterRef.current || welcomeOpenRef.current || pendingStartRef.current !== null) return;
    pendingStartRef.current = window.setTimeout(() => {
      pendingStartRef.current = null;
      if (chapterRef.current || welcomeOpenRef.current) return;
      if (chaptersDoneRef.current.includes(drawer.id)) return;
      startChapter(drawer);
    }, DRAWER_TRIGGER_DELAY);
  }, [activeTab, tourPrompt, startChapter]);

  // ---------------------------------------------------------------------
  // Spotlight mechanics (unchanged from the linear tour).
  // ---------------------------------------------------------------------

  // Track the active target's rect: initial measure, resize, capture-phase
  // scroll (both rAF-throttled), and a ResizeObserver on the element itself.
  useEffect(() => {
    if (!chapter) return;
    const step = chapter.steps[stepIndex];
    let raf = 0;
    const update = () => {
      const box = measureStep(step);
      setTargetBox((prev) => (boxesEqual(prev, box) ? prev : box));
    };
    const schedule = () => {
      if (raf) return;
      raf = requestAnimationFrame(() => {
        raf = 0;
        update();
      });
    };
    update();
    window.addEventListener("resize", schedule);
    window.addEventListener("scroll", schedule, true);
    let observer: ResizeObserver | undefined;
    if (step.target && typeof ResizeObserver !== "undefined") {
      try {
        const el = document.querySelector(step.target);
        if (el) {
          observer = new ResizeObserver(schedule);
          observer.observe(el);
        }
      } catch {
        /* invalid selector or observer failure: measure-on-event still works */
      }
    }
    return () => {
      if (raf) cancelAnimationFrame(raf);
      window.removeEventListener("resize", schedule);
      window.removeEventListener("scroll", schedule, true);
      observer?.disconnect();
    };
  }, [chapter, stepIndex]);

  // Keyboard: → / Enter next, ← back, Esc closes and marks the chapter done.
  useEffect(() => {
    if (!chapter) return;
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        event.preventDefault();
        finishChapter();
        return;
      }
      if (event.key === "ArrowRight") {
        event.preventDefault();
        next();
        return;
      }
      if (event.key === "ArrowLeft") {
        event.preventDefault();
        back();
        return;
      }
      if (event.key === "Enter") {
        // A focused button already advances via its click; don't double-step.
        const target = event.target as HTMLElement | null;
        if (target?.closest?.(".tour-popover button")) return;
        event.preventDefault();
        next();
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [chapter, finishChapter, next, back]);

  // Welcome dialog keyboard: Esc counts as「先不用」.
  useEffect(() => {
    if (!welcomeOpen) return;
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return;
      event.preventDefault();
      declineTour();
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [welcomeOpen, declineTour]);

  // Focus the active card so the dialog owns the keyboard.
  useEffect(() => {
    if (chapter) popoverRef.current?.focus();
  }, [chapter, stepIndex]);
  useEffect(() => {
    if (welcomeOpen) welcomeRef.current?.focus();
  }, [welcomeOpen]);

  // Position the popover next to the highlight, clamped to the viewport.
  useLayoutEffect(() => {
    if (!chapter) return;
    const popover = popoverRef.current;
    const pw = popover?.offsetWidth ?? 0;
    const ph = popover?.offsetHeight ?? 0;
    const vw = window.innerWidth;
    const vh = window.innerHeight;
    let top = (vh - ph) / 2;
    let left = (vw - pw) / 2;
    if (targetBox) {
      const hx = targetBox.left - HIGHLIGHT_PAD;
      const hy = targetBox.top - HIGHLIGHT_PAD;
      const hw = targetBox.width + HIGHLIGHT_PAD * 2;
      const hh = targetBox.height + HIGHLIGHT_PAD * 2;
      const placement = chapter.steps[stepIndex].placement ?? "bottom";
      switch (placement) {
        case "top":
          top = hy - POPOVER_GAP - ph;
          left = hx + hw / 2 - pw / 2;
          break;
        case "left":
          top = hy + hh / 2 - ph / 2;
          left = hx - POPOVER_GAP - pw;
          break;
        case "right":
          top = hy + hh / 2 - ph / 2;
          left = hx + hw + POPOVER_GAP;
          break;
        case "center":
          break;
        case "bottom":
        default:
          top = hy + hh + POPOVER_GAP;
          left = hx + hw / 2 - pw / 2;
          break;
      }
    }
    top = Math.max(VIEWPORT_MARGIN, Math.min(top, vh - ph - VIEWPORT_MARGIN));
    left = Math.max(VIEWPORT_MARGIN, Math.min(left, vw - pw - VIEWPORT_MARGIN));
    setPopoverPos((prev) => (prev.top === top && prev.left === left ? prev : { top, left }));
  }, [chapter, stepIndex, targetBox]);

  // ---------------------------------------------------------------------
  // Render
  // ---------------------------------------------------------------------

  if (welcomeOpen) {
    return (
      <div className="tour-welcome-backdrop">
        <div
          ref={welcomeRef}
          className="tour-welcome"
          role="dialog"
          aria-modal="true"
          aria-label="歡迎使用 Nodevas"
          tabIndex={-1}
        >
          <strong className="tour-title">歡迎使用 Nodevas</strong>
          <p className="tour-body">
            要花 30 秒認識一下介面嗎?之後點到各區塊時,會在第一次使用時給你簡短提示。
          </p>
          <div className="tour-actions">
            <button type="button" className="tour-btn tour-btn--ghost" onClick={declineTour}>
              先不用
            </button>
            <span className="tour-actions-spacer" />
            <button type="button" className="tour-btn tour-btn--primary" onClick={acceptTour}>
              開始導覽
            </button>
          </div>
        </div>
      </div>
    );
  }

  if (!chapter) return null;

  const step = chapter.steps[stepIndex];
  const isLast = findStep(chapter.steps, stepIndex + 1, 1) === null;
  const hasPrev = findStep(chapter.steps, stepIndex - 1, -1) !== null;

  return (
    <>
      <div
        className={"tour-backdrop" + (targetBox ? "" : " tour-backdrop--dim")}
        aria-hidden="true"
      />
      {targetBox && (
        <div
          className="tour-highlight"
          aria-hidden="true"
          style={{
            top: targetBox.top - HIGHLIGHT_PAD,
            left: targetBox.left - HIGHLIGHT_PAD,
            width: targetBox.width + HIGHLIGHT_PAD * 2,
            height: targetBox.height + HIGHLIGHT_PAD * 2,
          }}
        />
      )}
      <div
        ref={popoverRef}
        className="tour-popover"
        style={{ top: popoverPos.top, left: popoverPos.left }}
        role="dialog"
        aria-modal="true"
        aria-label={step.title}
        tabIndex={-1}
      >
        <strong className="tour-title">{step.title}</strong>
        <p className="tour-body">{step.body}</p>
        <div className="tour-dots" aria-hidden="true">
          {chapter.steps.map((s, index) => (
            <span
              key={s.id}
              className={"tour-dot" + (index === stepIndex ? " tour-dot--active" : "")}
            />
          ))}
        </div>
        <div className="tour-actions">
          <button type="button" className="tour-btn tour-btn--ghost" onClick={finishChapter}>
            略過
          </button>
          {/* 略過 only ends this chapter; someone who wants none of it should
            * not have to dismiss four more. */}
          <button
            type="button"
            className="tour-btn tour-btn--ghost"
            onClick={skipEverything}
            title="不再顯示任何導覽提示"
          >
            全部略過
          </button>
          <span className="tour-actions-spacer" />
          {hasPrev && (
            <button type="button" className="tour-btn" onClick={back}>
              上一步
            </button>
          )}
          <button type="button" className="tour-btn tour-btn--primary" onClick={next}>
            {isLast ? "完成" : "下一步"}
          </button>
        </div>
      </div>
    </>
  );
}
