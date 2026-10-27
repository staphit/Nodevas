// The server room: a rack per runner. Its lights are on while the runner is
// online, off when it is not; a light per slot (at most four) is lit for
// each run it holds, across every project. A rack is a button: the runners'
// settings.

import { memo, useMemo } from "react";

import { useI18n } from "../../i18n";
import { useGameActions } from "../GameActions";
import { rackLabel } from "../labels";
import { PixelSprite } from "../pixel/PixelSprite";
import { RACK } from "../pixel/sprites";
import type { Rack } from "../world";
import { rackScene } from "./deskArt";

/** Racks drawn before "+N". */
export const MAX_RACKS = 8;

const RackButton = memo(function RackButton({ rack }: { rack: Rack }) {
  const { t } = useI18n();
  const { dispatch } = useGameActions();
  const parts = useMemo(() => rackScene(rack.online, rack.busy, rack.slots), [rack.online, rack.busy, rack.slots]);
  const label = rackLabel(t, rack);
  return (
    <button
      type="button"
      className={`px-rack${rack.online === false ? " is-lights-off" : ""}`}
      aria-label={label}
      title={label}
      onClick={() => dispatch(rack.target)}
    >
      <span className="px-art">
        <PixelSprite parts={parts} width={RACK.w} height={RACK.h} />
      </span>
      <span className="px-rack-name">{rack.name || t("game.room.unnamed")}</span>
      <span className="px-rack-load">{rack.slots ? `${rack.busy}/${rack.slots}` : `▶ ${rack.busy}`}</span>
    </button>
  );
});

export function ServerRoom({ racks }: { racks: Rack[] }) {
  const { t } = useI18n();
  const { dispatch } = useGameActions();
  const shown = racks.slice(0, MAX_RACKS);
  const more = racks.length - shown.length;
  return (
    <section className="px-server-room" aria-label={t("game.area.serverRoom")}>
      <h3 className="px-room-title">{t("game.area.serverRoom")}</h3>
      <div className="px-racks">
        {shown.map((rack) => (
          <RackButton key={rack.runnerId} rack={rack} />
        ))}
        {more > 0 && (
          <button
            type="button"
            className="px-rack-more"
            aria-label={`${t("game.rack.more", { count: more })}. ${t("game.action.runners")}`}
            onClick={() => dispatch({ kind: "runners" })}
          >
            +{more}
          </button>
        )}
      </div>
      {racks.length === 0 && <p className="px-room-note">{t("game.serverRoom.none")}</p>}
    </section>
  );
}
