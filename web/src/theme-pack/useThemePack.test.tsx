import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api, type ThemePack } from "../api";
import { configurePreferenceStorage, readPreferences } from "../preferences";
import { resyncAfterReconnect } from "../state/reconnect";
import { useApp } from "../store";
import { activeThemePack, useThemePacks } from "./themePackStore";
import { useThemePack } from "./useThemePack";

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

const manifest = (id: string, version = "1.0.0") => ({
  schema: 1,
  id,
  name: { en: id },
  version,
  author: "someone",
  canvas: [100, 100],
  rooms: { lounge: "rooms/lounge.webp" },
  characters: {},
});

function pick(value: string) {
  useApp.getState().updateUIPreference("themePack", value);
}

beforeEach(() => {
  configurePreferenceStorage(fakeStorage());
  useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
  useThemePacks.getState().reset();
});

afterEach(() => vi.restoreAllMocks());

function serve(packs: ThemePack[], defaultId = "") {
  const list = vi.spyOn(api, "listThemePacks").mockResolvedValue({ packs, default: defaultId });
  const read = vi.spyOn(api, "getThemeManifest").mockImplementation(async (id) => manifest(id));
  return { list, read };
}

describe("the active pack", () => {
  const packs = [pack("magic"), pack("sea")];

  it.each<[string, string, string, string | undefined]>([
    ["nothing picked, no default: the built-in people", "", "", undefined],
    ["nothing picked: the site default", "", "sea", "sea"],
    ["a pack picked: that pack, over the default", "magic", "sea", "magic"],
    ["none picked: the built-in people, over the default", "none", "sea", undefined],
    ["a picked pack that is gone: the site default", "gone", "sea", "sea"],
    ["a picked pack that is gone, no default: the built-in people", "gone", "", undefined],
    ["a default that is gone: the built-in people", "", "gone", undefined],
  ])("%s", (_name, picked, defaultId, expected) => {
    expect(activeThemePack(packs, defaultId, picked)?.id).toBe(expected);
  });
});

