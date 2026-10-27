// The boss map: the project as a dungeon. Each node is a monster in a room
// laid out like the board (dependency depth down, slots across), corridors
// follow the dependencies, boolean gates are doors, live autopilots are a
// party on the move, and the boss's HP is the unfinished work.

import { useCallback, useEffect, useMemo, useRef, useState, type CSSProperties } from "react";

import { useBoardColumns } from "../../components/canvas/useBoardColumns";
import { useI18n } from "../../i18n";
import { openNodeRuns } from "../../runs/openRuns";
import { useRuns } from "../../runs/runStore";
import { reportError, useApp } from "../../store";
import type { Graph, Status } from "../../types";
import { useGameActions } from "../GameActions";
import { GameMenu, type GameMenuItem, type GameMenuState } from "../GameMenu";
import { clampZoom, ZoomControl } from "../Zoom";
import { doorLabel, monsterLabel, partyLabel, type TitleOf, type Translate } from "../labels";
import { isoBounds, isoProject, type GridPoint } from "../layout";
import type { Door, Dungeon as DungeonModel, Monster, Party } from "../world";
import { PackArt } from "../packs";
import { BossSvg, DUNGEON_TILE, Hero, KENNEY, KenneyImg, MonsterSvg } from "./DungeonArt";
import { layoutDungeon } from "./dungeonLayout";

const PAD_X = 48;
const PAD_TOP = 150;
const PAD_BOTTOM = 56;
/** How long a merged PR's "+XP" stays over its monster. */
const XP_MS = 2400;
/** Merges seen this soon after the map opened came with the first load. */
export const ARM_MS = 3000;
/** The most a first fit enlarges the map. */
const FIT_MAX = 1.6;

/** A non-colour cue per monster state, besides the art itself (a lively
 * monster, a chained grey one, an opened chest). */
const STATE_GLYPH: Record<Monster["state"], string> = {
  attackable: "",
  locked: "🔒",
  defeated: "✓",
};

export interface MonsterActionContext {
  t: Translate;
  titleOf: TitleOf;
  /** Whether agents can be dispatched here (the workstation is shown). */
  canDispatch: boolean;
}

export interface MonsterAction {
  id: string;
  label: string;
  run: () => void;
}

/** What can be done about a monster, for its menu and its list entry. */
export function monsterActions(monster: Monster, ctx: MonsterActionContext): MonsterAction[] {
  const { t, titleOf } = ctx;
  const actions: MonsterAction[] = [
    {
      id: "node",
      label: t("game.action.node", { title: monster.title || monster.nodeId }),
      run: () => void useApp.getState().openTab(monster.nodeId).catch(reportError),
    },
  ];
  if (monster.engaged || monster.merged) {
    actions.push({ id: "runs", label: t("game.action.runs"), run: () => openNodeRuns(monster.nodeId) });
  }
  if (monster.state === "attackable") {
    if (ctx.canDispatch) {
      // The dispatch dialog asks before anything runs: an "attack" costs
      // tokens, so it is never a single click.
      actions.push({
        id: "dispatch",
        label: t("game.action.dispatch"),
        run: () => useRuns.getState().openDispatch(monster.nodeId),
      });
    }
    if (monster.status === "ready" || monster.status === "locked") {
      actions.push({
        id: "start",
        label: t("game.action.start"),
        run: () =>
          void useApp
            .getState()
            .setLifecycleStatus(monster.nodeId, "started")
            .catch(reportError),
      });
    }
  }
  if (monster.state === "locked") {
    for (const id of monster.blockers) {
      actions.push({
        id: `blocker-${id}`,
        label: t("game.action.blocker", { title: titleOf(id) || id }),
        run: () => void useApp.getState().openTab(id).catch(reportError),
      });
    }
  }
  return actions;
}

function toMenuItems(actions: MonsterAction[]): GameMenuItem[] {
  return actions.map((action) => ({ id: action.id, label: action.label, onSelect: action.run }));
}

