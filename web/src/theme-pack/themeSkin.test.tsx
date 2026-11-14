import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api, type ThemePack } from "../api";
import { configurePreferenceStorage, readPreferences } from "../preferences";
import { useApp } from "../store";
import type { ThemeSkin } from "./themeManifest";
import { useThemePacks } from "./themePackStore";
import { applyThemeSkin, useThemeSkin } from "./themeSkin";

const night: ThemeSkin = {
  scheme: "dark",
  font: "serif",
  colors: { bg: "#0d0e1c", surface: "#15142b", "surface-2": "#1c1b36" },
};
const day: ThemeSkin = { scheme: "light", colors: { bg: "#fdf6e3", accent: "#b58900" } };

/** What is on the root that a look could have put there. */
function look(root: HTMLElement) {
  const style: Record<string, string> = {};
  for (const property of Array.from(root.style)) style[property] = root.style.getPropertyValue(property);
  return {
    theme: root.getAttribute("data-theme"),
    font: root.getAttribute("data-theme-font"),
    pack: root.getAttribute("data-theme-pack"),
    style,
  };
}

const builtIn = (theme: string) => ({ theme, font: null, pack: null, style: {} });

describe("applyThemeSkin", () => {
  let root: HTMLElement;
  beforeEach(() => {
    root = document.createElement("html");
  });

  it("puts the viewer's own theme on the root when there is no pack", () => {
    expect(applyThemeSkin(root, "dark")).toEqual([]);
    expect(look(root)).toEqual(builtIn("dark"));
    applyThemeSkin(root, "light");
    expect(look(root)).toEqual(builtIn("light"));
  });

  it("puts a skin's scheme, colours and font over the viewer's theme", () => {
    expect(applyThemeSkin(root, "light", night, "magic")).toEqual(["bg", "surface", "surface-2"]);
    expect(look(root)).toEqual({
      theme: "dark",
      font: "serif",
      pack: "magic",
      style: { "--bg": "#0d0e1c", "--surface": "#15142b", "--surface-2": "#1c1b36" },
    });
  });

  it("names a pack with no skin, and leaves the viewer's theme as it is", () => {
    expect(applyThemeSkin(root, "dark", undefined, "magic")).toEqual([]);
    expect(look(root)).toEqual({ theme: "dark", font: null, pack: "magic", style: {} });
  });

  it("says sans as it says serif", () => {
    applyThemeSkin(root, "light", { ...day, font: "sans" }, "sea");
    expect(root.getAttribute("data-theme-font")).toBe("sans");
  });

  it("takes off what another skin set and this one does not", () => {
    const set = applyThemeSkin(root, "dark", night, "magic");
    expect(applyThemeSkin(root, "dark", day, "sea", set)).toEqual(["bg", "accent"]);
    expect(look(root)).toEqual({
      theme: "light",
      font: null,
      pack: "sea",
      style: { "--bg": "#fdf6e3", "--accent": "#b58900" },
    });
  });

  it("goes back to the viewer's own theme when the skin goes", () => {
    const set = applyThemeSkin(root, "light", night, "magic");
    expect(applyThemeSkin(root, "light", undefined, undefined, set)).toEqual([]);
    expect(look(root)).toEqual(builtIn("light"));
  });

  it("takes off only what it set", () => {
    root.style.setProperty("--font-scale", "1.25");
    root.style.setProperty("--text", "#111111");
    root.lang = "en";
    const set = applyThemeSkin(root, "light", night, "magic");
    applyThemeSkin(root, "light", undefined, undefined, set);
    expect(look(root).style).toEqual({ "--font-scale": "1.25", "--text": "#111111" });
    expect(root.lang).toBe("en");
  });

  it("writes nothing when nothing changed", async () => {
    const set = applyThemeSkin(root, "light", night, "magic");
    const seen: MutationRecord[] = [];
    const observer = new MutationObserver((records) => void seen.push(...records));
    observer.observe(root, { attributes: true, attributeFilter: ["data-theme", "data-theme-font", "data-theme-pack"] });
    applyThemeSkin(root, "light", night, "magic", set);
    // The viewer's own setting moves under a skin: the root stays the skin's.
    applyThemeSkin(root, "dark", night, "magic", set);
    await Promise.resolve();
    seen.push(...observer.takeRecords());
    observer.disconnect();
    expect(seen).toEqual([]);
    expect(root.getAttribute("data-theme")).toBe("dark");
  });

  it("writes no colour that is not hex, whatever it is handed", () => {
    const odd = { scheme: "dark", colors: { bg: "url(//x.test/a)", text: "#fff;color:red", accent: "#abc" } };
    expect(applyThemeSkin(root, "light", odd as ThemeSkin, "magic")).toEqual(["accent"]);
    expect(look(root).style).toEqual({ "--accent": "#abc" });
  });

  it("writes no colour the contract does not name", () => {
    const odd = { scheme: "dark", colors: { shadow: "#000", "bg;color": "#000" } };
    expect(applyThemeSkin(root, "light", odd as unknown as ThemeSkin, "magic")).toEqual([]);
    expect(look(root).style).toEqual({});
  });

  describe("fails soft", () => {
    it("on a skin that is not one: the built-in look", () => {
      const set = applyThemeSkin(root, "light", night, "magic");
      const odd = { scheme: "dark", font: "serif" } as unknown as ThemeSkin;
      expect(applyThemeSkin(root, "light", odd, "sea", set)).toEqual([]);
      expect(look(root)).toEqual(builtIn("light"));
    });

    it("on a root that refuses a colour: what was set is taken back", () => {
      const setProperty = root.style.setProperty.bind(root.style);
      vi.spyOn(root.style, "setProperty").mockImplementation((property, value) => {
        if (property === "--surface-2") throw new Error("no");
        setProperty(property, value);
      });
      expect(applyThemeSkin(root, "light", night, "magic")).toEqual([]);
      expect(look(root)).toEqual(builtIn("light"));
    });

    it("on a root that takes nothing at all, without throwing", () => {
      vi.spyOn(root, "setAttribute").mockImplementation(() => {
        throw new Error("no");
      });
      expect(applyThemeSkin(root, "light", night, "magic")).toEqual([]);
      expect(look(root).style).toEqual({});
    });
  });
});

