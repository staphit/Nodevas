import { create } from "zustand";
import { api, type ViewPack } from "../api";

/**
 * The workspace's view packs (docs/view-packs.md). Loaded when something
 * shows them — Station's view switch, the palette, Appearance settings — and
 * again on packs-changed. Small and in the main bundle; drawing a pack lives
 * in the game chunk (packs.tsx).
 */
export interface PackStoreState {
  packs: ViewPack[];
  loaded: boolean;
  error: string | null;
  busy: boolean;
  load: () => Promise<void>;
  /** Refetches only once something has loaded the list. */
  onPacksChanged: () => void;
  /** Installs a .zip; resolves with the error text, or "" on success. */
  install: (file: File) => Promise<string>;
  remove: (id: string) => Promise<string>;
}

/** A pack's sprites must be this server's pack files: anything else is
 * dropped, whatever the list said. */
const PACK_URL = /^\/api\/packs\/[a-z0-9-]{1,40}\/files\/[A-Za-z0-9._/-]+(\?v=\d+)?$/;

export function safePack(pack: ViewPack): ViewPack | null {
  if (!/^[a-z0-9-]{1,40}$/.test(pack.id) || (pack.layout !== "rooms" && pack.layout !== "path")) return null;
  const sprites: Record<string, string> = {};
  for (const [key, url] of Object.entries(pack.sprites ?? {})) {
    // A ".." would be resolved by the browser before it reaches the server.
    if (typeof url === "string" && PACK_URL.test(url) && !url.includes("..")) sprites[key] = url;
  }
  const color = (value: string | undefined) => (value && /^#[0-9a-f]{3}([0-9a-f]{3})?$/i.test(value) ? value : undefined);
  return {
    ...pack,
    sprites,
    palette: { ground: color(pack.palette?.ground), text: color(pack.palette?.text) },
  };
}

/** A pack's name in the reader's language, else English, else its id. */
export function packName(pack: ViewPack, language: string): string {
  return pack.name?.[language] || pack.name?.en || pack.id;
}

function message(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

function base64(buffer: ArrayBuffer): string {
  const bytes = new Uint8Array(buffer);
  let binary = "";
  const chunk = 0x8000;
  for (let i = 0; i < bytes.length; i += chunk) {
    binary += String.fromCharCode(...bytes.subarray(i, i + chunk));
  }
  return btoa(binary);
}

let inFlight: Promise<void> | null = null;

export const usePacks = create<PackStoreState>((set, get) => ({
  packs: [],
  loaded: false,
  error: null,
  busy: false,

  load: () => {
    if (inFlight) return inFlight;
    const work = (async () => {
      try {
        const res = await api.listPacks();
        const packs = (res.packs ?? []).map(safePack).filter((p): p is ViewPack => p !== null);
        set({ packs, loaded: true, error: null });
      } catch (error) {
        set({ loaded: true, error: message(error) });
      }
    })().finally(() => {
      if (inFlight === work) inFlight = null;
    });
    inFlight = work;
    return work;
  },

  onPacksChanged: () => {
    if (get().loaded) void get().load();
  },

  install: async (file) => {
    set({ busy: true });
    try {
      await api.installPack(base64(await file.arrayBuffer()));
      await get().load();
      return "";
    } catch (error) {
      return message(error);
    } finally {
      set({ busy: false });
    }
  },

  remove: async (id) => {
    set({ busy: true });
    try {
      await api.removePack(id);
      await get().load();
      return "";
    } catch (error) {
      return message(error);
    } finally {
      set({ busy: false });
    }
  },
}));
