// The scenes' zoom: − / percentage / +, in steps of 10%.

import { useI18n } from "../i18n";

export const MIN_ZOOM = 0.4;
export const MAX_ZOOM = 2;

export function clampZoom(value: number): number {
  return Math.max(MIN_ZOOM, Math.min(MAX_ZOOM, Math.round(value * 20) / 20));
}

export function ZoomControl({ zoom, onZoom }: { zoom: number; onZoom: (zoom: number) => void }) {
  const { t } = useI18n();
  return (
    <span className="game-zoom" role="group" aria-label={t("game.zoom.label")}>
      <button
        type="button"
        className="board-tool-button"
        aria-label={t("game.zoom.out")}
        disabled={zoom <= MIN_ZOOM}
        onClick={() => onZoom(clampZoom(zoom - 0.1))}
      >
        −
      </button>
      <output aria-live="polite">{Math.round(zoom * 100)}%</output>
      <button
        type="button"
        className="board-tool-button"
        aria-label={t("game.zoom.in")}
        disabled={zoom >= MAX_ZOOM}
        onClick={() => onZoom(clampZoom(zoom + 0.1))}
      >
        +
      </button>
    </span>
  );
}