function fakeStorage() {
  const map = new Map<string, string>();
  return {
    getItem: (key: string) => map.get(key) ?? null,
    setItem: (key: string, value: string) => void map.set(key, value),
    removeItem: (key: string) => void map.delete(key),
  };
}

const pack = (id: string): ThemePack => ({
  id,
  name: { en: id },
  version: "1.0.0",
  author: "someone",
  owner: "ann",
  sizeBytes: 2048,
  createdAt: "2026-11-01T00:00:00Z",
  updatedAt: "2026-11-01T00:00:00Z",
  mine: true,
  canDelete: true,
});

const manifest = (id: string, skin?: ThemeSkin) => ({
  schema: 1,
  id,
  name: { en: id },
  version: "1.0.0",
  canvas: [100, 100],
  ...(skin ? { skin } : {}),
});

/** magic is dark and serif, sea is light, plain has no skin. */
const skins: Record<string, ThemeSkin | undefined> = { magic: night, sea: day };

function serve(packs: ThemePack[], defaultId = "") {
  const list = vi.spyOn(api, "listThemePacks").mockResolvedValue({ packs, default: defaultId });
  const read = vi.spyOn(api, "getThemeManifest").mockImplementation(async (id) => manifest(id, skins[id]));
  return { list, read };
}

function pick(value: string) {
  useApp.getState().updateUIPreference("themePack", value);
}

function setTheme(theme: "light" | "dark") {
  useApp.getState().updateUIPreference("theme", theme);
}

