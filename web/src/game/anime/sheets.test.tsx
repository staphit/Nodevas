import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { readPreferences } from "../../preferences";
import { useApp } from "../../store";
import fixture from "./fixture.json";
import { loadSheet, resetAnimeSheets, RETRY_FIRST_MS, useAnimeSheet, type SheetSource } from "./sheets";

const IMAGE = "/assets/fixture-1a2b3c.webp";

function source(over: Partial<SheetSource> = {}): SheetSource {
  return {
    manifests: { "./sheets/gpt.json": async () => fixture },
    images: { "./sheets/fixture.webp": IMAGE },
    ...over,
  };
}
/** The fixture's image loads, as big as its manifest says: 8 frames of 192
 * by 4 rows. */
const loads = async () => ({ width: 1536, height: 768 });

beforeEach(() => {
  useApp.setState({ preferences: readPreferences() });
});

afterEach(() => {
  resetAnimeSheets();
});

describe("loadSheet", () => {
  it("loads a sheet whose manifest reads and whose image loads", async () => {
    const decode = vi.fn(loads);
    const sheet = await loadSheet("gpt", source(), decode);
    expect(sheet).toMatchObject({ id: "gpt", href: IMAGE, width: 1536, height: 768 });
    expect(Object.keys(sheet!.manifest.actions)).toEqual(["door-idle", "desk-type", "hike-walk", "cycle-ride"]);
    expect(decode).toHaveBeenCalledWith(IMAGE);
  });

  it.each<[string, Partial<SheetSource>, () => Promise<{ width: number; height: number } | undefined>]>([
    ["no manifest", { manifests: {} }, loads],
    ["a manifest that fails to load", { manifests: { "./sheets/gpt.json": () => Promise.reject(new Error("gone")) } }, loads],
    ["a manifest that does not read", { manifests: { "./sheets/gpt.json": async () => ({ style: "anime" }) } }, loads],
    ["no image", { images: {} }, loads],
    ["an image that does not load", {}, async () => undefined],
    ["an image that throws", {}, () => Promise.reject(new Error("decode"))],
    // Out of step with its manifest (a row short, or wider): drawn, every row would be off.
    ["an image a row short of its manifest", {}, async () => ({ width: 1536, height: 576 })],
    ["an image wider than its manifest", {}, async () => ({ width: 1728, height: 768 })],
  ])("has none, so the character stays pixel art, with %s", async (_, over, decode) => {
    expect(await loadSheet("gpt", source(over), decode)).toBeUndefined();
  });
});

