// Another project's office, as a glance through its door: the door sign,
// a strip of its style's wall, up to three small desks with who is at them
// (busy, asleep, a hand up), and badges for what is under way and what waits
// on the viewer there. The whole card is one button; its menu goes there
// first, then to its runs and what waits there. As a door sign alone (an idle
// office, or one past the busy few), a click goes straight there.

import { memo, useMemo } from "react";

import { useI18n } from "../../i18n";
import { StationMenu } from "../../station/StationMenu";
import { useGameActions } from "../GameActions";
import { agentColorKey, compactLabel, styleName, type LabelOf } from "../labels";
import { PixelSprite } from "../pixel/PixelSprite";
import type { CompactOffice as CompactOfficeModel, MiniDesk } from "../world";
import { miniDesk } from "./deskArt";
import { STYLE_GLYPH } from "./OfficeRoom";
import { menuItem } from "./PixelDesk";

const MiniDeskArt = memo(function MiniDeskArt({ desk }: { desk: MiniDesk }) {
  const parts = useMemo(() => miniDesk(desk.pose), [desk.pose]);
  return (
    <span className={`px-mini agent--${agentColorKey(desk.agent)} pose-${desk.pose}`}>
      <PixelSprite parts={parts} width={16} height={16} />
    </span>
  );
});

function Badges({ office }: { office: CompactOfficeModel }) {
  const { t } = useI18n();
  const { counts } = office;
  return (
    <span className="px-compact-badges" aria-hidden="true">
      {counts.running > 0 && <span className="px-badge-chip">▶ {counts.running}</span>}
      {counts.waiting > 0 && <span className="px-badge-chip is-attention">✋ {counts.waiting}</span>}
      {counts.offered > 0 && <span className="px-badge-chip">🚪 {counts.offered}</span>}
      {counts.sleeping > 0 && <span className="px-badge-chip">zZ {counts.sleeping}</span>}
      {counts.othersActive > 0 && (
        <span className="px-badge-chip is-muted">{t("game.compact.othersBadge", { count: counts.othersActive })}</span>
      )}
    </span>
  );
}

function Plate({ office }: { office: CompactOfficeModel }) {
  const { t } = useI18n();
  return (
    <span className="px-plate">
      <span className="px-plate-glyph" aria-hidden="true" title={styleName(t, office.style)}>
        {STYLE_GLYPH[office.style]}
      </span>
      <span className="px-plate-name">{office.label}</span>
    </span>
  );
}

function sameOffice(a: { office: CompactOfficeModel; labelOf: LabelOf }, b: { office: CompactOfficeModel; labelOf: LabelOf }) {
  const key = (office: CompactOfficeModel) =>
    JSON.stringify([office.label, office.style, office.counts, office.more, office.desks, office.actions]);
  return a.labelOf === b.labelOf && key(a.office) === key(b.office);
}

/** A busy office in the row of others. */
export const CompactOffice = memo(function CompactOffice({
  office,
  labelOf,
}: {
  office: CompactOfficeModel;
  labelOf: LabelOf;
}) {
  const { t } = useI18n();
  const { dispatch } = useGameActions();
  const label = compactLabel(t, office);
  return (
    <StationMenu
      items={office.actions.map(menuItem(t, dispatch, () => "", labelOf))}
      label={label}
      heading={{ title: office.label, detail: label }}
      className={`px-compact px-style--${office.style}`}
      triggerClassName="px-compact-button"
      trigger={
        <>
          <Plate office={office} />
          <span className="px-compact-wall" aria-hidden="true" />
          <span className="px-compact-floor" aria-hidden="true">
            {office.desks.map((desk, i) => (
              <MiniDeskArt key={i} desk={desk} />
            ))}
            {office.more > 0 && <span className="px-compact-more">+{office.more}</span>}
          </span>
          <Badges office={office} />
        </>
      }
    />
  );
}, sameOffice);

/** A door sign alone: an idle office, or a busy one past the few drawn. A
 * click goes there. */
export const DoorChip = memo(function DoorChip({ office }: { office: CompactOfficeModel; labelOf: LabelOf }) {
  const { t } = useI18n();
  const { dispatch } = useGameActions();
  const label = compactLabel(t, office);
  return (
    <button
      type="button"
      className={`px-door-chip px-style--${office.style}${office.busy ? " is-busy" : ""}`}
      aria-label={label}
      title={label}
      onClick={() => dispatch(office.target)}
    >
      <Plate office={office} />
      {office.busy && <Badges office={office} />}
    </button>
  );
}, sameOffice);
