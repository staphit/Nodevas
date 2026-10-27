import { req } from "./http";

// Mirrors internal/httpapi packs (docs/view-packs.md): view packs installed in
// the workspace. Anyone may list them and load their images; admins install
// and remove.

export type ViewPackLayout = "rooms" | "path";

export interface ViewPack {
  id: string;
  name: Record<string, string>;
  layout: ViewPackLayout;
  palette?: { ground?: string; text?: string };
  /** Sprite key → URL of the image on this server. */
  sprites: Record<string, string>;
  installedAt: number;
  installedBy?: string;
}

export const packsApi = {
  listPacks: () => req<{ packs: ViewPack[] }>("/api/packs"),
  /** zip: the .zip file, base64. */
  installPack: (zip: string) =>
    req<{ pack: ViewPack }>("/api/packs", { method: "POST", body: JSON.stringify({ zip }) }),
  /** 204 on success. */
  removePack: (id: string) => req<void>(`/api/packs/${encodeURIComponent(id)}`, { method: "DELETE" }),
};
