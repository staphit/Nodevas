import { act, renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it } from "vitest";

import { readPreference, readPreferences } from "../preferences";
import { useApp } from "../store";
import { CHARACTER_STYLES, characterStyleOf, DEFAULT_CHARACTER_STYLE, useCharacterStyle } from "./characterStyle";

const KEY = "vised-character-style";

beforeEach(() => {
  useApp.setState({ preferences: readPreferences() });
});

describe("the character style", () => {
  it("is pixel or anime, anime until the viewer picks", () => {
    expect(CHARACTER_STYLES).toEqual(["pixel", "anime"]);
    expect(DEFAULT_CHARACTER_STYLE).toBe("anime");
    expect(readPreference("characterStyle")).toBe("anime");
  });

  it("keeps the viewer's pick in this browser", () => {
    localStorage.setItem(KEY, "pixel");
    expect(readPreference("characterStyle")).toBe("pixel");
    localStorage.setItem(KEY, "anime");
    expect(readPreference("characterStyle")).toBe("anime");
  });

  it("reads anything else as the default, anime", () => {
    localStorage.setItem(KEY, "manga");
    expect(readPreference("characterStyle")).toBe("anime");
    expect(characterStyleOf(undefined)).toBe("anime");
    expect(characterStyleOf("Pixel")).toBe("anime");
    expect(characterStyleOf(3)).toBe("anime");
    expect(characterStyleOf("pixel")).toBe("pixel");
    expect(characterStyleOf("anime")).toBe("anime");
  });

  it("is read and set through one hook, which redraws on a change", () => {
    const { result } = renderHook(() => useCharacterStyle());
    expect(result.current[0]).toBe("anime");
    act(() => result.current[1]("pixel"));
    expect(result.current[0]).toBe("pixel");
    expect(localStorage.getItem(KEY)).toBe("pixel");
    expect(useApp.getState().preferences.characterStyle).toBe("pixel");
    act(() => result.current[1]("anime"));
    expect(result.current[0]).toBe("anime");
    expect(localStorage.getItem(KEY)).toBe("anime");
  });

  it("reads a store that never heard of it as the default", () => {
    const { characterStyle: _gone, ...older } = useApp.getState().preferences;
    useApp.setState({ preferences: older as never });
    expect(renderHook(() => useCharacterStyle()).result.current[0]).toBe("anime");
  });
});
