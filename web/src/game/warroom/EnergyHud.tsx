// The day's tokens over the building: a meter against the cap when there is
// one, and the count.

import { useI18n } from "../../i18n";
import { energyLabel } from "../labels";
import type { Building } from "../world";

export function EnergyHud({ energy }: { energy: NonNullable<Building["energy"]> }) {
  const { t } = useI18n();
  return (
    <div className="px-hud">
      <span>{t("game.energy.today")}</span>
      {energy.cap ? (
        <span
          className="px-meter is-wide"
          role="meter"
          aria-label={t("game.energy.today")}
          aria-valuemin={0}
          aria-valuemax={energy.cap}
          aria-valuenow={Math.min(energy.used, energy.cap)}
          aria-valuetext={energyLabel(t, energy.used, energy.cap)}
        >
          <span
            className={energy.used >= energy.cap ? "is-full" : undefined}
            style={{ width: `${Math.min(100, (energy.used / energy.cap) * 100)}%` }}
          />
        </span>
      ) : null}
      <span className="px-hud-value">{energyLabel(t, energy.used, energy.cap)}</span>
    </div>
  );
}
