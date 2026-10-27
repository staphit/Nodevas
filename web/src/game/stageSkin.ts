// A project's two faces and Station's game views. Info is the board as it
// always was; Station is where the project's agents work together, drawn as
// the war room or the boss map. Small on purpose, because the top bar, the
// graph header and the command palette are in the main bundle and import it;
// everything that draws a game view lives behind Station's lazy import of
// GameStage.

import { useCallback } from "react";

import type { OverflowMenuItem } from "../components/menus/OverflowMenu";
import { useI18n } from "../i18n";
import type { ViewPack } from "../api";
import { useStation } from "../station/stationStore";
import { useApp, usePreference } from "../store";
import { usePacks } from "./packStore";

/** The stored preference: "cards" is what every browser had before Station,
 * and in Station it means the war room. */
export type StageSkin = "cards" | "warroom" | "dungeon";

/** The game views Station draws. */
export type StationView = "warroom" | "dungeon";

export const STATION_VIEWS: readonly StationView[] = ["warroom", "dungeon"];

export type ProjectMode = "info" | "station" | "settings";

/** i18n key of each skin's name. */
export const STAGE_SKIN_LABEL: Record<StageSkin, string> = {
  cards: "game.skin.cards",
  warroom: "game.skin.warroom",
  dungeon: "game.skin.dungeon",
};

/** The view Station shows for a stored skin. */
export function stationView(skin: StageSkin): StationView {
  return skin === "dungeon" ? "dungeon" : "warroom";
}

export function useStationView(): StationView {
  return stationView(usePreference("stageSkin"));
}

/**
 * Shows Info or Station. Station is the agent workstation, so entering it
 * turns the workstation on: the "hidden until an agent appears" rule is for
 * Info, where a board used as a plain planner should stay plain.
 */
export function setProjectMode(mode: ProjectMode): void {
  const { updateUIPreference } = useApp.getState();
  updateUIPreference("projectMode", mode);
  if (mode === "station") updateUIPreference("agentWorkstation", true);
}

/** Opens Station on one of its built-in views (its "In progress" card
 * draws it instead of its list). */
export function setStageSkin(view: StationView): void {
  const { updateUIPreference } = useApp.getState();
  updateUIPreference("stageSkin", view);
  updateUIPreference("stationPack", "");
  useStation.getState().setLiveView("game");
  setProjectMode("station");
}

/** Opens Station drawn with a view pack (docs/view-packs.md). */
export function setStationPack(pack: Pick<ViewPack, "id" | "layout">): void {
  const { updateUIPreference } = useApp.getState();
  updateUIPreference("stageSkin", pack.layout === "rooms" ? "warroom" : "dungeon");
  updateUIPreference("stationPack", pack.id);
  useStation.getState().setLiveView("game");
  setProjectMode("station");
}

/** The view pack Station draws with, when the chosen one is installed. */
export function useStationPack(): ViewPack | null {
  const id = usePreference("stationPack");
  const packs = usePacks((s) => s.packs);
  return (id && packs.find((pack) => pack.id === id)) || null;
}

/** The views Station's switch offers. */
export function useStageSkinChoices(): StationView[] {
  return [...STATION_VIEWS];
}

/** Station's views as `⋯` menu items, for the graph header in Info: each
 * opens Station on that view. */
export function useStageSkinItems(): OverflowMenuItem[] {
  const { t } = useI18n();
  const pick = useCallback((view: StationView) => setStageSkin(view), []);
  return STATION_VIEWS.map((view, index) => ({
    id: `stage-skin-${view}`,
    label: t("game.menu.openInStation", { name: t(STAGE_SKIN_LABEL[view]) }),
    separatorBefore: index === 0,
    onSelect: () => pick(view),
  }));
}
