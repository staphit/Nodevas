// The war room, in pixel art: the workspace as a building. The current
// project's office first and in full (its work area, its meeting room, its
// entrance), the other projects' offices as a glance through their doors,
// then the ground floor: a lounge every project's idle agents share and a
// server room with a rack per runner. The viewer's global agents (outside any
// project) have a full office of their own under the current one. Every agent, item, office and rack is
// a real button; an agent's menu holds what can be done about it (a run's own
// controls, through station/runMenuItems). Station mounts it lazily
// (StationWarRoom), so its code and styles only reach a browser that opens
// Station. A text list of the same building is there for screen readers.
//
// The drawing is inline SVG at whole art pixels (pixel/PixelSprite), coloured
// by CSS tokens, so a theme switch costs nothing; animation is CSS steps()
// over a few frames, off under reduced motion and in a background tab. A
// heartbeat only redraws a desk whose agent changed (PixelDesk is memoised).
//
// With a theme pack on, the same building is told as a story instead
// (StoryRoom): places to go to, the pack's pictures of who is there, and a
// dialogue box. The text list and the announcements are the same either way.

import { useEffect, useMemo, useRef, useState, type CSSProperties } from "react";

import "./warroom.css";

import { useI18n } from "../../i18n";
import { useWorkstationVisible } from "../../runners/workstation";
import type { ControlRow } from "../../station/control";
import { setProjectMode } from "../../station/projectMode";
import { useApp } from "../../store";
import { agentWho, itemLabel, type LabelOf, type TitleOf } from "../labels";
import { usePageHidden, useReducedMotion } from "../useMotion";
import { GLOBAL_OFFICE, type Actor, type Building } from "../world";
import { artPixel, useArtPixel } from "./artPixel";
import { BuildingText } from "./BuildingText";
import { EnergyHud } from "./EnergyHud";
import { Lounge } from "./Lounge";
import { OfficeRoom } from "./OfficeRoom";
import { OtherOffices } from "./OtherOffices";
import { ServerRoom } from "./ServerRoom";
import { StoryRoom } from "./StoryRoom";
import { ThemeCastContext, useActiveThemeCast, useThemeCast } from "./themeCastContext";

export { artPixel };

export interface WarRoomProps {
  building: Building;
  titleOf: TitleOf;
  /** The run behind a character, with its autopilot: what its menu acts on. */
  rowOf: (runId: string) => ControlRow | undefined;
}

/** Project name → its label, from every office in the building; the
 * global agents' office by its own name. */
function useLabelOf(building: Building): LabelOf {
  const { t } = useI18n();
  return useMemo(() => {
    const labels = new Map<string, string>();
    if (building.current) labels.set(building.current.project, building.current.label);
    for (const office of [...building.others, ...building.doorSigns]) labels.set(office.project, office.label);
    labels.set(GLOBAL_OFFICE, t("globalAgents.office"));
    return (project: string) => labels.get(project) || project;
  }, [building.current, building.others, building.doorSigns, t]);
}

/** The building as Station shows it: still under reduced motion, paused in
 * a background tab, and a way back when the workstation is hidden. */
export default function WarRoomStage(props: WarRoomProps) {
  const reduced = useReducedMotion();
  const hidden = usePageHidden();
  const workstation = useWorkstationVisible();
  const announcement = useOfficeAnnouncements(props.building, workstation);
  const px = useArtPixel();
  const cast = useActiveThemeCast(reduced || hidden);
  return (
    <div
      className="px-office"
      data-motion={reduced ? "reduced" : "full"}
      data-paused={hidden ? "true" : undefined}
      style={{ ["--px" as string]: `${px}px` } as CSSProperties}
    >
      {workstation ? (
        <ThemeCastContext.Provider value={cast}>
          <WarRoom {...props} />
          <BuildingText building={props.building} />
        </ThemeCastContext.Provider>
      ) : (
        <WarRoomOff />
      )}
      <div className="visually-hidden" aria-live="polite">
        {announcement}
      </div>
    </div>
  );
}

/** The war room shows the agent workstation; with it hidden there is
 * nothing to draw, so say how to bring it back. */
function WarRoomOff() {
  const { t } = useI18n();
  return (
    <div className="px-empty" role="status">
      <p>{t("game.warroom.off")}</p>
      <p className="px-empty-hint">{t("game.warroom.offHint")}</p>
      <div className="px-empty-actions">
        <button
          type="button"
          className="board-tool-button active"
          onClick={() => useApp.getState().updateUIPreference("agentWorkstation", true)}
        >
          {t("workstation.enable")}
        </button>
        <button type="button" className="board-tool-button" onClick={() => setProjectMode("info")}>
          {t("mode.toInfo")}
        </button>
      </div>
    </div>
  );
}

/** Says a raised hand, a held message or something new on the meeting
 * table once, when it appears: heartbeats rebuild the world every few
 * seconds and must not repeat it. */
function useOfficeAnnouncements(building: Building, active: boolean): string {
  const { t } = useI18n();
  const seen = useRef<Set<string> | null>(null);
  const [text, setText] = useState("");
  useEffect(() => {
    if (!active) return;
    const current = new Set<string>();
    const fresh: string[] = [];
    const isNew = (key: string) => {
      current.add(key);
      return Boolean(seen.current && !seen.current.has(key));
    };
    for (const actor of building.actors) {
      const who = agentWho(actor.agent, actor.model);
      if (actor.ask && isNew(`ask:${actor.ask.id}`)) fresh.push(t("game.announce.ask", { who }));
      for (const message of actor.heldMail) {
        if (isNew(`held:${message.id}`)) fresh.push(t("game.announce.held", { who }));
      }
    }
    for (const item of building.current?.items ?? []) {
      if (isNew(`item:${item.id}`)) fresh.push(t("game.announce.item", { item: itemLabel(t, item) }));
    }
    seen.current = current;
    if (fresh.length) setText(fresh.join(" "));
  }, [building, active, t]);
  return text;
}

export function WarRoom({ building, titleOf, rowOf }: WarRoomProps) {
  const { t } = useI18n();
  const actorsById = useMemo(
    () => new Map<string, Actor>(building.actors.map((actor) => [actor.id, actor])),
    [building.actors],
  );
  const labelOf = useLabelOf(building);
  const cast = useThemeCast();

  if (!building.current && !building.global) {
    return (
      <div className="px-empty">
        <p>{t("game.warroom.noRooms")}</p>
        <p className="px-empty-hint">{t("game.warroom.noRoomsHint")}</p>
      </div>
    );
  }

  if (cast) {
    return (
      <StoryRoom
        building={building}
        office={building.current}
        actorsById={actorsById}
        titleOf={titleOf}
        rowOf={rowOf}
        labelOf={labelOf}
        cast={cast}
      />
    );
  }

  return (
    <div className="px-building">
      {building.energy && <EnergyHud energy={building.energy} />}
      {building.current && (
        <OfficeRoom office={building.current} actorsById={actorsById} titleOf={titleOf} rowOf={rowOf} />
      )}
      {building.global && (
        <OfficeRoom office={building.global} actorsById={actorsById} titleOf={titleOf} rowOf={rowOf} />
      )}
      <OtherOffices building={building} labelOf={labelOf} />
      <div className="px-ground">
        <Lounge building={building} actorsById={actorsById} titleOf={titleOf} rowOf={rowOf} labelOf={labelOf} />
        <ServerRoom racks={building.racks} />
      </div>
    </div>
  );
}
