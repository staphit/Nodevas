import { act, renderHook } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { useNarrowViewport } from "./TopbarOverflow";

/**
 * jsdom's matchMedia always reports "no match" and has no listener plumbing, so
 * the breakpoint is faked here, with a handle to fire its change event.
 */
function mockViewport(narrow: boolean) {
  const listeners = new Set<(event: MediaQueryListEvent) => void>();
  vi.stubGlobal(
    "matchMedia",
    vi.fn((query: string) => ({
      matches: narrow,
      media: query,
      onchange: null,
      addEventListener: (_: string, listener: (event: MediaQueryListEvent) => void) => listeners.add(listener),
      removeEventListener: (_: string, listener: (event: MediaQueryListEvent) => void) => listeners.delete(listener),
      addListener: vi.fn(),
      removeListener: vi.fn(),
      dispatchEvent: vi.fn(),
    })),
  );
  return (matches: boolean) => {
    for (const listener of listeners) listener({ matches } as MediaQueryListEvent);
  };
}

describe("useNarrowViewport", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("reads the phone breakpoint", () => {
    mockViewport(true);
    expect(renderHook(() => useNarrowViewport()).result.current).toBe(true);
  });

  it("is false on a wide window", () => {
    mockViewport(false);
    expect(renderHook(() => useNarrowViewport()).result.current).toBe(false);
  });

  // A desktop window is resized across the breakpoint mid-session.
  it("follows the width as it changes", () => {
    const resize = mockViewport(false);
    const { result } = renderHook(() => useNarrowViewport());
    act(() => resize(true));
    expect(result.current).toBe(true);
    act(() => resize(false));
    expect(result.current).toBe(false);
  });
});