describe("useThemePack", () => {
  it("gives undefined with no pack, and asks for no manifest", async () => {
    const { list, read } = serve([]);
    const { result } = renderHook(() => useThemePack());
    await waitFor(() => expect(list).toHaveBeenCalled());
    expect(result.current).toBeUndefined();
    expect(read).not.toHaveBeenCalled();
  });

  it("gives the picked pack and its manifest", async () => {
    serve([pack("magic")]);
    pick("magic");
    const { result } = renderHook(() => useThemePack());
    await waitFor(() => expect(result.current).toBeDefined());
    expect(result.current?.pack.id).toBe("magic");
    expect(result.current?.manifest.rooms).toEqual({ lounge: "rooms/lounge.webp" });
  });

  it("follows the site default until a pack is picked, and shows the built-in people on none", async () => {
    serve([pack("magic"), pack("sea")], "sea");
    const { result } = renderHook(() => useThemePack());
    await waitFor(() => expect(result.current?.pack.id).toBe("sea"));
    act(() => pick("magic"));
    await waitFor(() => expect(result.current?.pack.id).toBe("magic"));
    act(() => pick("none"));
    expect(result.current).toBeUndefined();
  });

  it("asks once when several read it", async () => {
    const { list, read } = serve([pack("magic")]);
    pick("magic");
    const { result } = renderHook(() => [useThemePack(), useThemePack(), useThemePack()]);
    await waitFor(() => expect(result.current[0]).toBeDefined());
    expect(list).toHaveBeenCalledOnce();
    expect(read).toHaveBeenCalledOnce();
  });

  describe("imageUrl", () => {
    async function imageUrl() {
      serve([pack("magic")]);
      pick("magic");
      const { result } = renderHook(() => useThemePack());
      await waitFor(() => expect(result.current).toBeDefined());
      return result.current!.imageUrl;
    }

    it("encodes each segment of the path and keeps its slashes", async () => {
      const url = await imageUrl();
      expect(url("rooms/lounge.webp")).toBe("/api/themes/magic/files/rooms/lounge.webp");
      expect(url("a b/c#d?e.webp")).toBe("/api/themes/magic/files/a%20b/c%23d%3Fe.webp");
      expect(url("魔法/1%2e.webp")).toBe(`/api/themes/magic/files/${encodeURIComponent("魔法")}/1%252e.webp`);
    });

    // The browser would resolve the dots away, out of the pack.
    it.each(["../../projects/x.webp", "a/../b.webp", "./a.webp", "/a.webp", "a//b.webp", "a\\b.webp", ""])(
      "gives no URL for %j",
      async (path) => {
        const url = await imageUrl();
        expect(url(path)).toBe("");
      },
    );

    it("gives no URL, rather than throwing, for text that cannot be encoded", async () => {
      const url = await imageUrl();
      expect(url("\uD800.webp")).toBe("");
    });
  });

  describe("fails soft", () => {
    it("on a list that fails", async () => {
      const list = vi.spyOn(api, "listThemePacks").mockRejectedValue(new Error("down"));
      const read = vi.spyOn(api, "getThemeManifest");
      pick("magic");
      const { result } = renderHook(() => useThemePack());
      await waitFor(() => expect(list).toHaveBeenCalled());
      await waitFor(() => expect(useThemePacks.getState().error).toBe("down"));
      expect(result.current).toBeUndefined();
      expect(read).not.toHaveBeenCalled();
    });

    it("on a reload that fails, with a pack already on show", async () => {
      const { list } = serve([pack("magic")]);
      pick("magic");
      const { result } = renderHook(() => useThemePack());
      await waitFor(() => expect(result.current).toBeDefined());

      list.mockRejectedValue(new Error("down"));
      act(() => useThemePacks.getState().onChanged("sea"));
      await waitFor(() => expect(result.current).toBeUndefined());
    });

    it("on a manifest that fails, without asking again and again", async () => {
      const { read } = serve([pack("magic")]);
      read.mockRejectedValue(new Error("404"));
      pick("magic");
      const { result, rerender } = renderHook(() => useThemePack());
      await waitFor(() => expect(useThemePacks.getState().manifest?.id).toBe("magic"));
      rerender();
      expect(result.current).toBeUndefined();
      expect(read).toHaveBeenCalledOnce();
    });

    it.each<[string, unknown]>([
      ["of the wrong shape", { schema: 2 }],
      ["that is no object", "<html>"],
      ["for another pack", manifest("sea")],
    ])("on a manifest %s", async (_name, answer) => {
      const { read } = serve([pack("magic")]);
      read.mockResolvedValue(answer);
      pick("magic");
      const { result } = renderHook(() => useThemePack());
      await waitFor(() => expect(useThemePacks.getState().manifest?.id).toBe("magic"));
      expect(result.current).toBeUndefined();
    });

    it("on a list that is not one (a nil list from Go is null)", async () => {
      vi.spyOn(api, "listThemePacks").mockResolvedValue({ packs: null, default: 7 } as never);
      pick("magic");
      const { result } = renderHook(() => useThemePack());
      await waitFor(() => expect(useThemePacks.getState().loading).toBe(false));
      expect(useThemePacks.getState().packs).toEqual([]);
      expect(useThemePacks.getState().defaultId).toBe("");
      expect(result.current).toBeUndefined();
    });

    it("recovers once a later reload works", async () => {
      const list = vi.spyOn(api, "listThemePacks").mockRejectedValueOnce(new Error("down"));
      list.mockResolvedValue({ packs: [pack("magic")], default: "" });
      vi.spyOn(api, "getThemeManifest").mockImplementation(async (id) => manifest(id));
      pick("magic");
      const { result } = renderHook(() => useThemePack());
      await waitFor(() => expect(useThemePacks.getState().error).toBe("down"));
      act(() => useThemePacks.getState().onChanged());
      await waitFor(() => expect(result.current?.pack.id).toBe("magic"));
    });
  });

  describe("on themes-changed", () => {
    it("does nothing while nothing has asked for packs", async () => {
      const { list } = serve([]);
      useThemePacks.getState().onChanged("magic");
      await Promise.resolve();
      expect(list).not.toHaveBeenCalled();
    });

    it("reloads the manifest of a pack replaced", async () => {
      const { read } = serve([pack("magic")]);
      pick("magic");
      const { result } = renderHook(() => useThemePack());
      await waitFor(() => expect(result.current?.manifest.version).toBe("1.0.0"));

      read.mockImplementation(async (id) => manifest(id, "2.0.0"));
      act(() => useThemePacks.getState().onChanged("magic"));
      await waitFor(() => expect(result.current?.manifest.version).toBe("2.0.0"));
    });

    it("keeps showing the pack when another one changes, asking only for the list", async () => {
      const { list, read } = serve([pack("magic")]);
      pick("magic");
      const { result } = renderHook(() => useThemePack());
      await waitFor(() => expect(result.current).toBeDefined());
      const seen: unknown[] = [];
      const stop = useThemePacks.subscribe((state) => seen.push(state.manifest));
      list.mockClear();

      act(() => useThemePacks.getState().onChanged("sea"));
      await waitFor(() => expect(list).toHaveBeenCalledOnce());
      await waitFor(() => expect(useThemePacks.getState().loading).toBe(false));
      stop();
      expect(read).toHaveBeenCalledOnce();
      expect(seen.every(Boolean)).toBe(true);
      expect(result.current?.pack.id).toBe("magic");
    });

    // Whatever was broadcast while the socket was down is gone.
    it("reloads list and manifest after a reconnect", async () => {
      useApp.setState({
        loadAll: vi.fn(async () => undefined),
        refreshState: vi.fn(async () => undefined),
        refreshTrash: vi.fn(async () => undefined),
        refreshProjects: vi.fn(async () => undefined),
      });
      const { read } = serve([pack("magic")]);
      pick("magic");
      const { result } = renderHook(() => useThemePack());
      await waitFor(() => expect(result.current?.manifest.version).toBe("1.0.0"));

      read.mockImplementation(async (id) => manifest(id, "2.0.0"));
      await act(() => resyncAfterReconnect());
      await waitFor(() => expect(result.current?.manifest.version).toBe("2.0.0"));
    });

    it("falls back when the active pack is deleted", async () => {
      const { list } = serve([pack("magic"), pack("sea")], "sea");
      pick("magic");
      const { result } = renderHook(() => useThemePack());
      await waitFor(() => expect(result.current?.pack.id).toBe("magic"));

      list.mockResolvedValue({ packs: [pack("sea")], default: "sea" });
      act(() => useThemePacks.getState().onChanged("magic"));
      await waitFor(() => expect(result.current?.pack.id).toBe("sea"));

      list.mockResolvedValue({ packs: [], default: "" });
      act(() => useThemePacks.getState().onChanged("sea"));
      await waitFor(() => expect(result.current).toBeUndefined());
    });

    it("lets a slow answer for an old pick land under the newer one", async () => {
      const { read } = serve([pack("magic"), pack("sea")]);
      let answerMagic: (value: unknown) => void = () => {};
      read.mockImplementation((id) =>
        id === "magic" ? new Promise((resolve) => (answerMagic = resolve)) : Promise.resolve(manifest(id)),
      );
      pick("magic");
      const { result } = renderHook(() => useThemePack());
      await waitFor(() => expect(read).toHaveBeenCalledWith("magic"));

      act(() => pick("sea"));
      await waitFor(() => expect(result.current?.pack.id).toBe("sea"));
      await act(async () => answerMagic(manifest("magic")));
      expect(result.current?.pack.id).toBe("sea");
      expect(result.current?.manifest.id).toBe("sea");
    });
  });
});
