/**
 * Undo stack [A-06] — unit tests for the module itself.
 *
 * `stack` in ./undo is module-level state, not store state, so it survives
 * between `it()` blocks in this file unless something clears it. We reset with
 * the module's own `clearUndo()` in `beforeEach` rather than `vi.resetModules()`:
 * `vi.resetModules()` would only give a *fresh copy* of ./undo to a dynamic
 * `import()` done after the reset — the `pushUndo`/`popUndo`/etc. bound at the
 * top of this file would keep pointing at the old copy, so it would not
 * actually isolate anything here. `clearUndo()` empties the one real module
 * instance, which is what every call site (graphSlice, runSlice, undoSlice)
 * also shares, so it is the reset that matches production behaviour.
 *
 * Integration coverage — a graph command pushing an entry and undo restoring
 * the previous graph, a failed revert putting the entry back, `recordUndo:
 * false` on the compensating write — already exists end-to-end in
 * graphSlice.test.ts ("undo" describe block) and runSlice.test.ts ("journal
 * undo" describe block). This file covers the stack primitives those depend
 * on, including the trimming and labelling rules that are otherwise untested.
 */

import { beforeEach, describe, expect, it } from "vitest";
import {
  clearRedo,
  clearUndo,
  MAX_UNDO_ENTRIES,
  observeUndo,
  peekRedo,
  peekUndo,
  popRedo,
  popUndo,
  pushRedo,
  pushUndo,
  redoDepth,
  undoDepth,
  undoEntryLabel,
  type UndoEntry,
} from "./undo";

beforeEach(() => {
  clearUndo();
  observeUndo(null);
});

describe("push / pop / peek", () => {
  it("starts empty", () => {
    expect(undoDepth()).toBe(0);
    expect(peekUndo()).toBeUndefined();
  });

  it("pushes onto the top and peek does not consume it", () => {
    pushUndo({ kind: "create-node", ids: ["a"] });
    pushUndo({ kind: "create-node", ids: ["b"] });

    expect(undoDepth()).toBe(2);
    expect(peekUndo()).toMatchObject({ ids: ["b"] });
    expect(undoDepth()).toBe(2); // peek is read-only
  });

  it("pops the most recent entry first (LIFO)", () => {
    pushUndo({ kind: "create-node", ids: ["a"] });
    pushUndo({ kind: "create-node", ids: ["b"] });

    expect(popUndo()).toMatchObject({ ids: ["b"] });
    expect(popUndo()).toMatchObject({ ids: ["a"] });
    expect(popUndo()).toBeUndefined();
    expect(undoDepth()).toBe(0);
  });
});

describe("clearUndo", () => {
  // README rule 5: an old project's snapshot must never be applied to a new
  // one, so switching drops whatever was on the stack unconditionally.
  it("empties the stack regardless of depth", () => {
    pushUndo({ kind: "create-node", ids: ["a"] });
    pushUndo({ kind: "create-node", ids: ["b"] });
    pushUndo({ kind: "create-node", ids: ["c"] });

    clearUndo();

    expect(undoDepth()).toBe(0);
    expect(peekUndo()).toBeUndefined();
  });
});

describe("redo stack", () => {
  it("is LIFO and independent of the undo stack", () => {
    pushRedo({ kind: "create-node", ids: ["a"] });
    pushRedo({ kind: "create-node", ids: ["b"] });

    expect(redoDepth()).toBe(2);
    expect(undoDepth()).toBe(0);
    expect(peekRedo()).toMatchObject({ ids: ["b"] });
    expect(popRedo()).toMatchObject({ ids: ["b"] });
    expect(popRedo()).toMatchObject({ ids: ["a"] });
    expect(popRedo()).toBeUndefined();
  });

  // A fresh edit branches away from the state those entries describe, so they
  // are no longer a future anyone can get to.
  it("is dropped by a normal pushUndo and kept by keepRedo", () => {
    pushRedo({ kind: "create-node", ids: ["a"] });
    pushUndo({ kind: "create-node", ids: ["b"] }, { keepRedo: true });
    expect(redoDepth()).toBe(1);

    pushUndo({ kind: "create-node", ids: ["c"] });
    expect(redoDepth()).toBe(0);
  });

  it("empties on clearRedo, leaving the undo stack alone", () => {
    pushUndo({ kind: "create-node", ids: ["a"] }, { keepRedo: true });
    pushRedo({ kind: "create-node", ids: ["b"] });

    clearRedo();

    expect(redoDepth()).toBe(0);
    expect(undoDepth()).toBe(1);
  });

  // Rule 5: a project switch must not leave a redo entry pointing at the old
  // project's nodes any more than an undo one.
  it("is emptied by clearUndo together with the undo stack", () => {
    pushUndo({ kind: "create-node", ids: ["a"] }, { keepRedo: true });
    pushRedo({ kind: "create-node", ids: ["b"] });

    clearUndo();

    expect(undoDepth()).toBe(0);
    expect(redoDepth()).toBe(0);
  });

  it("drops the oldest entries once the cap is exceeded", () => {
    for (let i = 0; i < MAX_UNDO_ENTRIES + 1; i++) {
      pushRedo({ kind: "create-node", ids: [String(i)] });
    }
    expect(redoDepth()).toBe(MAX_UNDO_ENTRIES);
  });

  it("notifies the observer on push, pop and clear", () => {
    let calls = 0;
    observeUndo(() => {
      calls++;
    });

    pushRedo({ kind: "create-node", ids: ["a"] });
    popRedo();
    pushRedo({ kind: "create-node", ids: ["b"] });
    clearRedo();

    expect(calls).toBe(4);
  });
});

