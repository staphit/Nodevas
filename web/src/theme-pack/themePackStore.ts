import { create } from "zustand";
import { api, ConflictError, type ThemePack } from "../api";
import { isThemePack, parseThemeManifest, type ThemeManifest } from "./themeManifest";

/** What the viewer sees: what they picked, else the site default, else (an
 * undefined here) the built-in people. A pick that no longer exists falls
 * through; "none" is the built-in people whatever the site says. */
export function activeThemePack(packs: ThemePack[], defaultId: string, picked: string): ThemePack | undefined {
  if (picked === "none") return undefined;
  return packs.find((pack) => pack.id === picked) ?? packs.find((pack) => pack.id === defaultId);
}

/** A zip on its way up. Once all of it is sent, the server is reading it. */
export interface ThemePackUpload {
  name: string;
  sent: number;
  total: number;
}

export interface ThemePacksState {
  /** Whether anything has asked for the list yet. */
  loaded: boolean;
  loading: boolean;
  packs: ThemePack[];
  defaultId: string;
  /** The list could not be read. */
  error: string | null;
  /** The manifest last asked for; a null value could not be used. */
  manifest: { id: string; value: ThemeManifest | null } | null;

  /** Here so an upload outlives the row it was started from being closed. */
  upload: ThemePackUpload | null;
  uploadError: string | null;
  /** A delete or a change of default underway. */
  busy: boolean;
  /** Why it failed: a failure here says nothing about the pack on show. */
  actionError: string | null;

  refresh: () => Promise<void>;
  /** Fetches the list if nothing has yet. */
  ensureLoaded: () => void;
  /** Fetches a manifest unless it is held or on its way. */
  ensureManifest: (id: string) => void;
  /** themes-changed, with the pack's id, or none after missing some. */
  onChanged: (id?: string) => void;
  importPack: (file: File) => Promise<void>;
  deletePack: (id: string) => Promise<void>;
  setDefault: (id: string) => Promise<void>;
  reset: () => void;
}

/** "" for a 409 with no words of its own: its message is only "conflict". */
function message(error: unknown): string {
  if (error instanceof ConflictError) return error.serverMessage;
  return error instanceof Error ? error.message : String(error);
}

/** Bumped by every fetch, so a slow answer cannot land over a newer one. */
let listGeneration = 0;
let manifestGeneration = 0;
/** The manifest on its way, so several readers ask once. */
let manifestAsked: string | null = null;

async function loadManifest(id: string): Promise<void> {
  const mine = ++manifestGeneration;
  manifestAsked = id;
  let value: ThemeManifest | null = null;
  try {
    const parsed = parseThemeManifest(await api.getThemeManifest(id));
    if (parsed?.id === id) value = parsed;
  } catch {
    /* no manifest: the built-in people show */
  }
  if (mine !== manifestGeneration) return;
  manifestAsked = null;
  useThemePacks.setState({ manifest: { id, value } });
}

export const useThemePacks = create<ThemePacksState>((set, get) => {
  const act = async (work: () => Promise<unknown>) => {
    set({ busy: true, actionError: null });
    try {
      await work();
      await get().refresh();
    } catch (error) {
      set({ actionError: message(error) });
    } finally {
      set({ busy: false });
    }
  };

  return {
    loaded: false,
    loading: false,
    packs: [],
    defaultId: "",
    error: null,
    manifest: null,
    upload: null,
    uploadError: null,
    busy: false,
    actionError: null,

    refresh: async () => {
      const mine = ++listGeneration;
      set({ loading: true, loaded: true });
      try {
        const list = await api.listThemePacks();
        if (mine !== listGeneration) return;
        set({
          // A nil list in Go arrives as null.
          packs: Array.isArray(list.packs) ? list.packs.filter(isThemePack) : [],
          defaultId: typeof list.default === "string" ? list.default : "",
          error: null,
        });
      } catch (error) {
        if (mine !== listGeneration) return;
        set({ error: message(error) });
      } finally {
        if (mine === listGeneration) set({ loading: false });
      }
    },

    ensureLoaded: () => {
      if (!get().loaded) void get().refresh();
    },

    ensureManifest: (id) => {
      if (get().manifest?.id === id || manifestAsked === id) return;
      void loadManifest(id);
    },

    onChanged: (id) => {
      if (!get().loaded) return;
      void get().refresh();
      // Fetched over the old one, which stays on screen until the answer: a
      // change to another pack must not flash the built-in people.
      const held = get().manifest;
      if (held && (!id || held.id === id)) void loadManifest(held.id);
    },

    importPack: async (file) => {
      if (get().upload) return;
      const name = file.name;
      set({ upload: { name, sent: 0, total: file.size }, uploadError: null });
      try {
        await api.importThemePack(file, (sent, total) => set({ upload: { name, sent, total } }));
        await get().refresh();
      } catch (error) {
        set({ uploadError: message(error) });
      } finally {
        set({ upload: null });
      }
    },

    deletePack: (id) => act(() => api.deleteThemePack(id)),

    setDefault: (id) => act(() => api.setDefaultThemePack(id)),

    reset: () => {
      listGeneration += 1;
      manifestGeneration += 1;
      manifestAsked = null;
      set({
        loaded: false,
        loading: false,
        packs: [],
        defaultId: "",
        error: null,
        manifest: null,
        upload: null,
        uploadError: null,
        busy: false,
        actionError: null,
      });
    },
  };
});
