// How a project's office looks in Station: a preset per project, stored in
// graph.yaml (office_style) and shared with everyone in the project. Unset,
// the layout decides: a story project gets the study, anything else the
// engineering floor. Mirrors internal/engine/officestyle.go; both are pinned
// by testdata/office_styles.json.

import type { OfficeStyle } from "../types";

/** Every style, in the order a picker shows them. */
export const OFFICE_STYLES: readonly OfficeStyle[] = ["default", "engineering", "study", "studio", "cafe"];

export function isOfficeStyle(value: unknown): value is OfficeStyle {
  return typeof value === "string" && (OFFICE_STYLES as readonly string[]).includes(value);
}

/** The style a layout gets when the project has not chosen one. */
export function defaultOfficeStyle(layout: string | undefined): OfficeStyle {
  return layout === "story" ? "study" : "engineering";
}

/** The project's style: its choice when that is a known style, otherwise its
 * layout's default. */
export function resolveOfficeStyle(input: { officeStyle?: string | null; layout?: string | null }): OfficeStyle {
  return isOfficeStyle(input.officeStyle) ? input.officeStyle : defaultOfficeStyle(input.layout ?? undefined);
}
