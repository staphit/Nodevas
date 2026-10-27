import type { PickReasonCode, SuggestionChange } from "../api/analytics";

type T = (key: string, values?: Record<string, string | number>) => string;

/** A rate as a percentage; an em dash when there is none. */
export function formatRate(_value: number | null): string {
  // TODO(green): implement.
  return "";
}

/** A cost in USD, "(est.)" when any part of it is an estimate. */
export function formatCost(_value: number | null, _estimated: boolean, _t: T): string {
  // TODO(green): implement.
  return "";
}

/** A change, for the confirm dialog. */
export function changeText(_change: SuggestionChange, _t: T): string {
  // TODO(green): implement.
  return "";
}

/** A pick's reason. */
export function pickReasonText(_code: PickReasonCode | string, _values: Record<string, string | number>, _t: T): string {
  // TODO(green): implement.
  return "";
}
