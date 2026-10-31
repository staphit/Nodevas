import { localizedStatusLabel } from "./i18n";
import type { BuiltinStatus, Status, StatusDefinition } from "./types";

export interface StatusTheme {
  label: string;
  /** CSS variable refs so both light and dark themes resolve correctly */
  color: string;
  bg: string;
  shape: "circle" | "square" | "diamond" | "triangle" | "dash";
}

// Warm monochrome base + muted pastel accents; actual values live in CSS
// custom properties (app.css) with light/dark variants.
// Labels are read from the catalog when asked for (statusTheme), so they
// follow the interface language.
export const STATUS_THEME: Record<BuiltinStatus, Omit<StatusTheme, "label">> = {
  // The server's decode-only locked state renders like not-started.
  locked: { color: "var(--st-ready-fg)", bg: "var(--st-ready-bg)", shape: "circle" },
  ready: { color: "var(--st-ready-fg)", bg: "var(--st-ready-bg)", shape: "circle" },
  started: { color: "var(--st-ready-fg)", bg: "var(--st-ready-bg)", shape: "circle" },
  in_progress: { color: "var(--st-progress-fg)", bg: "var(--st-progress-bg)", shape: "diamond" },
  done: { color: "var(--st-done-fg)", bg: "var(--st-done-bg)", shape: "square" },
  skipped: { color: "var(--st-skipped-fg)", bg: "var(--st-skipped-bg)", shape: "dash" },
  failed: { color: "var(--st-failed-fg)", bg: "var(--st-failed-bg)", shape: "triangle" },
  deprecated: {
    color: "var(--st-deprecated-fg)",
    bg: "var(--st-deprecated-bg)",
    shape: "dash",
  },
};

/** User-selectable states. "locked" remains decode-only and is shown as ready. */
export const STATUS_OPTIONS: BuiltinStatus[] = [
  "ready",
  "started",
  "in_progress",
  "done",
  "skipped",
  "failed",
  "deprecated",
];

export function statusOptions(definitions: StatusDefinition[] = []): Status[] {
  return [...STATUS_OPTIONS, ...definitions.map((definition) => definition.id)];
}

export function statusTheme(
  status: Status,
  definitions: StatusDefinition[] = [],
): StatusTheme {
  const builtin = STATUS_THEME[status as BuiltinStatus];
  if (builtin) return { ...builtin, label: localizedStatusLabel(status) };
  const definition = definitions.find((candidate) => candidate.id === status);
  const color = definition?.color || "#8b7cf6";
  return {
    label: definition?.label || status.replace(/^custom-status-/, ""),
    color,
    bg: `color-mix(in srgb, ${color} 16%, transparent)`,
    shape: definition?.shape || "circle",
  };
}

/** Small SVG status marker: one distinct shape per status (not color-only). */
export function StatusShape({
  status,
  size = 12,
  definitions = [],
}: {
  status: Status;
  size?: number;
  definitions?: StatusDefinition[];
}) {
  const t = statusTheme(status, definitions);
  const s = size;
  const half = s / 2;
  let el: React.ReactNode;
  switch (t.shape) {
    case "circle":
      el = <circle cx={half} cy={half} r={half - 1} fill={t.color} />;
      break;
    case "square":
      el = <rect x={1} y={1} width={s - 2} height={s - 2} rx={2} fill={t.color} />;
      break;
    case "diamond":
      el = (
        <polygon
          points={`${half},1 ${s - 1},${half} ${half},${s - 1} 1,${half}`}
          fill={t.color}
        />
      );
      break;
    case "triangle":
      el = <polygon points={`${half},1 ${s - 1},${s - 1} 1,${s - 1}`} fill={t.color} />;
      break;
    case "dash":
      el = <rect x={1} y={half - 2} width={s - 2} height={4} rx={2} fill={t.color} />;
      break;
  }
  return (
    <svg width={s} height={s} viewBox={`0 0 ${s} ${s}`} style={{ flexShrink: 0 }}>
      {el}
    </svg>
  );
}
