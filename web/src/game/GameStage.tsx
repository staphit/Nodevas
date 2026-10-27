// Station's live view: the war room or the boss map. Loaded on demand by
// Station (React.lazy), so its code, styles and sprites only reach a browser
// that opens one.

import { useCallback, useEffect, useMemo, useRef, useState, type CSSProperties } from "react";

import "./game.css";

import { useI18n } from "../i18n";
import { useWorkstationVisible } from "../runners/workstation";
import { useApp } from "../store";
import { Dungeon } from "./dungeon/Dungeon";
import { GameActionsProvider } from "./GameActions";
import { GameList } from "./GameList";
import { agentWho } from "./labels";
import type { ViewPack } from "../api";
import { PackContext } from "./packs";
import { packName, usePacks } from "./packStore";
import {
  setProjectMode,
  setStageSkin,
  setStationPack,
  STAGE_SKIN_LABEL,
  useStageSkinChoices,
  type StationView,
} from "./stageSkin";
import { usePageHidden, useReducedMotion } from "./useMotion";
import { useWorld } from "./useWorld";
import { WarRoom } from "./warroom/WarRoom";
import type { World } from "./world";

export interface GameStageProps {
  skin: StationView;
  /** The view pack drawing it (docs/view-packs.md); null: the built-in art. */
  pack?: ViewPack | null;
}

export default function GameStage({ skin, pack = null }: GameStageProps) {
  const { language, t } = useI18n();
  const packs = usePacks((s) => s.packs);
  const reduced = useReducedMotion();
  const hidden = usePageHidden();
  const workstation = useWorkstationVisible();
  const [listView, setListView] = useState(false);
  const choices = useStageSkinChoices();
  const title = pack ? packName(pack, language) : t(STAGE_SKIN_LABEL[skin]);
  // A pack's palette, as variables game.css reads; only colours get here.
  const palette = pack?.palette
    ? ({
        "--pack-ground": pack.palette.ground,
        "--pack-text": pack.palette.text,
      } as CSSProperties)
    : undefined;

  return (
    <GameActionsProvider>
      <section
        className={`lane-wrap graph game-stage station-live skin-${skin}${pack ? " has-pack" : ""}`}
        aria-label={title}
        data-pack={pack?.id}
        style={palette}
        data-motion={reduced ? "reduced" : "full"}
        data-paused={hidden ? "true" : undefined}
      >
        <div className="board-title game-head">
          <div className="board-title-controls">
            <div className="game-skin-switch" role="group" aria-label={t("game.skin.label")}>
              {choices.map((choice) => (
                <button
                  key={choice}
                  type="button"
                  className={!pack && choice === skin ? "active" : ""}
                  aria-pressed={!pack && choice === skin}
                  onClick={() => setStageSkin(choice)}
                >
                  {t(STAGE_SKIN_LABEL[choice])}
                </button>
              ))}
              {packs.map((item) => (
                <button
                  key={`pack-${item.id}`}
                  type="button"
                  className={pack?.id === item.id ? "active" : ""}
                  aria-pressed={pack?.id === item.id}
                  onClick={() => setStationPack(item)}
                  title={t("packs.switchTitle")}
                >
                  {packName(item, language)}
                </button>
              ))}
            </div>
            <button
              type="button"
              className={`board-tool-button${listView ? " active" : ""}`}
              aria-pressed={listView}
              onClick={() => setListView((value) => !value)}
            >
              {t("game.listView")}
            </button>
            <span className="station-live-hint">{t("station.live.hint")}</span>
          </div>
        </div>
        <PackContext.Provider value={pack}>
          <div className="game-board">
            {skin === "warroom" && !workstation ? (
              <WarRoomOff />
            ) : (
              <GameBody skin={skin} listView={listView} reduced={reduced} canDispatch={workstation} />
            )}
          </div>
        </PackContext.Provider>
      </section>
    </GameActionsProvider>
  );
}

/** The war room shows the agent workstation; with it hidden there is
 * nothing to draw, so say how to bring it back. */
function WarRoomOff() {
  const { t } = useI18n();
  return (
    <div className="game-empty" role="status">
      <p>{t("game.warroom.off")}</p>
      <p className="game-empty-hint">{t("game.warroom.offHint")}</p>
      <div className="game-empty-actions">
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

function GameBody({
  skin,
  listView,
  reduced,
  canDispatch,
}: {
  skin: "warroom" | "dungeon";
  listView: boolean;
  reduced: boolean;
  canDispatch: boolean;
}) {
  const world = useWorld();
  const graph = useApp((s) => s.graph);
  const statuses = useApp((s) => s.statuses);
  const titles = useMemo(() => new Map((graph?.nodes ?? []).map((node) => [node.id, node.title ?? ""])), [graph]);
  const titleOf = useCallback((id: string) => titles.get(id) ?? "", [titles]);
  const announcement = useOfficeAnnouncements(world, skin === "warroom");

  return (
    <>
      {listView ? (
        <GameList skin={skin} world={world} titleOf={titleOf} canDispatch={canDispatch} />
      ) : skin === "warroom" ? (
        <WarRoom office={world.office} titleOf={titleOf} />
      ) : (
        <Dungeon
          dungeon={world.dungeon}
          graph={graph}
          statuses={statuses}
          titleOf={titleOf}
          canDispatch={canDispatch}
          reducedMotion={reduced}
        />
      )}
      <div className="visually-hidden" aria-live="polite">
        {announcement}
      </div>
    </>
  );
}

/** Says a raised hand or a held message once, when it appears: heartbeats
 * rebuild the world every few seconds and must not repeat it. */
function useOfficeAnnouncements(world: World, active: boolean): string {
  const { t } = useI18n();
  const seen = useRef<Set<string> | null>(null);
  const [text, setText] = useState("");
  useEffect(() => {
    if (!active) return;
    const current = new Set<string>();
    const fresh: string[] = [];
    for (const actor of world.office.actors) {
      const who = agentWho(actor.agent, actor.model);
      if (actor.ask) {
        current.add(`ask:${actor.ask.id}`);
        if (seen.current && !seen.current.has(`ask:${actor.ask.id}`)) fresh.push(t("game.announce.ask", { who }));
      }
      for (const message of actor.heldMail) {
        current.add(`held:${message.id}`);
        if (seen.current && !seen.current.has(`held:${message.id}`)) fresh.push(t("game.announce.held", { who }));
      }
    }
    seen.current = current;
    if (fresh.length) setText(fresh.join(" "));
  }, [world, active, t]);
  return text;
}