export function Dungeon({
  dungeon,
  graph,
  statuses,
  titleOf,
  canDispatch,
  reducedMotion,
}: {
  dungeon: DungeonModel;
  graph: Graph | null;
  statuses: Record<string, Status>;
  titleOf: TitleOf;
  canDispatch: boolean;
  reducedMotion: boolean;
}) {
  const { t } = useI18n();
  const { dispatch } = useGameActions();
  const [menu, setMenu] = useState<GameMenuState | null>(null);
  const { colOf } = useBoardColumns({
    graph,
    statuses,
    timelineSort: "manual",
    selectedNodeId: null,
    timelineCellWidth: 220,
    timelineCellHeight: 104,
  });

  const ids = useMemo(() => dungeon.monsters.map((monster) => monster.nodeId), [dungeon.monsters]);
  const layout = useMemo(
    () =>
      layoutDungeon(
        ids,
        colOf,
        (graph?.edges ?? [])
          .filter((edge) => edge.relation !== "deprecated")
          .map((edge) => ({ from: edge.from, to: edge.to })),
      ),
    [ids, colOf, graph?.edges],
  );

  const bounds = isoBounds(layout.extent, DUNGEON_TILE);
  const width = Math.ceil(bounds.width + PAD_X * 2);
  const height = Math.ceil(bounds.height + PAD_TOP + PAD_BOTTOM);
  const top = useCallback(
    (p: GridPoint) => {
      const q = isoProject(p.col, p.row, DUNGEON_TILE);
      return { x: q.x - bounds.minX + PAD_X, y: q.y - bounds.minY + PAD_TOP };
    },
    [bounds.minX, bounds.minY],
  );
  const z = (p: GridPoint, bump = 0) => 100 + Math.round((p.col + p.row) * 10) + bump;

  // Zoom: the map fits the pane once (larger than 1:1 when there is room,
  // up to FIT_MAX), then the person decides.
  const viewport = useRef<HTMLDivElement | null>(null);
  const [zoom, setZoom] = useState(1);
  // The map is refitted as it grows (the graph and the runs arrive after the
  // first render) until the person picks a zoom themselves.
  const zoomedByHand = useRef(false);
  const zoomByHand = useCallback((value: number) => {
    zoomedByHand.current = true;
    setZoom(value);
  }, []);
  useEffect(() => {
    if (zoomedByHand.current || !viewport.current || ids.length === 0) return;
    const { clientWidth, clientHeight } = viewport.current;
    if (clientWidth > 0 && clientHeight > 0) {
      setZoom(clampZoom(Math.min(FIT_MAX, clientWidth / width, clientHeight / height)));
    }
  }, [width, height, ids.length]);

  // A PR that merged while the map is open: "+XP" over its monster. Merges
  // that arrive with the first load (within ARM_MS of opening) are history.
  const seenMerged = useRef<Set<string>>(new Set());
  const openedAt = useRef(Date.now());
  const [fresh, setFresh] = useState<string[]>([]);
  const [announce, setAnnounce] = useState("");
  const xpTimers = useRef<ReturnType<typeof setTimeout>[]>([]);
  useEffect(() => {
    const added = dungeon.mergedNodeIds.filter((id) => !seenMerged.current.has(id));
    seenMerged.current = new Set(dungeon.mergedNodeIds);
    if (!added.length || Date.now() - openedAt.current < ARM_MS) return;
    setFresh((list) => [...list, ...added]);
    setAnnounce(t("game.xp.announce", { names: added.map((id) => titleOf(id) || id).join(", ") }));
    // Not cleared when the world rebuilds (every push does): only on unmount.
    xpTimers.current.push(
      setTimeout(() => setFresh((list) => list.filter((id) => !added.includes(id))), XP_MS),
    );
  }, [dungeon.mergedNodeIds, t, titleOf]);
  useEffect(() => () => xpTimers.current.forEach(clearTimeout), []);

  const actionContext = useMemo(() => ({ t, titleOf, canDispatch }), [t, titleOf, canDispatch]);
  const openMonster = (monster: Monster, anchor: HTMLElement) =>
    setMenu({
      anchor,
      title: monster.title || monster.nodeId,
      detail: monsterLabel(t, monster, titleOf),
      items: toMenuItems(monsterActions(monster, actionContext)),
    });
  const openBoss = (anchor: HTMLElement) => {
    const attackable = dungeon.monsters.filter((monster) => monster.state === "attackable");
    setMenu({
      anchor,
      title: t("game.boss.title", { name: dungeon.boss.name }),
      detail: attackable.length ? t("game.boss.targets") : t("game.boss.noTargets"),
      items: attackable.slice(0, 8).map((monster) => ({
        id: monster.nodeId,
        label: t("game.action.node", { title: monster.title || monster.nodeId }),
        onSelect: () => void useApp.getState().openTab(monster.nodeId).catch(reportError),
      })),
    });
  };

  const boss = dungeon.boss;
  const bossTop = top({ col: layout.boss.col + 1, row: layout.boss.row + 1 });
  const freshSet = new Set(fresh);

  return (
    <div className="game-dungeon">
      <div className="game-dungeon-hud">
        <span className="game-hud-boss">{t("game.boss.title", { name: boss.name })}</span>
        <span
          className="game-meter hp"
          role="meter"
          aria-label={t("game.boss.hp")}
          aria-valuemin={0}
          aria-valuemax={boss.maxHp}
          aria-valuenow={boss.hp}
          aria-valuetext={t("game.boss.hpText", { hp: boss.hp, max: boss.maxHp })}
        >
          <span className="game-meter-fill" style={{ width: `${boss.ratio * 100}%` }} />
        </span>
        <span className="game-hud-value">{t("game.boss.hpText", { hp: boss.hp, max: boss.maxHp })}</span>
        <span className="game-hud-value">{t("game.xp.total", { xp: dungeon.xp })}</span>
        {dungeon.parties.length > 0 && (
          <span className="game-hud-value">{t("game.party.count", { count: dungeon.parties.length })}</span>
        )}
        <ZoomControl zoom={zoom} onZoom={zoomByHand} />
      </div>
      {dungeon.monsters.length === 0 && (
        <p className="game-empty-hint game-dungeon-empty">{t("game.dungeon.empty")}</p>
      )}
      <div className="game-viewport" ref={viewport}>
        <div className="game-scene-sizer" style={{ width: width * zoom, height: height * zoom }}>
          <div
            className="game-scene"
            style={{ width, height, transform: zoom === 1 ? undefined : `scale(${zoom})` }}
          >
            <div className="game-floors" aria-hidden="true">
              {layout.corridors.map((p) => {
                const q = top(p);
                return <KenneyImg key={`c${p.col}:${p.row}`} sprite={KENNEY.floorDirt} x={q.x} y={q.y} className="corridor" />;
              })}
              {dungeon.monsters.map((monster) => {
                const p = layout.rooms.get(monster.nodeId);
                if (!p) return null;
                const q = top(p);
                return (
                  <KenneyImg
                    key={`f${monster.nodeId}`}
                    sprite={monster.state === "locked" ? KENNEY.floorUneven : KENNEY.floorStone}
                    x={q.x}
                    y={q.y}
                    className={`room ${monster.state}${monster.critical ? " critical" : ""}${monster.faded ? " faded" : ""}`}
                  />
                );
              })}
              {[0, 1].flatMap((dc) =>
                [0, 1].map((dr) => {
                  const q = top({ col: layout.boss.col + dc, row: layout.boss.row + dr });
                  return <KenneyImg key={`b${dc}${dr}`} sprite={KENNEY.floorStone} x={q.x} y={q.y} className="arena" />;
                }),
              )}
            </div>

            {/* Arena dressing: columns at the back corners, a barrel. */}
            {(() => {
              const left = top({ col: layout.boss.col - 0.2, row: layout.boss.row + 1.6 });
              const right = top({ col: layout.boss.col + 1.6, row: layout.boss.row - 0.2 });
              const barrel = top({ col: layout.boss.col + 1.9, row: layout.boss.row + 1.1 });
              return (
                <>
                  <KenneyImg sprite={KENNEY.column} x={left.x} y={left.y} style={{ zIndex: z(layout.boss, 5) }} />
                  <KenneyImg sprite={KENNEY.column} x={right.x} y={right.y} style={{ zIndex: z(layout.boss, 5) }} />
                  <KenneyImg sprite={KENNEY.barrel} x={barrel.x} y={barrel.y} style={{ zIndex: z(layout.boss, 30) }} />
                </>
              );
            })()}

            {dungeon.monsters.map((monster) => {
              const p = layout.rooms.get(monster.nodeId);
              if (!p) return null;
              const q = top(p);
              return (
                <MonsterSprite
                  key={monster.nodeId}
                  monster={monster}
                  label={monsterLabel(t, monster, titleOf)}
                  style={{ left: q.x - 40, top: q.y - 52, zIndex: z(p, 1) }}
                  fresh={freshSet.has(monster.nodeId) && !reducedMotion}
                  freshStatic={freshSet.has(monster.nodeId) && reducedMotion}
                  onOpen={(anchor) => openMonster(monster, anchor)}
                />
              );
            })}

            {dungeon.doors.map((door, i) => {
              const out = layout.rooms.get(door.output);
              if (!out) return null;
              // In the corridor tile before its room; a second gate on the
              // same room stands beside the first.
              const before = dungeon.doors.slice(0, i).filter((other) => other.output === door.output).length;
              const p = { col: out.col + before, row: out.row - 1 };
              const q = top(p);
              return (
                <DoorSprite
                  key={door.gateId}
                  door={door}
                  label={doorLabel(t, door, titleOf)}
                  style={{ left: q.x - KENNEY.gateClosed.dx, top: q.y - KENNEY.gateClosed.dy, zIndex: z(p, 2) }}
                  onOpen={() => dispatch(door.target)}
                />
              );
            })}

            {dungeon.parties.map((party, i) => {
              const at = party.positions.map((id) => layout.rooms.get(id)).find(Boolean) ?? layout.rooms.get(party.goalNodeId);
              if (!at) return null;
              const p = { col: at.col + 0.35 - i * 0.2, row: at.row - 0.15 + i * 0.2 };
              const q = top(p);
              return (
                <PartySprite
                  key={party.autopilotId}
                  party={party}
                  label={partyLabel(t, party, titleOf)}
                  style={{ left: q.x - 16, top: q.y - 28, zIndex: z(at, 6) }}
                  running={party.status === "running" && !reducedMotion}
                  onOpen={() => dispatch(party.target)}
                />
              );
            })}

            <button
              type="button"
              className={`game-boss${boss.defeated ? " defeated" : ""}${fresh.length && !reducedMotion ? " hit" : ""}`}
              style={{ left: bossTop.x - 70, top: bossTop.y - 150, zIndex: z(layout.boss, 20) }}
              aria-label={t("game.boss.label", { name: boss.name, hp: boss.hp, max: boss.maxHp })}
              onClick={(event) => openBoss(event.currentTarget)}
            >
              <span className="game-boss-bar" aria-hidden="true">
                <span style={{ width: `${boss.ratio * 100}%` }} />
              </span>
              <PackArt
                keys={boss.defeated ? ["boss.defeated", "boss"] : ["boss"]}
                className="game-boss-art"
                width={140}
                height={130}
              >
                <BossSvg defeated={boss.defeated} />
              </PackArt>
              <span className="game-boss-name" aria-hidden="true">
                {boss.defeated ? t("game.boss.defeated") : t("game.boss.hpText", { hp: boss.hp, max: boss.maxHp })}
              </span>
            </button>
          </div>
        </div>
      </div>
      <div className="visually-hidden" aria-live="polite">
        {announce}
      </div>
      {menu && <GameMenu menu={menu} onClose={() => setMenu(null)} />}
    </div>
  );
}

