import { req, uploadWithProgress } from "./http";

/** A theme pack as the server lists it. Its pictures are in its manifest. */
export interface ThemePack {
  id: string;
  name: { en: string; "zh-TW"?: string };
  version: string;
  author: string;
  /** Who imported it. */
  owner: string;
  sizeBytes: number;
  createdAt: string;
  updatedAt: string;
  /** This viewer imported it. */
  mine: boolean;
  canDelete: boolean;
}

export interface ThemePackList {
  packs: ThemePack[];
  /** The site default's id, "" when there is none. */
  default: string;
}

export const themesApi = {
  listThemePacks: () => req<ThemePackList>("/api/themes"),
  /** A pack's stored theme.json, unread: parseThemeManifest says what is usable. */
  getThemeManifest: (id: string) => req<unknown>(`/api/themes/${encodeURIComponent(id)}`),
  /** Refused whole, with the server's reason, if anything in the zip is wrong. */
  importThemePack: (file: File, onProgress?: (sent: number, total: number) => void) => {
    const body = new FormData();
    body.append("file", file);
    return uploadWithProgress<ThemePack>("/api/themes", body, onProgress);
  },
  deleteThemePack: (id: string) => req<void>(`/api/themes/${encodeURIComponent(id)}`, { method: "DELETE" }),
  /** Administrators only; an empty id clears the default. */
  setDefaultThemePack: (id: string) =>
    req<unknown>("/api/themes/default", { method: "PUT", body: JSON.stringify({ id }) }),
};
