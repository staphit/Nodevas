// DEV ONLY: a theme pack's cast in the war room, with no real agents. Served
// by the Vite dev server at /theme-preview.html; `npm run build` builds only
// index.html (vite.config.ts names no other input), so none of this ships.
//
// It loads the packs from /api/themes like the app does, puts the one asked
// for on show (in memory only: nothing is saved to preferences) with its skin,
// and draws the real StoryRoom (through WarRoom) over a made-up building with
// every character somewhere in every place.
//
//   ?pack=<id>    the pack (default: the first one listed)
//   ?state=<name> every figure in that scene (idle, write, hike, …)
//   ?lang=zh-TW   or en; default: the viewer's own setting
//
// Places are the room's own navigation: its side buttons, or the digits 1–6.

import { useEffect, useMemo, useState, type CSSProperties } from "react";

import type { AgentMessage } from "../api/agents";
import { usePageHidden, useReducedMotion } from "../game/useMotion";
import { useArtPixel } from "../game/warroom/artPixel";
import { hash, OUTINGS } from "../game/warroom/themeCast";
import { ThemeCastContext, useActiveThemeCast } from "../game/warroom/themeCastContext";
import { WarRoom } from "../game/warroom/WarRoom";
import type { Actor, Area, Building, LoungeSpot, Pose } from "../game/world";
import { useApp } from "../store";
import { useThemeSkin } from "../theme-pack/themeSkin";
import { useThemePacks } from "../theme-pack/themePackStore";

const PROJECT = "preview";

const NODES: Record<string, string> = {
  login: "Login form",
  schema: "DB schema",
  docs: "API docs",
  search: "Search index",
  deploy: "Deploy script",
  tests: "Test suite",
};
const titleOf = (id: string) => NODES[id] ?? "";

/** Who is who, by the pack's match rules (agent, else model). */
const WHO = {
  claude: { agent: "claude", model: "claude-opus-4" },
  gpt: { agent: "codex", model: "gpt-5" },
  deepseek: { agent: "opencode", model: "deepseek-v3" },
  gemini: { agent: "gemini", model: "gemini-2.5-pro" },
} as const;
type Who = keyof typeof WHO;

interface Spec {
  who: Who;
  pose: Pose;
  area: Area;
  node: string;
  role?: string;
  bubble?: string;
  /** For a usage limit: the outing it goes on (picked by its id's hash). */
  outing?: string;
}

/** An id whose hash sends a run on this outing (themeCast.sceneWanted). */
function idFor(base: string, outing: string): string {
  for (let n = 0; n < 1000; n++) {
    const id = `run:${base}-${n}`;
    if (OUTINGS[hash(id) % OUTINGS.length] === outing) return id;
  }
  return `run:${base}`;
}

function message(id: string, body: string): AgentMessage {
  return { id, project: PROJECT, from: "agent", senderActor: "agent", to: "all", body, createdAt: Date.now() };
}

function actor(key: string, spec: Spec): Actor {
  const id = spec.outing ? idFor(key, spec.outing) : `run:${key}`;
  const runId = id.slice("run:".length);
  const target = { kind: "run" as const, runId, nodeId: spec.node };
  return {
    id,
    kind: "run",
    ...WHO[spec.who],
    project: PROJECT,
    area: spec.area,
    runnerId: "rn1",
    runnerName: "mac-mini",
    runnerOnline: true,
    pose: spec.pose,
    nodeId: spec.node,
    nodeTitle: titleOf(spec.node),
    runId,
    ownerName: "preview",
    bubble: spec.bubble ? message(`m-${key}`, spec.bubble) : undefined,
    heldMail: [],
    role: spec.role,
    target,
    actions: [target],
  };
}

