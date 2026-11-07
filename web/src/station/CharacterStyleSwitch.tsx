// How the war room draws its characters for this viewer: pixel art or
// anime (game/characterStyle.ts). In the war room's head, beside its title:
// a view of the room, not a change to it, so it says it is the viewer's
// alone.

import { useId } from "react";

import { CHARACTER_STYLES, useCharacterStyle } from "../game/characterStyle";
import { useI18n } from "../i18n";

export function CharacterStyleSwitch() {
  const { t } = useI18n();
  const [style, setStyle] = useCharacterStyle();
  const hint = useId();
  const label = t("game.characterStyle.label");
  return (
    <div
      className="station-seg station-character-style"
      role="group"
      aria-label={label}
      aria-describedby={hint}
      title={t("game.characterStyle.hint")}
    >
      <span className="station-seg-label" aria-hidden="true">
        {label}
      </span>
      {CHARACTER_STYLES.map((value) => (
        <button key={value} type="button" aria-pressed={value === style} onClick={() => setStyle(value)}>
          {t(`game.characterStyle.${value}`)}
        </button>
      ))}
      <span id={hint} className="visually-hidden">
        {t("game.characterStyle.hint")}
      </span>
    </div>
  );
}
