// The theme pack this viewer sees, for whatever draws people and rooms. It
// fails soft: with no pack, or on any error at all, it gives undefined and the
// caller draws the built-in pixel people as if packs did not exist.

import { useEffect, useMemo } from "react";
import type { ThemePack } from "../api";
import { usePreference } from "../store";
import { isSafeThemePath, type ThemeManifest } from "./themeManifest";
import { activeThemePack, useThemePacks } from "./themePackStore";

export interface ActiveThemePack {
  pack: ThemePack;
  manifest: ThemeManifest;
  /** A path of the manifest as a URL, "" for one that would leave the pack. */
  imageUrl: (path: string) => string;
}

function imageUrlFor(packId: string, path: string): string {
  if (!isSafeThemePath(path)) return "";
  try {
    const encoded = path.split("/").map(encodeURIComponent).join("/");
    return `/api/themes/${encodeURIComponent(packId)}/files/${encoded}`;
  } catch {
    // A lone surrogate cannot be encoded.
    return "";
  }
}

export function useThemePack(): ActiveThemePack | undefined {
  const picked = usePreference("themePack");
  const packs = useThemePacks((s) => s.packs);
  const defaultId = useThemePacks((s) => s.defaultId);
  const failed = useThemePacks((s) => s.error !== null);
  const held = useThemePacks((s) => s.manifest);
  const pack = failed ? undefined : activeThemePack(packs, defaultId, picked);
  const id = pack?.id;

  useEffect(() => useThemePacks.getState().ensureLoaded(), []);
  useEffect(() => {
    if (id) useThemePacks.getState().ensureManifest(id);
  }, [id]);

  const manifest = held && held.id === id ? held.value : null;
  return useMemo(() => {
    if (!pack || !manifest) return undefined;
    return { pack, manifest, imageUrl: (path: string) => imageUrlFor(pack.id, path) };
  }, [pack, manifest]);
}
