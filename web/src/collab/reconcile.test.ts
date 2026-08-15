import { describe, expect, it } from "vitest";
import * as Y from "yjs";

import { reconcileText } from "./reconcile";

const TEXT_KEY = "text";

interface Peer {
  doc: Y.Doc;
  text: Y.Text;
  /** Updates this document produced, i.e. what would go on the wire. */
  updates: Uint8Array[];
}

function join(): Peer {
  const doc = new Y.Doc();
  const peer: Peer = { doc, text: doc.getText(TEXT_KEY), updates: [] };
  doc.on("update", (update: Uint8Array) => {
    peer.updates.push(update);
  });
  return peer;
}

/** Hands everything `from` knows that `to` does not to `to`, as the hub would. */
function sync(from: Peer, to: Peer): void {
  Y.applyUpdate(to.doc, Y.encodeStateAsUpdate(from.doc, Y.encodeStateVector(to.doc)));
}

function pair(seed: string): [Peer, Peer] {
  const a = join();
  const b = join();
  a.doc.transact(() => a.text.insert(0, seed));
  sync(a, b);
  a.updates.length = 0;
  b.updates.length = 0;
  return [a, b];
}

/** A half of a surrogate pair standing on its own; the corruption to watch for. */
const LONE_SURROGATE = /[\uD800-\uDBFF](?![\uDC00-\uDFFF])|(?<![\uD800-\uDBFF])[\uDC00-\uDFFF]/;

const DOCUMENT = ["# Title", "", "one", "two", "three", "four", ""].join("\n");

/** Long enough that a rewrite of it is past the guard's absolute floor. */
const CHAPTER = Array.from({ length: 30 }, (_, i) => `paragraph ${i} of the chapter`).join("\n");

describe("reconcileText", () => {
  it("merges an external edit at the top while the peer is editing lower down", () => {
    const [a, b] = pair(DOCUMENT);

    // The other person is mid-word further down and has not been relayed yet.
    b.doc.transact(() => b.text.insert(DOCUMENT.indexOf("four") + 4, "teen"));

    const result = reconcileText(a.text, DOCUMENT.replace("# Title", "# Better title"));
    expect(result.outcome).toBe("merged");

    sync(a, b);
    sync(b, a);
    expect(a.text.toString()).toBe(b.text.toString());
    expect(a.text.toString()).toContain("# Better title");
    expect(a.text.toString()).toContain("fourteen");
  });

  it("leaves the line between two changed ones alone, edit and all", () => {
    const [a, b] = pair(DOCUMENT);
    b.doc.transact(() => b.text.insert(DOCUMENT.indexOf("two") + 3, " AND A HALF"));

    reconcileText(a.text, DOCUMENT.replace("one", "ONE").replace("four", "FOUR"));

    sync(a, b);
    sync(b, a);
    expect(a.text.toString()).toBe(b.text.toString());
    expect(a.text.toString()).toContain("two AND A HALF");
    expect(a.text.toString()).toContain("ONE");
    expect(a.text.toString()).toContain("FOUR");
  });

  it("sends the changed character, not the document", () => {
    const long = Array.from({ length: 200 }, (_, i) => `line ${i} of a long document`).join("\n");
    const [a] = pair(long);
    const seedSize = Y.encodeStateAsUpdate(a.doc).length;

    const result = reconcileText(a.text, long.replace("line 100", "line 1O0"));

    expect(result).toEqual({ outcome: "merged", changed: 2 });
    expect(seedSize).toBeGreaterThan(4000);
    expect(a.updates).toHaveLength(1);
    expect(a.updates[0].length).toBeLessThan(100);
  });

  it("emits nothing at all when the file matches the document", () => {
    const [a] = pair(DOCUMENT);

    expect(reconcileText(a.text, DOCUMENT)).toEqual({ outcome: "unchanged", changed: 0 });
    expect(a.updates).toEqual([]);
  });

  it("refuses a wholesale rewrite instead of merging it", () => {
    const [a, b] = pair(CHAPTER);
    const rewritten = Array.from({ length: 30 }, (_, i) => `% a restored line ${i}`).join("\n");

    const result = reconcileText(a.text, rewritten);

    expect(result.outcome).toBe("too-large");
    expect(result.changed).toBeGreaterThan(0);
    expect(a.text.toString()).toBe(CHAPTER);
    expect(a.updates).toEqual([]);
    expect(b.text.toString()).toBe(CHAPTER);
  });

  it("takes the same rewrite when the caller raises the ratio", () => {
    const [a] = pair(CHAPTER);
    const rewritten = Array.from({ length: 30 }, (_, i) => `% a restored line ${i}`).join("\n");

    expect(reconcileText(a.text, rewritten, { maxChangedRatio: 10 }).outcome).toBe("merged");
    expect(a.text.toString()).toBe(rewritten);
  });

  it("merges a small file wholesale, where there is nothing to bury", () => {
    const [a] = pair("draft\n");

    expect(reconcileText(a.text, "an entirely different note\n").outcome).toBe("merged");
    expect(a.text.toString()).toBe("an entirely different note\n");
  });

  it("fills an empty document rather than calling it a rewrite", () => {
    const a = join();

    expect(reconcileText(a.text, DOCUMENT).outcome).toBe("merged");
    expect(a.text.toString()).toBe(DOCUMENT);
  });

  it("merges at an emoji boundary without splitting the pair", () => {
    const seed = "greeting: 😀 hello\nsecond line\n";
    const [a, b] = pair(seed);
    b.doc.transact(() => b.text.insert(seed.indexOf("second") + 6, " (edited)"));

    reconcileText(a.text, seed.replace("😀 hello", "😀 HELLO"));

    sync(a, b);
    sync(b, a);
    expect(a.text.toString()).toBe(b.text.toString());
    expect(a.text.toString()).toContain("😀 HELLO");
    expect(a.text.toString()).toContain("second (edited) line");
    expect(LONE_SURROGATE.test(a.text.toString())).toBe(false);
  });

  it("replaces one emoji with another as a whole character", () => {
    const [a, b] = pair("a😀b\n");

    const result = reconcileText(a.text, "a😃b\n");

    sync(a, b);
    expect(b.text.toString()).toBe("a😃b\n");
    expect(LONE_SURROGATE.test(b.text.toString())).toBe(false);
    // The pair moves together: two units out, two in, and the letters stay put.
    expect(result.changed).toBe(4);
  });

  it("merges CJK text without disturbing its neighbours", () => {
    const seed = "第一行の見出し\n第二行はここにある\n第三行\n";
    const [a, b] = pair(seed);
    b.doc.transact(() => b.text.insert(seed.indexOf("第三行") + 3, "の続き"));

    reconcileText(a.text, seed.replace("見出し", "小見出し"));

    sync(a, b);
    sync(b, a);
    expect(a.text.toString()).toBe(b.text.toString());
    expect(a.text.toString()).toBe("第一行の小見出し\n第二行はここにある\n第三行の続き\n");
  });

  it("carries the caller's origin on the transaction", () => {
    const origin = Symbol("file");
    const [a] = pair(DOCUMENT);
    const seen: unknown[] = [];
    a.doc.on("update", (_update: Uint8Array, from: unknown) => {
      seen.push(from);
    });

    reconcileText(a.text, DOCUMENT.replace("three", "3"), { origin });

    expect(seen).toEqual([origin]);
  });
});
