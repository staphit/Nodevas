import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { api, type ThemePack } from "../../api";
import { readPreferences, writePreference } from "../../preferences";
import { useApp } from "../../store";
import { parseThemeManifest } from "../../theme-pack/themeManifest";
import { useThemePacks } from "../../theme-pack/themePackStore";
import { CastPortrait, castCardImgStyle, useCastCard, useFolderArt } from "./castArt";

const PACK: ThemePack = {
  id: "studio",
  name: { en: "Studio" },
  version: "1.0.0",
  author: "tester",
  owner: "u1",
  sizeBytes: 1,
  createdAt: "2026-11-18T00:00:00Z",
  updatedAt: "2026-11-18T00:00:00Z",
  mine: true,
  canDelete: true,
};

const MANIFEST = {
  schema: 1,
  id: "studio",
  name: { en: "Studio" },
  version: "1.0.0",
  author: "tester",
  canvas: [200, 240],
  rooms: {
    zebra: "rooms/zebra.webp",
    alpha: "rooms/alpha.webp",
    middle: "rooms/middle.webp",
  },
  characters: {
    sage: {
      match: { agents: ["claude"], models: ["claude*"] },
      sign: "signs/sage.png",
      title: { en: "Sage", "zh-TW": "賢者" },
      scenes: {
        idle: {
          image: "sage/idle.webp",
          x: 20,
          y: 10,
          w: 160,
          h: 200,
          box: [40, 30, 120, 190] as [number, number, number, number],
          frames: { count: 2, blink: 1, click: [1] },
        },
        speak: {
          image: "sage/speak.webp",
          x: 0,
          y: 0,
          w: 200,
          h: 240,
          box: [50, 20, 150, 220],
        },
      },
    },
    whale: {
      match: { agents: [], models: ["*deepseek*"] },
      title: { en: "DeepSeek" },
      scenes: {
        idle: {
          image: "whale/idle.webp",
          x: 0,
          y: 0,
          w: 100,
          h: 120,
          box: [10, 12, 90, 112],
        },
      },
    },
  },
};

function withPack(manifest: object = MANIFEST) {
  const value = parseThemeManifest(manifest);
  if (!value) throw new Error("test manifest failed to parse");
  useThemePacks.setState({ loaded: true, packs: [PACK], defaultId: PACK.id, manifest: { id: PACK.id, value } });
}

function CardProbe({ agent, model, scene }: { agent: string; model: string; scene?: string }) {
  const art = useCastCard(agent, model, scene);
  return (
    <div>
      <span data-testid="char">{art?.figure.character ?? "none"}</span>
      <span data-testid="url">{art?.url ?? "none"}</span>
      <CastPortrait
        agent={agent}
        model={model}
        scene={scene}
        className="card-thumb"
        fallback={<span className="station-avatar agent-claude">CL</span>}
      />
    </div>
  );
}

function FolderProbe({ repoId }: { repoId: string }) {
  const art = useFolderArt(repoId);
  if (!art) return <span data-testid="folder-fallback">plain</span>;
  return (
    <div>
      <span data-testid="room-id">{art.roomId}</span>
      <img data-testid="room-img" src={art.url} alt="" loading="lazy" onError={art.onError} />
    </div>
  );
}

beforeEach(() => {
  vi.spyOn(api, "listThemePacks").mockResolvedValue({ packs: [], default: "" });
  useThemePacks.getState().reset();
  writePreference("themePack", "");
  useApp.setState({ preferences: readPreferences() });
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe("castArt", () => {
  it("matches a deepseek model to the DeepSeek still, and default to the agent's character", () => {
    withPack();
    const { rerender } = render(<CardProbe agent="opencode" model="deepseek-r1" />);
    expect(screen.getByTestId("char")).toHaveTextContent("whale");
    expect(screen.getByTestId("url")).toHaveTextContent("/api/themes/studio/files/whale/idle.webp");

    rerender(<CardProbe agent="claude" model="default" />);
    expect(screen.getByTestId("char")).toHaveTextContent("sage");
    expect(screen.getByTestId("url")).toHaveTextContent("/api/themes/studio/files/sage/idle.webp");
  });

  it("falls back to initials on broken image via img onError, and when no pack is active", () => {
    const { container, rerender } = render(<CardProbe agent="claude" model="default" />);
    expect(container.querySelector("img")).toBeNull();
    expect(container.querySelector(".station-avatar")).toHaveTextContent("CL");

    withPack();
    rerender(<CardProbe agent="claude" model="default" />);
    const img = container.querySelector("img")!;
    expect(img).not.toBeNull();
    expect(img.getAttribute("src")).toBe("/api/themes/studio/files/sage/idle.webp");

    fireEvent.error(img);
    expect(container.querySelector("img")).toBeNull();
    expect(container.querySelector(".station-avatar")).toHaveTextContent("CL");
  });

  it("crops frame 0 of a scene strip to scene.box", () => {
    // x=20, y=10, w=160, h=200, frames=2 (stripW=320), box=[40, 30, 120, 190]
    // cropLeft=20, cropTop=20, cropW=80 => width = 320/80 = 400%, left = -20/80 = -25%, translateY(-20/200 = -10%)
    const style = castCardImgStyle(MANIFEST.characters.sage.scenes.idle);
    expect(style).toEqual({
      position: "absolute",
      top: 0,
      left: "-25%",
      width: "400%",
      height: "auto",
      maxWidth: "none",
      transform: "translateY(-10%)",
    });
  });

  it("picks a stable room per repo id over sorted room ids, returns none without rooms, and falls back on error", () => {
    withPack();
    const first = render(<FolderProbe repoId="repo-billing" />);
    const firstRoom = screen.getByTestId("room-id").textContent;
    expect(["alpha", "middle", "zebra"]).toContain(firstRoom);
    const img = screen.getByTestId("room-img");
    expect(img.getAttribute("src")).toBe(`/api/themes/studio/files/rooms/${firstRoom}.webp`);

    // Broken room image falls back to plain folder card.
    fireEvent.error(img);
    expect(screen.getByTestId("folder-fallback")).toHaveTextContent("plain");
    first.unmount();

    // Reordering keys in manifest.rooms yields the exact same room id.
    withPack({
      ...MANIFEST,
      rooms: {
        alpha: "rooms/alpha.webp",
        middle: "rooms/middle.webp",
        zebra: "rooms/zebra.webp",
      },
    });
    const second = render(<FolderProbe repoId="repo-billing" />);
    expect(screen.getByTestId("room-id")).toHaveTextContent(firstRoom!);
    second.unmount();

    // No rooms in pack => plain folder fallback.
    withPack({ ...MANIFEST, rooms: {} });
    render(<FolderProbe repoId="repo-billing" />);
    expect(screen.getByTestId("folder-fallback")).toHaveTextContent("plain");
  });

  it("stays clear of the war-room chunk imports", () => {
    const source = readFileSync(resolve(process.cwd(), "src/game/warroom/castArt.tsx"), "utf8");
    const imports = [...source.matchAll(/from\s+"([^"]+)"/g)].map((m) => m[1]);
    expect(imports.every((spec) => ["react", "../../theme-pack/themeManifest", "./themeCast", "./themeCastContext", "../../theme-pack/useThemePack"].includes(spec))).toBe(true);
  });
});
