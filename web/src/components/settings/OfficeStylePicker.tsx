import { useRef, type KeyboardEvent } from "react";

import { defaultOfficeStyle, OFFICE_STYLES } from "../../game/officeStyle";
import { OfficeStylePreview } from "../../game/warroom/OfficeStylePreview";
import { useI18n } from "../../i18n";
import { useApp } from "../../store";
import type { OfficeStyle } from "../../types";
import { runSettingsCommand, type SettingsNotify } from "./notify";

type Choice = OfficeStyle | "auto";

/**
 * How the project's office looks in Station: one of the presets, or
 * automatic (the layout's default). Stored in graph.yaml (office_style) and
 * saved like the node types, so everyone in the project sees the same
 * office; the hub refuses the change from an agent. Used in Settings › This
 * project and from the office's door sign in Station.
 */
export function OfficeStylePicker({ notify, autoFocus = false }: { notify: SettingsNotify; autoFocus?: boolean }) {
  const { t } = useI18n();
  const graph = useApp((s) => s.graph);
  const updateWorkflowDefinition = useApp((s) => s.updateWorkflowDefinition);
  const group = useRef<HTMLDivElement | null>(null);
  const chosen = graph?.officeStyle;
  const current: Choice = chosen ?? "auto";
  const fallback = defaultOfficeStyle(graph?.type);
  const choices: Choice[] = ["auto", ...OFFICE_STYLES];

  const label = (choice: Choice) =>
    choice === "auto"
      ? t("office.style.auto", { style: t(`office.style.${fallback}`) })
      : t(`office.style.${choice}`);

  const pick = (choice: Choice) => {
    if (choice === current) return;
    void runSettingsCommand(
      notify,
      updateWorkflowDefinition({ type: "workflow.setOfficeStyle", style: choice === "auto" ? null : choice }),
    );
  };

  // A radio group: the arrows move the choice, Tab leaves the group.
  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    const step = event.key === "ArrowRight" || event.key === "ArrowDown" ? 1 : event.key === "ArrowLeft" || event.key === "ArrowUp" ? -1 : 0;
    if (!step) return;
    event.preventDefault();
    const next = choices[(choices.indexOf(current) + step + choices.length) % choices.length];
    pick(next);
    const buttons = group.current?.querySelectorAll<HTMLButtonElement>('[role="radio"]');
    buttons?.[choices.indexOf(next)]?.focus();
  };

  return (
    <section className="settings-section office-style-picker">
      <p className="settings-hint">{t("settings.officeStyleIntro")}</p>
      <div
        ref={group}
        className="office-style-options"
        role="radiogroup"
        aria-label={t("settings.officeStyle")}
        onKeyDown={onKeyDown}
      >
        {choices.map((choice) => {
          const on = choice === current;
          return (
            <button
              key={choice}
              type="button"
              role="radio"
              aria-checked={on}
              tabIndex={on ? 0 : -1}
              autoFocus={autoFocus && on}
              className={`office-style-option${on ? " is-on" : ""}`}
              onClick={() => pick(choice)}
            >
              <OfficeStylePreview style={choice === "auto" ? fallback : choice} />
              <span className="office-style-name">{label(choice)}</span>
            </button>
          );
        })}
      </div>
    </section>
  );
}
