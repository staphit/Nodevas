import { describe, expect, it } from "vitest";
import {
  addAfter,
  changeKind,
  countLinks,
  diffDrafts,
  draftToSpec,
  findCycleKeys,
  linkInsteadOfDuplicate,
  mentionsCode,
  mergeMany,
  mergeNodes,
  relink,
  removeAfter,
  removeNode,
  renameNode,
  validateDraft,
  type Draft,
} from "./draftStore";

const chain: Draft = {
  summary: "Launch",
  nodes: [
    { key: "docs", title: "Write docs", kind: "task" },
    { key: "test", title: "Test", kind: "task", after: ["docs"] },
    { key: "ship", title: "Ship", kind: "task", after: ["test", "existing:node-0007"] },
  ],
};

const node = (draft: Draft, key: string) => draft.nodes.find((item) => item.key === key)!;

describe("draft edits", () => {
  it("renames without touching the original", () => {
    const next = renameNode(chain, "test", "  Test on staging ");
    expect(node(next, "test").title).toBe("Test on staging");
    expect(node(chain, "test").title).toBe("Test");
  });

  it("removes a node and bridges its dependents to what it waited for", () => {
    const next = removeNode(chain, "test");
    expect(next.nodes.map((item) => item.key)).toEqual(["docs", "ship"]);
    expect(node(next, "ship").after).toEqual(["docs", "existing:node-0007"]);
  });

  it("removing a first step leaves its dependents waiting for nothing new", () => {
    const next = removeNode(chain, "docs");
    expect(node(next, "test").after).toEqual([]);
  });

  it("merges: the second title goes into the body, after is unioned without self-links", () => {
    const draft: Draft = {
      summary: "",
      nodes: [
        { key: "a", title: "A", kind: "task" },
        { key: "b", title: "B", kind: "task", after: ["a"] },
        { key: "c", title: "C", kind: "task", after: ["b", "a"], body: "c body" },
        { key: "d", title: "D", kind: "task", after: ["c"] },
      ],
    };
    const next = mergeNodes(draft, "b", "c");
    expect(next.nodes.map((item) => item.key)).toEqual(["a", "b", "d"]);
    expect(node(next, "b").body).toBe("C\nc body");
    // c waited for b: merging must not leave b waiting for itself.
    expect(node(next, "b").after).toEqual(["a"]);
    // d waited for c, now for b.
    expect(node(next, "d").after).toEqual(["b"]);
  });

  it("merges several into the first", () => {
    const next = mergeMany(chain, ["docs", "test", "ship"]);
    expect(next.nodes).toHaveLength(1);
    expect(node(next, "docs").body).toBe("Test\n\nShip");
    expect(node(next, "docs").after).toEqual(["existing:node-0007"]);
  });

  it("relinks, dropping self and repeats", () => {
    const next = relink(chain, "ship", ["docs", "docs", "ship"]);
    expect(node(next, "ship").after).toEqual(["docs"]);
    expect(node(addAfter(next, "ship", "test"), "ship").after).toEqual(["docs", "test"]);
    expect(node(removeAfter(next, "ship", "docs"), "ship").after).toEqual([]);
  });

  it("changes kind", () => {
    expect(node(changeKind(chain, "ship", "gate"), "ship").kind).toBe("gate");
  });

  it("links to an existing node instead of a duplicate", () => {
    const next = linkInsteadOfDuplicate(chain, "test", "node-0003");
    expect(next.nodes.map((item) => item.key)).toEqual(["docs", "ship"]);
    expect(node(next, "ship").after).toEqual(["existing:node-0003", "existing:node-0007"]);
  });

  it("counts dependencies", () => {
    expect(countLinks(chain)).toBe(3);
  });
});

describe("validateDraft", () => {
  it("finds a cycle", () => {
    const looped = relink(chain, "docs", ["ship"]);
    expect([...findCycleKeys(looped)].sort()).toEqual(["docs", "ship", "test"]);
    expect(validateDraft(looped).filter((issue) => issue.code === "cycle")).toHaveLength(3);
    expect(findCycleKeys(chain).size).toBe(0);
  });

  it("flags empty titles, unknown targets and kinds, and too many nodes", () => {
    const draft: Draft = {
      summary: "",
      nodes: [
        { key: "a", title: " ", kind: "task", after: ["zzz", "existing:gone"] },
        { key: "b", title: "B", kind: "wizard" },
      ],
    };
    const codes = validateDraft(draft, new Set(["node-1"]), new Set(["task"])).map((issue) => issue.code);
    expect(codes).toEqual(["empty_title", "unknown_after", "unknown_after", "unknown_kind"]);
    const many: Draft = {
      summary: "",
      nodes: Array.from({ length: 41 }, (_, index) => ({ key: `k${index}`, title: `T${index}`, kind: "task" })),
    };
    expect(validateDraft(many).map((issue) => issue.code)).toEqual(["too_many"]);
    expect(validateDraft({ summary: "", nodes: [] }).map((issue) => issue.code)).toEqual(["empty_draft"]);
  });

  it("passes a sound draft", () => {
    expect(validateDraft(chain, new Set(["node-0007"]))).toEqual([]);
  });
});

describe("diffDrafts", () => {
  it("marks added, changed and removed nodes", () => {
    const next: Draft = {
      summary: "",
      nodes: [
        { key: "docs", title: "Write docs", kind: "task" },
        { key: "review", title: "Review", kind: "gate", after: ["docs"] },
        { key: "ship", title: "Ship it", kind: "task", after: ["review"] },
      ],
    };
    const diff = diffDrafts(chain, next);
    expect(diff.marks).toEqual({ review: "added", ship: "changed" });
    expect(diff.removed.map((item) => item.key)).toEqual(["test"]);
  });

  it("compares against the edited draft, so manual edits read as kept, not changed", () => {
    const edited = renameNode(chain, "docs", "Write the user guide");
    // The drafter got the edited draft and kept the rename.
    const refined: Draft = { ...edited, nodes: [...edited.nodes, { key: "x", title: "X", kind: "task" }] };
    const diff = diffDrafts(edited, refined);
    expect(diff.marks).toEqual({ x: "added" });
    expect(node(refined, "docs").title).toBe("Write the user guide");
  });
});

describe("draftToSpec", () => {
  it("maps after to dependsOn, strips existing:, and carries positions and the batch", () => {
    const draft: Draft = {
      ...chain,
      nodes: [...chain.nodes, { key: "n", title: "Note", kind: "note", refs: ["existing:node-0001"], body: "x" }],
    };
    const spec = draftToSpec(draft, { positions: { docs: { col: 4, row: 1 } }, via: "copilot-draft:e1" });
    expect(spec).toEqual({
      nodes: [
        { key: "docs", title: "Write docs", kind: "task" },
        { key: "test", title: "Test", kind: "task", dependsOn: ["docs"] },
        { key: "ship", title: "Ship", kind: "task", dependsOn: ["test", "node-0007"] },
        { key: "n", title: "Note", kind: "note", body: "x", refs: ["node-0001"] },
      ],
      positions: { docs: { col: 4, row: 1 } },
      batch: { via: "copilot-draft:e1" },
    });
  });
});

describe("mentionsCode", () => {
  it("spots paths, files and functions", () => {
    expect(mentionsCode("fix web/src/App.tsx")).toBe(true);
    expect(mentionsCode("refactor session.ts")).toBe(true);
    expect(mentionsCode("rename sendTurn() please")).toBe(true);
    expect(mentionsCode("plan a product launch: docs, tests, release")).toBe(false);
  });
});