const SPECS: Record<string, Spec> = {
  // Work area: one per role scene, plus done and fail.
  "w-claude": { who: "claude", pose: "reviewing", area: "work", node: "login", role: "reviewer" },
  "w-gemini": { who: "gemini", pose: "working", area: "work", node: "schema", role: "planner" },
  "w-gpt": { who: "gpt", pose: "working", area: "work", node: "docs", role: "executor" },
  "w-deepseek": { who: "deepseek", pose: "working", area: "work", node: "search", role: "researcher" },
  "w-claude-done": { who: "claude", pose: "finished", area: "work", node: "tests" },
  "w-gpt-fail": { who: "gpt", pose: "failed", area: "work", node: "deploy" },
  "w-gemini-cast": { who: "gemini", pose: "working", area: "work", node: "tests", role: "tester" },
  // Meeting room: one speaks, one listens.
  "m-claude": { who: "claude", pose: "working", area: "meeting", node: "docs", bubble: "The schema needs an index on email." },
  "m-gpt": { who: "gpt", pose: "working", area: "meeting", node: "docs" },
  // Entrance: one asks, one waits to be let in.
  "e-deepseek": { who: "deepseek", pose: "asking-user", area: "entrance", node: "schema" },
  "e-gemini": { who: "gemini", pose: "awaiting-accept", area: "entrance", node: "deploy" },
  // Lounge: idle (claude sleeps there as a visitor, below).
  "l-gpt": { who: "gpt", pose: "idle", area: "lounge", node: "login" },
  // Away on a usage limit: every outing once.
  "a-deepseek": { who: "deepseek", pose: "limit", area: "lounge", node: "search", outing: "hike" },
  "a-gemini": { who: "gemini", pose: "limit", area: "lounge", node: "docs", outing: "ride" },
  "a-claude": { who: "claude", pose: "limit", area: "lounge", node: "login", outing: "stream" },
  "a-gpt": { who: "gpt", pose: "limit", area: "lounge", node: "schema", outing: "fish" },
  "a-claude2": { who: "claude", pose: "limit", area: "lounge", node: "tests", outing: "camp" },
  "a-deepseek2": { who: "deepseek", pose: "limit", area: "lounge", node: "deploy", outing: "bbq" },
};

function makeBuilding(): Building {
  const actors = Object.entries(SPECS).map(([key, spec]) => actor(key, spec));
  const ids = (area: Area) => actors.filter((a) => a.area === area).map((a) => a.id);
  const lounge: LoungeSpot[] = ids("lounge").map((id) => ({ id: `spot:${id}`, actorId: id }));
  const asleep = { kind: "project" as const, project: "other" };
  lounge.push({
    id: "spot:visitor-claude",
    visitor: {
      project: "other",
      label: "Other project",
      ...WHO.claude,
      pose: "offline",
      title: "Old notes",
      target: asleep,
      actions: [asleep],
    },
  });
  return {
    current: {
      project: PROJECT,
      label: "Preview",
      layout: "default",
      style: "default",
      styleSet: false,
      work: ids("work"),
      freeDesks: 1,
      meeting: ids("meeting"),
      items: [],
      entrance: ids("entrance"),
      meetings: [],
    },
    global: null,
    others: [],
    doorSigns: [],
    lounge,
    loungeMore: 0,
    racks: [
      {
        runnerId: "rn1",
        name: "mac-mini",
        ownerName: "preview",
        online: true,
        slots: 6,
        busy: 4,
        agentKinds: ["claude", "codex", "gemini", "opencode"],
        target: { kind: "runners" },
      },
    ],
    actors,
    energy: { used: 180_000, cap: 500_000 },
    truncated: false,
    total: 1,
  };
}

const query = new URLSearchParams(window.location.search);

function setQuery(key: string, value: string) {
  const next = new URLSearchParams(window.location.search);
  if (value) next.set(key, value);
  else next.delete(key);
  window.history.replaceState(null, "", `${window.location.pathname}?${next}`);
}

/** Puts a preference in memory only: the preview must not change the app's. */
function setPreferenceInMemory(key: "themePack" | "language", value: string) {
  useApp.setState((state) => ({ preferences: { ...state.preferences, [key]: value } }));
}

const bar: CSSProperties = {
  display: "flex",
  flexWrap: "wrap",
  gap: 12,
  alignItems: "center",
  padding: "8px 16px",
  borderBottom: "1px solid var(--border)",
  background: "var(--surface)",
  color: "var(--text)",
  font: "13px system-ui, sans-serif",
};

