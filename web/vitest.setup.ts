import "@testing-library/jest-dom/vitest";
import { cleanup } from "@testing-library/react";
import { afterEach, beforeEach } from "vitest";
import { resetCoalescing } from "./src/state/coalesce";

// jsdom has no layout engine and therefore no ResizeObserver. Components use it
// to track available space; in tests there is no space to track, so a no-op
// observer is the honest stand-in.
if (!("ResizeObserver" in globalThis)) {
  class NoopResizeObserver implements ResizeObserver {
    observe(): void {}
    unobserve(): void {}
    disconnect(): void {}
  }
  globalThis.ResizeObserver = NoopResizeObserver;
}

// jsdom 26 gates Web Storage behind a non-opaque document origin, and vitest's
// default document has one, so `localStorage` comes through undefined. Tests
// exercise preference persistence, so an in-memory stand-in is the honest
// substitute — same idea as the ResizeObserver shim above.
if (typeof globalThis.localStorage === "undefined") {
  class MemoryStorage implements Storage {
    private store = new Map<string, string>();
    get length(): number {
      return this.store.size;
    }
    clear(): void {
      this.store.clear();
    }
    getItem(key: string): string | null {
      return this.store.has(key) ? (this.store.get(key) as string) : null;
    }
    key(index: number): string | null {
      return Array.from(this.store.keys())[index] ?? null;
    }
    removeItem(key: string): void {
      this.store.delete(key);
    }
    setItem(key: string, value: string): void {
      this.store.set(key, String(value));
    }
  }
  const storage = new MemoryStorage();
  Object.defineProperty(globalThis, "localStorage", {
    value: storage,
    configurable: true,
  });
  if (typeof window !== "undefined") {
    Object.defineProperty(window, "localStorage", {
      value: storage,
      configurable: true,
    });
  }
}

// jsdom keeps localStorage between tests; preferences must start from defaults.
beforeEach(() => {
  localStorage.clear();
});

// Refetch coalescing lives in module state and holds timers, so a test that
// leaves a trigger gathered would otherwise fire it during the next one.
afterEach(() => {
  resetCoalescing();
});

afterEach(() => {
  cleanup();
});
