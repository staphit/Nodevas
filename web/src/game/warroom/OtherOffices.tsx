// The other projects' offices: the busy ones as a glance through their
// doors, busiest first, the rest as door signs in a wrapping row, so a
// workspace of many projects stays one screen. A click goes there.

import { useI18n } from "../../i18n";
import type { LabelOf } from "../labels";
import type { Building } from "../world";
import { CompactOffice, DoorChip } from "./CompactOffice";

export function OtherOffices({ building, labelOf }: { building: Building; labelOf: LabelOf }) {
  const { t } = useI18n();
  const { others, doorSigns } = building;
  const shown = (building.current ? 1 : 0) + others.length + doorSigns.length;
  const hidden = Math.max(0, building.total - shown);
  if (others.length === 0 && doorSigns.length === 0 && hidden === 0) return null;
  return (
    <div className="px-others">
      {others.length > 0 && (
        <ul className="px-compacts" aria-label={t("game.building.others")}>
          {others.map((office) => (
            <li key={office.project}>
              <CompactOffice office={office} labelOf={labelOf} />
            </li>
          ))}
        </ul>
      )}
      {doorSigns.length > 0 && (
        <ul className="px-doors" aria-label={t("game.building.doors")}>
          {doorSigns.map((office) => (
            <li key={office.project}>
              <DoorChip office={office} labelOf={labelOf} />
            </li>
          ))}
        </ul>
      )}
      {(building.truncated || hidden > 0) && (
        <p className="px-others-more">{t("game.building.more", { count: hidden })}</p>
      )}
    </div>
  );
}