describe("useThemeSkin", () => {
  const root = document.documentElement;

  beforeEach(() => {
    configurePreferenceStorage(fakeStorage());
    useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
    setTheme("light");
    useThemePacks.getState().reset();
  });

  afterEach(() => {
    vi.restoreAllMocks();
    root.removeAttribute("data-theme");
    root.removeAttribute("data-theme-font");
    root.removeAttribute("data-theme-pack");
    root.removeAttribute("style");
  });

  it("puts the viewer's own theme on the root with no pack, and follows it", async () => {
    const { list, read } = serve([]);
    renderHook(() => useThemeSkin());
    expect(look(root)).toEqual(builtIn("light"));
    act(() => setTheme("dark"));
    expect(look(root)).toEqual(builtIn("dark"));
    await waitFor(() => expect(useThemePacks.getState().loading).toBe(false));
    expect(list).toHaveBeenCalledOnce();
    expect(read).not.toHaveBeenCalled();
    expect(look(root)).toEqual(builtIn("dark"));
  });

  it("puts the skin of the pack on show over the viewer's theme", async () => {
    serve([pack("magic")]);
    pick("magic");
    renderHook(() => useThemeSkin());
    // Until the pack has answered, the page is the viewer's own.
    expect(look(root)).toEqual(builtIn("light"));
    await waitFor(() => expect(root.getAttribute("data-theme-pack")).toBe("magic"));
    expect(look(root)).toEqual({
      theme: "dark",
      font: "serif",
      pack: "magic",
      style: { "--bg": "#0d0e1c", "--surface": "#15142b", "--surface-2": "#1c1b36" },
    });
  });

  it("keeps the skin's scheme while the viewer changes theme, and gives back the theme they left", async () => {
    serve([pack("sea")]);
    pick("sea");
    renderHook(() => useThemeSkin());
    await waitFor(() => expect(root.getAttribute("data-theme-pack")).toBe("sea"));
    expect(root.getAttribute("data-theme")).toBe("light");

    act(() => setTheme("dark"));
    expect(root.getAttribute("data-theme")).toBe("light");

    act(() => pick("none"));
    expect(look(root)).toEqual(builtIn("dark"));
    act(() => setTheme("light"));
    expect(look(root)).toEqual(builtIn("light"));
  });

  it("changes skin with the pack, leaving nothing of the last one", async () => {
    serve([pack("magic"), pack("sea")], "magic");
    renderHook(() => useThemeSkin());
    await waitFor(() => expect(root.getAttribute("data-theme-pack")).toBe("magic"));

    act(() => pick("sea"));
    await waitFor(() => expect(root.getAttribute("data-theme-pack")).toBe("sea"));
    expect(look(root)).toEqual({
      theme: "light",
      font: null,
      pack: "sea",
      style: { "--bg": "#fdf6e3", "--accent": "#b58900" },
    });
  });

  it("names a pack that has no skin, in the viewer's own theme", async () => {
    serve([pack("magic"), pack("plain")]);
    pick("magic");
    setTheme("dark");
    renderHook(() => useThemeSkin());
    await waitFor(() => expect(root.getAttribute("data-theme-pack")).toBe("magic"));

    act(() => pick("plain"));
    await waitFor(() => expect(root.getAttribute("data-theme-pack")).toBe("plain"));
    expect(look(root)).toEqual({ theme: "dark", font: null, pack: "plain", style: {} });
    act(() => setTheme("light"));
    expect(look(root)).toEqual({ theme: "light", font: null, pack: "plain", style: {} });
  });

  it("takes the skin of a pack replaced by one with another", async () => {
    const { read } = serve([pack("magic")]);
    pick("magic");
    renderHook(() => useThemeSkin());
    await waitFor(() => expect(root.getAttribute("data-theme")).toBe("dark"));

    read.mockImplementation(async (id) => manifest(id, day));
    act(() => useThemePacks.getState().onChanged("magic"));
    await waitFor(() => expect(root.getAttribute("data-theme")).toBe("light"));
    expect(look(root)).toEqual({
      theme: "light",
      font: null,
      pack: "magic",
      style: { "--bg": "#fdf6e3", "--accent": "#b58900" },
    });
  });

  it("goes back to the built-in look when the pack is deleted", async () => {
    const { list } = serve([pack("magic")]);
    pick("magic");
    renderHook(() => useThemeSkin());
    await waitFor(() => expect(root.getAttribute("data-theme-pack")).toBe("magic"));

    list.mockResolvedValue({ packs: [], default: "" });
    act(() => useThemePacks.getState().onChanged("magic"));
    await waitFor(() => expect(root.getAttribute("data-theme-pack")).toBeNull());
    expect(look(root)).toEqual(builtIn("light"));
  });

  it("leaves what else is on the root alone", async () => {
    root.style.setProperty("--font-scale", "1.25");
    serve([pack("magic")]);
    pick("magic");
    renderHook(() => useThemeSkin());
    await waitFor(() => expect(root.getAttribute("data-theme-pack")).toBe("magic"));
    act(() => pick("none"));
    expect(look(root)).toEqual({ ...builtIn("light"), style: { "--font-scale": "1.25" } });
  });

  it("takes the skin with it when it goes, leaving the viewer's theme", async () => {
    serve([pack("magic")]);
    pick("magic");
    const { unmount } = renderHook(() => useThemeSkin());
    await waitFor(() => expect(root.getAttribute("data-theme-pack")).toBe("magic"));
    unmount();
    expect(look(root)).toEqual(builtIn("light"));
  });

  describe("fails soft", () => {
    it("on a list that fails", async () => {
      vi.spyOn(api, "listThemePacks").mockRejectedValue(new Error("down"));
      pick("magic");
      renderHook(() => useThemeSkin());
      await waitFor(() => expect(useThemePacks.getState().error).toBe("down"));
      expect(look(root)).toEqual(builtIn("light"));
    });

    it("on a manifest whose skin is refused", async () => {
      const { read } = serve([pack("magic")]);
      read.mockResolvedValue({ ...manifest("magic"), skin: { scheme: "dark", colors: { bg: "black" } } });
      pick("magic");
      renderHook(() => useThemeSkin());
      await waitFor(() => expect(useThemePacks.getState().manifest?.id).toBe("magic"));
      expect(look(root)).toEqual(builtIn("light"));
    });
  });
});