describe("useAnimeSheet", () => {
  const pick = (style: "pixel" | "anime") => act(() => useApp.getState().updateUIPreference("characterStyle", style));

  it("loads nothing while the viewer draws pixel characters", () => {
    pick("pixel");
    const manifest = vi.fn(async () => fixture);
    resetAnimeSheets({ source: source({ manifests: { "./sheets/gpt.json": manifest } }), decode: loads });
    const { result } = renderHook(() => useAnimeSheet("gpt"));
    expect(result.current).toBeUndefined();
    expect(manifest).not.toHaveBeenCalled();
  });

  it("loads the sheets for a viewer who has not picked, anime being the default", async () => {
    const manifest = vi.fn(async () => fixture);
    resetAnimeSheets({ source: source({ manifests: { "./sheets/gpt.json": manifest } }), decode: loads });
    const { result } = renderHook(() => useAnimeSheet("gpt"));
    await waitFor(() => expect(result.current).toMatchObject({ id: "gpt", href: IMAGE }));
    expect(manifest).toHaveBeenCalledTimes(1);
  });

  it("loads only the sheets of the characters it draws, and none for the person", async () => {
    const gpt = vi.fn(async () => fixture);
    const claude = vi.fn(async () => ({ ...fixture, character: "claude" }));
    resetAnimeSheets({
      source: source({ manifests: { "./sheets/gpt.json": gpt, "./sheets/claude.json": claude } }),
      decode: loads,
    });
    const { result } = renderHook(() => useAnimeSheet("gpt"));
    renderHook(() => useAnimeSheet("human"));
    await waitFor(() => expect(result.current).toMatchObject({ id: "gpt" }));
    expect(gpt).toHaveBeenCalledTimes(1);
    expect(claude).not.toHaveBeenCalled();
  });

  it("loads the sheets once anime is picked, and draws from the character's own", async () => {
    pick("pixel");
    const gpt = vi.fn(async () => fixture);
    const claude = vi.fn(async () => ({ ...fixture, character: "claude" }));
    resetAnimeSheets({
      source: source({ manifests: { "./sheets/gpt.json": gpt, "./sheets/claude.json": claude } }),
      decode: loads,
    });
    const { result, rerender } = renderHook(({ character }) => useAnimeSheet(character), {
      initialProps: { character: "gpt" as const } as { character: "gpt" | "claude" | "star" | "human" },
    });
    act(() => useApp.getState().updateUIPreference("characterStyle", "anime"));
    await waitFor(() => expect(result.current).toMatchObject({ id: "gpt", href: IMAGE }));
    rerender({ character: "claude" });
    await waitFor(() => expect(result.current).toMatchObject({ id: "claude" }));
    // Gemini's sheet is not there; the person never has one.
    rerender({ character: "star" });
    expect(result.current).toBeUndefined();
    rerender({ character: "human" });
    expect(result.current).toBeUndefined();
    // Once each, however many characters ask.
    renderHook(() => useAnimeSheet("gpt"));
    await act(async () => undefined);
    expect(gpt).toHaveBeenCalledTimes(1);
    expect(claude).toHaveBeenCalledTimes(1);
  });

  it("tries a sheet that could not be fetched again a while later, for as long as it is drawn", async () => {
    vi.useFakeTimers();
    try {
      let online = false;
      const manifest = vi.fn(async () => {
        if (!online) throw new Error("offline");
        return fixture;
      });
      const decode = vi.fn(loads);
      resetAnimeSheets({ source: source({ manifests: { "./sheets/gpt.json": manifest } }), decode });
      const { result } = renderHook(() => useAnimeSheet("gpt"));
      await act(async () => undefined);
      expect(manifest).toHaveBeenCalledTimes(1);
      expect(result.current).toBeUndefined();
      // Still failing a while later: again, then twice as long until the next try.
      await act(async () => void (await vi.advanceTimersByTimeAsync(RETRY_FIRST_MS)));
      expect(manifest).toHaveBeenCalledTimes(2);
      await act(async () => void (await vi.advanceTimersByTimeAsync(RETRY_FIRST_MS)));
      expect(manifest).toHaveBeenCalledTimes(2);
      online = true;
      await act(async () => void (await vi.advanceTimersByTimeAsync(RETRY_FIRST_MS)));
      expect(manifest).toHaveBeenCalledTimes(3);
      expect(result.current).toMatchObject({ id: "gpt", href: IMAGE });
      // Loaded: never again.
      await act(async () => void (await vi.advanceTimersByTimeAsync(10 * RETRY_FIRST_MS)));
      expect(manifest).toHaveBeenCalledTimes(3);
    } finally {
      vi.useRealTimers();
    }
  });

  it("waits for the character to be drawn again before trying once nobody draws it", async () => {
    vi.useFakeTimers();
    try {
      const manifest = vi.fn(() => Promise.reject(new Error("offline")));
      resetAnimeSheets({ source: source({ manifests: { "./sheets/gpt.json": manifest } }), decode: loads });
      const first = renderHook(() => useAnimeSheet("gpt"));
      await act(async () => undefined);
      first.unmount();
      await act(async () => void (await vi.advanceTimersByTimeAsync(10 * RETRY_FIRST_MS)));
      expect(manifest).toHaveBeenCalledTimes(1);
      renderHook(() => useAnimeSheet("gpt"));
      await act(async () => undefined);
      expect(manifest).toHaveBeenCalledTimes(2);
    } finally {
      vi.useRealTimers();
    }
  });

  it("tries an image that did not load again, but never a sheet that is not there or does not match", async () => {
    vi.useFakeTimers();
    try {
      const decode = vi.fn(async () => undefined);
      resetAnimeSheets({ source: source(), decode });
      renderHook(() => useAnimeSheet("gpt"));
      await act(async () => undefined);
      expect(decode).toHaveBeenCalledTimes(1);
      await act(async () => void (await vi.advanceTimersByTimeAsync(RETRY_FIRST_MS)));
      expect(decode).toHaveBeenCalledTimes(2);
      // Claude has no sheet in this source; GPT's is a row short.
      const manifest = vi.fn(async () => fixture);
      const short = vi.fn(async () => ({ width: 1536, height: 576 }));
      resetAnimeSheets({ source: source({ manifests: { "./sheets/gpt.json": manifest } }), decode: short });
      const gpt = renderHook(() => useAnimeSheet("gpt"));
      const claude = renderHook(() => useAnimeSheet("claude"));
      await act(async () => void (await vi.advanceTimersByTimeAsync(20 * RETRY_FIRST_MS)));
      expect(manifest).toHaveBeenCalledTimes(1);
      expect(short).toHaveBeenCalledTimes(1);
      expect(gpt.result.current).toBeUndefined();
      expect(claude.result.current).toBeUndefined();
    } finally {
      vi.useRealTimers();
    }
  });

  it("draws pixel characters again, the sheets kept, when pixel is picked back", async () => {
    resetAnimeSheets({ source: source(), decode: loads });
    const { result } = renderHook(() => useAnimeSheet("gpt"));
    act(() => useApp.getState().updateUIPreference("characterStyle", "anime"));
    await waitFor(() => expect(result.current).toBeDefined());
    act(() => useApp.getState().updateUIPreference("characterStyle", "pixel"));
    expect(result.current).toBeUndefined();
  });
});