function MonsterSprite({
  monster,
  label,
  style,
  fresh,
  freshStatic,
  onOpen,
}: {
  monster: Monster;
  label: string;
  style: CSSProperties;
  fresh: boolean;
  freshStatic: boolean;
  onOpen: (anchor: HTMLElement) => void;
}) {
  return (
    <button
      type="button"
      className={`game-monster-tile state-${monster.state}${monster.engaged ? " engaged" : ""}${
        monster.faded ? " faded" : ""
      }${monster.critical ? " critical" : ""}${fresh ? " fresh" : ""}`}
      style={style}
      aria-label={label}
      data-node={monster.nodeId}
      data-state={monster.state}
      onClick={(event) => onOpen(event.currentTarget)}
    >
      {monster.state === "defeated" ? (
        // A settled node is loot, not a monster: a pack's own "monster"
        // picture does not stand in for it.
        <PackArt keys={["monster.defeated"]} className="game-monster" width={48} height={48}>
          <KenneyImg sprite={KENNEY.chestOpen} x={40} y={52} className="loot" />
        </PackArt>
      ) : (
        <PackArt keys={[`monster.${monster.state}`, "monster"]} className="game-monster" width={48} height={48}>
          <MonsterSvg priority={monster.priority} />
        </PackArt>
      )}
      {monster.merged && monster.state !== "defeated" && (
        <KenneyImg sprite={KENNEY.chestClosed} x={62} y={66} className="loot" />
      )}
      {STATE_GLYPH[monster.state] && (
        <span className="game-state-glyph" aria-hidden="true">
          {STATE_GLYPH[monster.state]}
        </span>
      )}
      {monster.engaged && (
        <span className="game-badge fight" aria-hidden="true">
          ✦
        </span>
      )}
      {monster.entry && (
        <span className="game-badge entry" aria-hidden="true">
          ▶
        </span>
      )}
      {(fresh || freshStatic) && (
        <span className="game-xp" aria-hidden="true">
          +XP
        </span>
      )}
      <span className="game-nameplate" aria-hidden="true">
        <span className="game-nameplate-title">{monster.title || monster.nodeId}</span>
      </span>
    </button>
  );
}

