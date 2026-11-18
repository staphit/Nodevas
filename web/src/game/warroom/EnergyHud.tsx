// The day's tokens over the building: a meter against the cap when there is
// one, and the count. The label says "Today" and the count carries the unit,
// so "tokens" is said once. Simple (single-user edition) never says "token":
// it shows how much of today's limit is used, and nothing without a limit.

import { useI18n } from "../../i18n";
import { useSimpleMode } from "../../simple/simpleMode";
import { energyLabel } from "../labels";
import type { Building } from "../world";

export function EnergyHud({ energy }: { energy: NonNullable<Building["energy"]> }) {
  const { t } = useI18n();
  const simple = useSimpleMode();
  if (simple && !energy.cap) return null;
  const percent = energy.cap ? Math.min(100, Math.round((energy.used / energy.cap) * 100)) : 0;
  const value = simple ? t("simple.energy.used", { percent }) : energyLabel(t, energy.used, energy.cap);
  return (
    <div className="px-hud">
      <span>{t(simple ? "simple.energy.today" : "game.energy.todayLabel")}</span>
      {energy.cap ? (
        <span
          className="px-meter is-wide"
          role="meter"
          aria-label={t(simple ? "simple.energy.today" : "game.energy.today")}
          aria-valuemin={0}
          aria-valuemax={energy.cap}
          aria-valuenow={Math.min(energy.used, energy.cap)}
          aria-valuetext={value}
        >
          <span
            className={energy.used >= energy.cap ? "is-full" : undefined}
            style={{ width: `${Math.min(100, (energy.used / energy.cap) * 100)}%` }}
          />
        </span>
      ) : null}
      <span className="px-hud-value">{value}</span>
    </div>
  );
}
