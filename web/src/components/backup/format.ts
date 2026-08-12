/** How the backup dialog turns server values into text. */

import { formatLocalizedDateTime, translate } from "../../i18n";

export function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

export function formatWhen(iso: string): string {
  const at = new Date(iso);
  return formatLocalizedDateTime(at);
}

/**
 * How much wall-clock history a retention count buys at the configured
 * interval. A bare count teaches nobody: "保留最近 14 份" is meaningless until
 * you know whether a bundle is taken every hour or every week, and the interval
 * is exactly the setting people change.
 */
export function formatCoverage(count: number, hours: number): string {
  const total = count * hours;
  if (!Number.isFinite(total) || total <= 0) return "";
  if (total < 48) return translate("backup.approxHours", undefined, { count: Math.round(total) });
  const days = total / 24;
  return translate("backup.approxDays", undefined, {
    count: days.toFixed(1).replace(/\.0$/, ""),
  });
}

/** A thrown value as something worth showing a person. */
export function reason(error: unknown, fallback: string): string {
  return error instanceof Error && error.message ? error.message : fallback;
}
