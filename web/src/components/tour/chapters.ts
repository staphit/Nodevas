/**
 * Onboarding tour chapters. Titles and bodies are i18n keys (tour.step.*).
 *
 * The tour is no longer one long linear walk: each chapter is a short 2-4
 * step spotlight sequence that plays the first time the user actually enters
 * that area (pointerdown on the region, or the drawer opening). Steps whose
 * target is missing or has zero size are skipped at runtime, so chapters can
 * safely mention UI that only exists in some layouts.
 */

export interface TourStep {
  id: string;
  /** CSS selector of the element to spotlight. Omit for a centered card. */
  target?: string;
  titleKey: string;
  bodyKey: string;
  placement?: "top" | "bottom" | "left" | "right" | "center";
}

export type TourTrigger =
  | "manual"
  | { kind: "pointerdown"; selector: string }
  | { kind: "drawer-open" };

export interface TourChapter {
  id: string;
  trigger: TourTrigger;
  steps: TourStep[];
}

export const TOUR_CHAPTERS: TourChapter[] = [
  {
    // Played right after「開始導覽」; never auto-triggers contextually.
    id: "shell",
    trigger: "manual",
    steps: [
      {
        id: "shell-explorer",
        target: "#project-explorer",
        titleKey: "tour.step.shell-explorer.title",
        bodyKey: "tour.step.shell-explorer.body",
        placement: "right",
      },
      {
        id: "shell-command",
        target: ".command-trigger",
        titleKey: "tour.step.shell-command.title",
        bodyKey: "tour.step.shell-command.body",
        placement: "bottom",
      },
      {
        id: "shell-replay",
        target: '[data-tour="tour-replay"]',
        titleKey: "tour.step.shell-replay.title",
        bodyKey: "tour.step.shell-replay.body",
        placement: "bottom",
      },
    ],
  },
  {
    id: "graph",
    trigger: { kind: "pointerdown", selector: ".lane-wrap.graph" },
    steps: [
      {
        id: "graph-create",
        target: ".board-primary-action",
        titleKey: "tour.step.graph-create.title",
        bodyKey: "tour.step.graph-create.body",
        placement: "bottom",
      },
      {
        id: "graph-card",
        target: ".lane-wrap.graph",
        titleKey: "tour.step.graph-card.title",
        bodyKey: "tour.step.graph-card.body",
        placement: "bottom",
      },
      {
        id: "graph-link",
        target: ".lane-wrap.graph",
        titleKey: "tour.step.graph-link.title",
        bodyKey: "tour.step.graph-link.body",
        placement: "bottom",
      },
      {
        id: "graph-batch",
        target: ".lane-wrap.graph",
        titleKey: "tour.step.graph-batch.title",
        bodyKey: "tour.step.graph-batch.body",
        placement: "bottom",
      },
    ],
  },
  {
    id: "timeline",
    // Not while folded: a click on the header there is opening it, and the
    // steps point at a board that is not drawn yet.
    trigger: { kind: "pointerdown", selector: ".lane-wrap.timeline:not(.pane-collapsed)" },
    steps: [
      {
        id: "timeline-cells",
        target: ".lane-wrap.timeline",
        titleKey: "tour.step.timeline-cells.title",
        bodyKey: "tour.step.timeline-cells.body",
        placement: "top",
      },
      {
        id: "timeline-drag",
        target: ".lane-wrap.timeline",
        titleKey: "tour.step.timeline-drag.title",
        bodyKey: "tour.step.timeline-drag.body",
        placement: "top",
      },
      {
        id: "timeline-orientation",
        target: ".timeline-orientation",
        titleKey: "tour.step.timeline-orientation.title",
        bodyKey: "tour.step.timeline-orientation.body",
        placement: "bottom",
      },
    ],
  },
  {
    id: "drawer",
    trigger: { kind: "drawer-open" },
    steps: [
      {
        id: "drawer-panel",
        target: ".drawer",
        titleKey: "tour.step.drawer-panel.title",
        bodyKey: "tour.step.drawer-panel.body",
        placement: "left",
      },
      {
        id: "drawer-views",
        target: ".node-view-tabs",
        titleKey: "tour.step.drawer-views.title",
        bodyKey: "tour.step.drawer-views.body",
        placement: "left",
      },
      {
        id: "drawer-save",
        target: ".drawer",
        titleKey: "tour.step.drawer-save.title",
        bodyKey: "tour.step.drawer-save.body",
        placement: "left",
      },
    ],
  },
  {
    id: "explorer",
    trigger: { kind: "pointerdown", selector: "#project-explorer" },
    steps: [
      {
        id: "explorer-tree",
        target: "#project-explorer",
        titleKey: "tour.step.explorer-tree.title",
        bodyKey: "tour.step.explorer-tree.body",
        placement: "right",
      },
      {
        id: "explorer-transfer",
        target: "#project-explorer",
        titleKey: "tour.step.explorer-transfer.title",
        bodyKey: "tour.step.explorer-transfer.body",
        placement: "right",
      },
    ],
  },
];

export function chapterById(id: string): TourChapter | undefined {
  return TOUR_CHAPTERS.find((chapter) => chapter.id === id);
}
