import { languageLocale } from "../i18n";

/** "1.2M", "34.5k", "812": token counts short enough for a tile or a row. */
export function formatTokens(value: number): string {
  if (!Number.isFinite(value) || value <= 0) return "0";
  return new Intl.NumberFormat(languageLocale(), {
    notation: "compact",
    maximumFractionDigits: value >= 1000 ? 1 : 0,
  }).format(value);
}

/** "12,345,678": the exact count, for a tooltip or a title. */
export function formatTokensExact(value: number): string {
  return new Intl.NumberFormat(languageLocale()).format(Math.max(0, Math.round(value || 0)));
}

/** "$12.34"; anything above zero but under a cent reads "<$0.01", never "$0.00". */
export function formatUSD(value: number): string {
  if (!Number.isFinite(value) || value <= 0) return "$0.00";
  if (value < 0.01) return "<$0.01";
  return new Intl.NumberFormat("en-US", {
    style: "currency",
    currency: "USD",
    minimumFractionDigits: 2,
    maximumFractionDigits: 2,
  }).format(value);
}

/** "42%" of total; "<1%" for a sliver, "" when there is no total. */
export function formatShare(part: number, total: number): string {
  if (!(total > 0) || !(part > 0)) return total > 0 ? "0%" : "";
  const share = (part / total) * 100;
  if (share < 1) return "<1%";
  return `${Math.round(share)}%`;
}

/** The per-day average over a range of `days`. */
export function perDay(value: number, days: number): number {
  return days > 0 ? value / days : 0;
}

/** "Oct 24" / "10月24日" for a YYYY-MM-DD day key, read as UTC. */
export function formatDayKey(key: string): string {
  const at = Date.parse(`${key}T00:00:00Z`);
  if (!Number.isFinite(at)) return key;
  return new Intl.DateTimeFormat(languageLocale(), {
    month: "short",
    day: "numeric",
    timeZone: "UTC",
  }).format(at);
}