function DoorSprite({
  door,
  label,
  style,
  onOpen,
}: {
  door: Door;
  label: string;
  style: CSSProperties;
  onOpen: () => void;
}) {
  const sprite = door.open ? KENNEY.gateOpen : KENNEY.gateClosed;
  return (
    <button
      type="button"
      className={`game-door${door.open ? " open" : " shut"}`}
      style={{ ...style, width: sprite.w, height: sprite.h }}
      aria-label={label}
      data-gate={door.gateId}
      onClick={onOpen}
    >
      <KenneyImg sprite={sprite} x={sprite.dx} y={sprite.dy} />
      <span className="game-door-op" aria-hidden="true">
        {door.operator.toUpperCase()}
      </span>
    </button>
  );
}

function PartySprite({
  party,
  label,
  style,
  running,
  onOpen,
}: {
  party: Party;
  label: string;
  style: CSSProperties;
  running: boolean;
  onOpen: () => void;
}) {
  return (
    <button
      type="button"
      className={`game-party status-${party.status}${party.waitingHuman.length ? " waiting" : ""}`}
      style={style}
      aria-label={label}
      data-autopilot={party.autopilotId}
      onClick={onOpen}
    >
      <Hero running={running} />
      <span className="game-party-flag" aria-hidden="true">
        {party.done}/{party.total}
      </span>
      {party.waitingHuman.length > 0 && (
        <span className="game-badge waiting" aria-hidden="true">
          !
        </span>
      )}
    </button>
  );
}