describe("MAX_UNDO_ENTRIES", () => {
  it("drops the oldest entries once the cap is exceeded, keeping the newest", () => {
    for (let i = 0; i < MAX_UNDO_ENTRIES + 1; i++) {
      pushUndo({ kind: "create-node", ids: [String(i)] });
    }

    expect(undoDepth()).toBe(MAX_UNDO_ENTRIES);
    // Entry "0" was the oldest and should have been evicted; "1" survives as
    // the new bottom of the stack.
    const popped: string[] = [];
    let entry: UndoEntry | undefined;
    while ((entry = popUndo())) {
      if (entry.kind === "create-node") popped.push(...entry.ids);
    }
    expect(popped[0]).toBe(String(MAX_UNDO_ENTRIES));
    expect(popped.at(-1)).toBe("1");
    expect(popped).not.toContain("0");
  });
});

describe("observeUndo", () => {
  it("notifies on push and pop", () => {
    let calls = 0;
    observeUndo(() => {
      calls++;
    });

    pushUndo({ kind: "create-node", ids: ["a"] });
    expect(calls).toBe(1);

    popUndo();
    expect(calls).toBe(2);
  });

  it("notifies on clear", () => {
    pushUndo({ kind: "create-node", ids: ["a"] });
    let calls = 0;
    observeUndo(() => {
      calls++;
    });

    clearUndo();
    expect(calls).toBe(1);
  });

  it("stops notifying once the listener is replaced with null", () => {
    let calls = 0;
    observeUndo(() => {
      calls++;
    });
    observeUndo(null);

    pushUndo({ kind: "create-node", ids: ["a"] });
    expect(calls).toBe(0);
  });
});

describe("undoEntryLabel", () => {
  it("returns null for an empty stack", () => {
    expect(undoEntryLabel(undefined)).toBeNull();
  });

  it("prefers the entry's own label over the kind default", () => {
    expect(
      undoEntryLabel({ kind: "graph", graph: { version: 1, nodes: [] }, label: "自訂標籤" }),
    ).toBe("自訂標籤");
  });

  // Every UndoEntry kind has a default label the UI falls back to, since a
  // command that omits `label` must still describe itself in the undo button.
  it.each([
    ["graph", "圖譜變更"],
    ["create-node", "新增節點"],
    ["delete-node", "刪除節點"],
    ["lifecycle-status", "實際狀態變更"],
    ["lifecycle-move", "實際時間調整"],
  ] as const)("falls back to the %s default when no label is given", (kind, expected) => {
    const byKind: Record<string, UndoEntry> = {
      graph: { kind: "graph", graph: { version: 1, nodes: [] } },
      "create-node": { kind: "create-node", ids: ["a"] },
      "delete-node": { kind: "delete-node", graph: { version: 1, nodes: [] }, trashFiles: [] },
      "lifecycle-status": { kind: "lifecycle-status", nodeId: "a", previousStatus: "ready" },
      "lifecycle-move": { kind: "lifecycle-move", eventId: "ev-1", previousTimestamp: "t" },
    };
    expect(undoEntryLabel(byKind[kind])).toBe(expected);
  });
});

// README rule 3: `canUndo` must never be optimistic. The store only derives it
// from `undoDepth() > 0` inside the `onChange` callback below, so as long as
// depth only changes through push/pop/clear (asserted above), a UI that mirrors
// it via observeUndo can never show the affordance for a step that was not
// actually recorded.
describe("canUndo cannot go optimistic", () => {
  it("reflects only what was actually pushed, never a pending intent", () => {
    expect(undoDepth() > 0).toBe(false);
    // Merely constructing an entry, without pushing it, must not affect depth.
    const notPushed: UndoEntry = { kind: "create-node", ids: ["a"] };
    void notPushed;
    expect(undoDepth() > 0).toBe(false);

    pushUndo({ kind: "create-node", ids: ["a"] });
    expect(undoDepth() > 0).toBe(true);
  });
});