export function ThemePreview() {
  const packs = useThemePacks((s) => s.packs);
  const loaded = useThemePacks((s) => s.loaded && !s.loading);
  const error = useThemePacks((s) => s.error);
  const [packId, setPackId] = useState(query.get("pack") ?? "");
  const [state, setState] = useState(query.get("state") ?? "");
  const language = useApp((s) => s.preferences.language);

  useEffect(() => useThemePacks.getState().ensureLoaded(), []);
  useEffect(() => {
    const lang = query.get("lang");
    if (lang === "en" || lang === "zh-TW") setPreferenceInMemory("language", lang);
  }, []);
  const chosen = packs.find((pack) => pack.id === packId) ?? packs[0];
  useEffect(() => {
    if (chosen) setPreferenceInMemory("themePack", chosen.id);
  }, [chosen]);

  useThemeSkin();
  const reduced = useReducedMotion();
  const hidden = usePageHidden();
  const px = useArtPixel();
  const base = useActiveThemeCast(reduced || hidden);
  const cast = useMemo(
    () => (base && state ? { ...base, figureOf: (who: { agent: string; model: string }) => base.figureOf(who, state) } : base),
    [base, state],
  );
  const building = useMemo(makeBuilding, []);
  const scenes = useMemo(() => {
    const manifest = useThemePacks.getState().manifest?.value;
    const names = new Set<string>();
    for (const character of Object.values(manifest?.characters ?? {})) {
      for (const name of Object.keys(character.scenes)) names.add(name);
    }
    return [...names].sort();
    // Re-read whenever the cast (and so the manifest) changes.
  }, [base]);

  return (
    <div style={{ minHeight: "100vh", background: "var(--bg)" }}>
      <div style={bar}>
        <strong>Theme preview (dev only)</strong>
        <label>
          Pack{" "}
          <select
            value={chosen?.id ?? ""}
            onChange={(event) => {
              setPackId(event.target.value);
              setQuery("pack", event.target.value);
            }}
          >
            {packs.map((pack) => (
              <option key={pack.id} value={pack.id}>
                {pack.name["zh-TW"] ?? pack.name.en} ({pack.id})
              </option>
            ))}
          </select>
        </label>
        <label>
          Everyone as{" "}
          <select
            value={state}
            onChange={(event) => {
              setState(event.target.value);
              setQuery("state", event.target.value);
            }}
          >
            <option value="">(their own state)</option>
            {scenes.map((name) => (
              <option key={name} value={name}>
                {name}
              </option>
            ))}
          </select>
        </label>
        <label>
          Language{" "}
          <select
            value={language}
            onChange={(event) => {
              setPreferenceInMemory("language", event.target.value);
              setQuery("lang", event.target.value);
            }}
          >
            <option value="en">en</option>
            <option value="zh-TW">zh-TW</option>
          </select>
        </label>
        <span style={{ color: "var(--text-2)" }}>Places: the room's side buttons, or keys 1–6.</span>
      </div>
      {error ? (
        <p style={{ padding: 16 }}>Could not list theme packs: {error}</p>
      ) : !loaded ? (
        <p style={{ padding: 16 }}>Loading packs…</p>
      ) : !chosen ? (
        <p style={{ padding: 16 }}>No theme pack is imported.</p>
      ) : !cast ? (
        <p style={{ padding: 16 }}>Loading {chosen.id}…</p>
      ) : (
        <div
          className="px-office"
          data-motion={reduced ? "reduced" : "full"}
          data-paused={hidden ? "true" : undefined}
          style={{ ["--px" as string]: `${px}px`, padding: 16 } as CSSProperties}
        >
          <ThemeCastContext.Provider value={cast}>
            {/* Keyed so a new pack or state opens where the people are again. */}
            <WarRoom key={`${chosen.id}/${state}`} building={building} titleOf={titleOf} rowOf={() => undefined} />
          </ThemeCastContext.Provider>
        </div>
      )}
    </div>
  );
}
