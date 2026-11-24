// Shared theme-pack character and room art for both the war room and the
// picture-card pickers. Imports only themeCast and themeCastContext (which
// wraps useThemePack), so pickers never pull in the war-room chunk.

import { useMemo, type CSSProperties, type ReactNode } from "react";

import type { ThemeScene } from "../../theme-pack/themeManifest";
import { hash, type CastFigure } from "./themeCast";
import { useActiveThemeCast, type ThemeCast } from "./themeCastContext";

/** A face in the roster, in CSS px: warroom.css has the same. */
export const FACE_PX = 38;
/** How much of the canvas's width a face takes, and how far under her sign
 * it starts, as a share of the canvas's height. */
export const FACE_REACH = 0.29;
export const FACE_UNDER_SIGN = 0.025;

/** A scene's still as a background: the first frame of its strip, as wide as
 * what it fills. */
export function still(cast: ThemeCast, scene: ThemeScene, down: string): CSSProperties {
  return {
    backgroundImage: `url(${JSON.stringify(cast.url(scene.image))})`,
    backgroundSize: `${(scene.frames?.count ?? 1) * 100}% auto`,
    backgroundPosition: `0 ${down}`,
  };
}

/** A character's face, cut from her picture: under her sign where she has
 * one, else the top of what is drawn of her. */
export function Face({ figure, cast }: { figure?: CastFigure; cast: ThemeCast }) {
  if (!figure) {
    return (
      <span className="px-story-face is-unnamed" aria-hidden="true">
        ?
      </span>
    );
  }
  const { scene } = figure;
  const [width, height] = cast.canvas;
  const reach = FACE_REACH * width;
  const scale = FACE_PX / reach;
  const centre = scene.sign ? scene.sign[0] : (scene.box[0] + scene.box[2]) / 2;
  const top = scene.sign ? scene.sign[1] + FACE_UNDER_SIGN * height : scene.box[1];
  return (
    <span
      className="px-story-face"
      aria-hidden="true"
      style={{
        backgroundImage: `url(${JSON.stringify(cast.url(scene.image))})`,
        backgroundSize: `${(scene.frames?.count ?? 1) * scene.w * scale}px ${scene.h * scale}px`,
        backgroundPosition: `${-(centre - reach / 2 - scene.x) * scale}px ${-(top - scene.y) * scale}px`,
      }}
    />
  );
}

/** What the pack's skin gives the stage: its accent, and under a dark skin
 * its night, ink and panels. A light skin keeps the stage's own night: the
 * pictures are lit for one. */
export function skinVars(cast: ThemeCast): CSSProperties {
  const skin = cast.skin;
  if (!skin) return {};
  const vars: Record<string, string> = {};
  if (skin.colors.accent) vars["--story-accent"] = skin.colors.accent;
  if (skin.scheme === "dark") {
    if (skin.colors.bg) vars["--story-night"] = skin.colors.bg;
    if (skin.colors.text) vars["--story-ink"] = skin.colors.text;
    if (skin.colors["text-2"]) vars["--story-soft"] = skin.colors["text-2"];
    if (skin.colors.surface) vars["--story-deep"] = skin.colors.surface;
  }
  return vars as CSSProperties;
}

/** Inline styles for an <img> inside an overflow:hidden box, showing frame 0
 * of `scene.image` cropped to `scene.box` (the character's drawn bounds). */
export function castCardImgStyle(scene: ThemeScene): CSSProperties {
  const frames = scene.frames?.count ?? 1;
  const cropLeft = scene.box[0] - scene.x;
  const cropTop = scene.box[1] - scene.y;
  const cropW = Math.max(1, scene.box[2] - scene.box[0]);
  const stripW = frames * scene.w;
  const sceneH = Math.max(1, scene.h);
  return {
    position: "absolute",
    top: 0,
    left: `${-(cropLeft / cropW) * 100}%`,
    width: `${(stripW / cropW) * 100}%`,
    height: "auto",
    maxWidth: "none",
    transform: `translateY(${-(cropTop / sceneH) * 100}%)`,
  };
}

export interface CastCardArt {
  figure: CastFigure;
  url: string;
  /** Cropped to `scene.box` (frame 0) for an <img> inside an overflow:hidden box. */
  imgStyle: CSSProperties;
  /** Width / height ratio of `scene.box`. */
  aspectRatio: string;
  /** Marks the scene image broken on this hook instance so the card falls back. */
  onError: () => void;
  /** Character's sign image URL, when present. */
  signUrl?: string;
  /** Pack's localised title for the character, or undefined. */
  title: (language: string) => string | undefined;
}

/** Resolves a theme-pack character card outside (or inside) the war room. */
export function useCastCard(agent: string, model: string, scene = "idle"): CastCardArt | undefined {
  const cast = useActiveThemeCast(true);
  return useMemo(() => {
    if (!cast) return undefined;
    const figure = cast.figureOf({ agent, model }, scene);
    if (!figure) return undefined;
    const url = cast.url(figure.scene.image);
    if (!url) return undefined;
    const cropW = Math.max(1, figure.scene.box[2] - figure.scene.box[0]);
    const cropH = Math.max(1, figure.scene.box[3] - figure.scene.box[1]);
    const signUrl = figure.sign ? cast.url(figure.sign) || undefined : undefined;
    return {
      figure,
      url,
      imgStyle: castCardImgStyle(figure.scene),
      aspectRatio: `${cropW} / ${cropH}`,
      onError: () => cast.broken(figure.scene.image),
      signUrl,
      title: (language: string) => cast.title(figure.character, language),
    };
  }, [cast, agent, model, scene]);
}

/** Renders a cropped character card <img> with onError fallback (e.g. initials). */
export function CastPortrait({
  agent,
  model,
  scene = "idle",
  fallback,
  className,
}: {
  agent: string;
  model: string;
  scene?: string;
  fallback: ReactNode;
  className?: string;
}) {
  const art = useCastCard(agent, model, scene);
  if (!art) return <>{fallback}</>;
  return (
    <span className={className} aria-hidden="true" data-character={art.figure.character}>
      <img src={art.url} alt="" draggable={false} style={art.imgStyle} onError={art.onError} />
    </span>
  );
}

export interface FolderCardArt {
  roomId: string;
  url: string;
  /** Marks the room image broken on this hook instance so the card falls back. */
  onError: () => void;
}

/** Picks a stable pack room still for a repo id by hashing over sorted room
 * ids. Returns undefined when no pack is active, the pack has no rooms, or
 * the room image fails to load. */
export function useFolderArt(repoId: string): FolderCardArt | undefined {
  const cast = useActiveThemeCast(true);
  return useMemo(() => {
    if (!cast || !repoId || cast.roomIds.length === 0) return undefined;
    const roomId = cast.roomIds[hash(repoId) % cast.roomIds.length];
    const path = cast.roomPath(roomId);
    if (!path) return undefined;
    const url = cast.url(path);
    if (!url) return undefined;
    return {
      roomId,
      url,
      onError: () => cast.broken(path),
    };
  }, [cast, repoId]);
}
