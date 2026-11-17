/**
 * A Copilot draft, as the person edits it before anything is created.
 *
 * Pure functions over a Draft: every edit returns a new draft and never
 * touches the one it was given, so the panel can keep the copilot's original
 * next to the edited copy, and the refine diff can compare the two. Nothing
 * here talks to the server or the store.
 *
 * `after` and `refs` name other draft keys, or an existing node as
 * `existing:<nodeId>`.
 */

import type { CopilotDraft, CopilotDraftNode, SubgraphSpec } from "../../api";

export type Draft = CopilotDraft;
export type DraftNode = CopilotDraftNode;

/** The server's cap (internal/copilot/draft_schema.go MaxDraftNodes). */
export const MAX_DRAFT_NODES = 40;
export const MAX_TITLE_CHARS = 60;
export const MAX_BODY_CHARS = 600;

export const EXISTING_PREFIX = "existing:";

export function isExistingRef(ref: string): boolean {
  return ref.startsWith(EXISTING_PREFIX);
}

/** The node id of an `existing:<id>` reference, else "". */
export function existingIdOf(ref: string): string {
  return isExistingRef(ref) ? ref.slice(EXISTING_PREFIX.length) : "";
}

export function existingRef(nodeId: string): string {
  return EXISTING_PREFIX + nodeId;
}

const unique = (values: string[]) => [...new Set(values)];

function mapNodes(draft: Draft, update: (node: DraftNode) => DraftNode): Draft {
  return { ...draft, nodes: draft.nodes.map(update) };
}

/** Replaces one reference everywhere (after and refs), dropping self-links and repeats. */
function repoint(node: DraftNode, from: string, to: string[]): DraftNode {
  const swap = (list: string[] | undefined) => {
    if (!list || !list.includes(from)) return list;
    const next = unique(list.flatMap((ref) => (ref === from ? to : [ref]))).filter(
      (ref) => ref !== node.key,
    );
    return next;
  };
  const after = swap(node.after);
  const refs = swap(node.refs);
  if (after === node.after && refs === node.refs) return node;
  return { ...node, after, refs };
}

export function renameNode(draft: Draft, key: string, title: string): Draft {
  const next = title.trim().slice(0, MAX_TITLE_CHARS);
  return mapNodes(draft, (node) => (node.key === key ? { ...node, title: next } : node));
}

export function changeKind(draft: Draft, key: string, kind: string): Draft {
  return mapNodes(draft, (node) => (node.key === key ? { ...node, kind } : node));
}

/**
 * Removes a node. What waited for it now waits for what it waited for, so a
 * chain a → b → c with b removed stays a → c. A reference to it is dropped.
 */
export function removeNode(draft: Draft, key: string): Draft {
  const removed = draft.nodes.find((node) => node.key === key);
  if (!removed) return draft;
  const inherited = removed.after ?? [];
  return {
    ...draft,
    nodes: draft.nodes
      .filter((node) => node.key !== key)
      .map((node) => {
        const after = node.after?.includes(key)
          ? unique(node.after.flatMap((ref) => (ref === key ? inherited : [ref]))).filter(
              (ref) => ref !== node.key,
            )
          : node.after;
        const refs = node.refs?.includes(key) ? node.refs.filter((ref) => ref !== key) : node.refs;
        return after === node.after && refs === node.refs ? node : { ...node, after, refs };
      }),
  };
}

/**
 * Merges `other` into `keep`: other's title (and body) go into keep's body,
 * their dependencies are unioned without either one, and whatever pointed at
 * other points at keep.
 */
export function mergeNodes(draft: Draft, keepKey: string, otherKey: string): Draft {
  if (keepKey === otherKey) return draft;
  const keep = draft.nodes.find((node) => node.key === keepKey);
  const other = draft.nodes.find((node) => node.key === otherKey);
  if (!keep || !other) return draft;
  const addition = [other.title, other.body ?? ""].filter((part) => part.trim()).join("\n");
  const body = [keep.body ?? "", addition].filter((part) => part.trim()).join("\n\n");
  const after = unique([...(keep.after ?? []), ...(other.after ?? [])]).filter(
    (ref) => ref !== keepKey && ref !== otherKey,
  );
  const refs = unique([...(keep.refs ?? []), ...(other.refs ?? [])]).filter(
    (ref) => ref !== keepKey && ref !== otherKey,
  );
  const merged: DraftNode = {
    ...keep,
    body: body.slice(0, MAX_BODY_CHARS) || undefined,
    after,
    refs: refs.length ? refs : undefined,
  };
  return {
    ...draft,
    nodes: draft.nodes
      .filter((node) => node.key !== otherKey)
      .map((node) => (node.key === keepKey ? merged : repoint(node, otherKey, [keepKey]))),
  };
}

/** Merges every key into the first, in order. */
export function mergeMany(draft: Draft, keys: string[]): Draft {
  const [first, ...rest] = keys;
  return rest.reduce((current, key) => mergeNodes(current, first, key), draft);
}

/** Sets what a node waits for (draft keys or `existing:<id>`). */
export function relink(draft: Draft, key: string, after: string[]): Draft {
  const next = unique(after).filter((ref) => ref !== key);
  return mapNodes(draft, (node) => (node.key === key ? { ...node, after: next } : node));
}

export function addAfter(draft: Draft, key: string, target: string): Draft {
  const node = draft.nodes.find((item) => item.key === key);
  if (!node || target === key) return draft;
  return relink(draft, key, [...(node.after ?? []), target]);
}

export function removeAfter(draft: Draft, key: string, target: string): Draft {
  const node = draft.nodes.find((item) => item.key === key);
  if (!node) return draft;
  return relink(
    draft,
    key,
    (node.after ?? []).filter((ref) => ref !== target),
  );
}

/**
 * The node repeats an existing one: drop it, and have whatever waited for it
 * (or used it) wait for (or use) the existing node instead.
 */
export function linkInsteadOfDuplicate(draft: Draft, key: string, existingId: string): Draft {
  if (!draft.nodes.some((node) => node.key === key)) return draft;
  const target = existingRef(existingId);
  return {
    ...draft,
    nodes: draft.nodes
      .filter((node) => node.key !== key)
      .map((node) => repoint(node, key, [target])),
  };
}

// ---------------------------------------------------------------------------
// Client-side checks: instant feedback while editing. The server's dry run
// (POST /api/graph/subgraph/check) has the last word.
// ---------------------------------------------------------------------------

export type ClientIssueCode =
  | "empty_draft"
  | "too_many"
  | "empty_title"
  | "duplicate_key"
  | "unknown_kind"
  | "unknown_after"
  | "self_after"
  | "cycle"
  | "unknown_ref";

export interface ClientIssue {
  code: ClientIssueCode;
  key?: string;
  /** The reference at fault (unknown_after, unknown_ref). */
  ref?: string;
}

/** Keys of nodes that sit on a dependency cycle (through draft keys only). */
export function findCycleKeys(draft: Draft): Set<string> {
  const keys = new Set(draft.nodes.map((node) => node.key));
  const edges = new Map(
    draft.nodes.map((node) => [node.key, (node.after ?? []).filter((ref) => keys.has(ref))]),
  );
  // Tarjan's strongly connected components.
  let index = 0;
  const indexOf = new Map<string, number>();
  const low = new Map<string, number>();
  const stack: string[] = [];
  const onStack = new Set<string>();
  const cyclic = new Set<string>();
  const visit = (key: string) => {
    indexOf.set(key, index);
    low.set(key, index);
    index += 1;
    stack.push(key);
    onStack.add(key);
    for (const next of edges.get(key) ?? []) {
      if (!indexOf.has(next)) {
        visit(next);
        low.set(key, Math.min(low.get(key)!, low.get(next)!));
      } else if (onStack.has(next)) {
        low.set(key, Math.min(low.get(key)!, indexOf.get(next)!));
      }
    }
    if (low.get(key) === indexOf.get(key)) {
      const component: string[] = [];
      let member: string;
      do {
        member = stack.pop()!;
        onStack.delete(member);
        component.push(member);
      } while (member !== key);
      if (component.length > 1 || (edges.get(key) ?? []).includes(key)) {
        for (const item of component) cyclic.add(item);
      }
    }
  };
  for (const key of keys) if (!indexOf.has(key)) visit(key);
  return cyclic;
}

/**
 * What would stop the draft from being created, found without a round trip.
 * `existingIds` are the board's node ids (unknown targets are checked only
 * when given); `kinds` the project's type ids (likewise).
 */
export function validateDraft(
  draft: Draft,
  existingIds?: ReadonlySet<string>,
  kinds?: ReadonlySet<string>,
): ClientIssue[] {
  const issues: ClientIssue[] = [];
  if (draft.nodes.length === 0) issues.push({ code: "empty_draft" });
  if (draft.nodes.length > MAX_DRAFT_NODES) issues.push({ code: "too_many" });
  const keys = new Set<string>();
  for (const node of draft.nodes) {
    if (keys.has(node.key)) issues.push({ code: "duplicate_key", key: node.key });
    keys.add(node.key);
  }
  const known = (ref: string) => {
    if (isExistingRef(ref)) return !existingIds || existingIds.has(existingIdOf(ref));
    return keys.has(ref);
  };
  for (const node of draft.nodes) {
    if (!node.title.trim()) issues.push({ code: "empty_title", key: node.key });
    if (kinds && node.kind && !kinds.has(node.kind)) issues.push({ code: "unknown_kind", key: node.key });
    for (const ref of node.after ?? []) {
      if (ref === node.key) issues.push({ code: "self_after", key: node.key, ref });
      else if (!known(ref)) issues.push({ code: "unknown_after", key: node.key, ref });
    }
    for (const ref of node.refs ?? []) {
      if (!known(ref)) issues.push({ code: "unknown_ref", key: node.key, ref });
    }
  }
  for (const key of findCycleKeys(draft)) issues.push({ code: "cycle", key });
  return issues;
}

/** Dependencies the draft makes: every `after` entry, existing nodes included. */
export function countLinks(draft: Draft): number {
  return draft.nodes.reduce((sum, node) => sum + (node.after?.length ?? 0), 0);
}

// ---------------------------------------------------------------------------
// Refine diff
// ---------------------------------------------------------------------------

export type DiffMark = "added" | "changed";

export interface DraftDiff {
  /** By key of the new draft: added, or renamed / re-linked / re-typed. */
  marks: Record<string, DiffMark>;
  /** Nodes of the previous draft that are gone. */
  removed: DraftNode[];
}

const sameSet = (a: string[] | undefined, b: string[] | undefined) => {
  const left = new Set(a ?? []);
  const right = new Set(b ?? []);
  return left.size === right.size && [...left].every((item) => right.has(item));
};

/** What a refined draft changed, by key, against the draft it refined. */
export function diffDrafts(previous: Draft, next: Draft): DraftDiff {
  const before = new Map(previous.nodes.map((node) => [node.key, node]));
  const keys = new Set(next.nodes.map((node) => node.key));
  const marks: Record<string, DiffMark> = {};
  for (const node of next.nodes) {
    const old = before.get(node.key);
    if (!old) marks[node.key] = "added";
    else if (
      old.title.trim() !== node.title.trim() ||
      old.kind !== node.kind ||
      !sameSet(old.after, node.after)
    ) {
      marks[node.key] = "changed";
    }
  }
  return { marks, removed: previous.nodes.filter((node) => !keys.has(node.key)) };
}

// ---------------------------------------------------------------------------
// Draft → POST /api/graph/subgraph
// ---------------------------------------------------------------------------

/** `existing:<id>` becomes the bare id; draft keys stay keys. */
const toSpecRef = (ref: string) => existingIdOf(ref) || ref;

export function draftToSpec(
  draft: Draft,
  options: { positions?: Record<string, { col: number; row: number }>; via?: string } = {},
): SubgraphSpec {
  const spec: SubgraphSpec = {
    nodes: draft.nodes.map((node) => {
      const dependsOn = (node.after ?? []).map(toSpecRef);
      const refs = (node.refs ?? []).map(toSpecRef);
      return {
        key: node.key,
        title: node.title.trim(),
        ...(node.kind ? { kind: node.kind } : {}),
        ...(node.body?.trim() ? { body: node.body } : {}),
        ...(dependsOn.length ? { dependsOn } : {}),
        ...(refs.length ? { refs } : {}),
      };
    }),
  };
  if (options.positions && Object.keys(options.positions).length) spec.positions = options.positions;
  if (options.via) spec.batch = { via: options.via };
  return spec;
}

/** The batch tag a draft's Create carries, so the undo can find it. */
export function draftVia(entryId: string): string {
  return `copilot-draft:${entryId}`;
}

/** Whether the person's words name code: repo paths, files or functions. */
export function mentionsCode(text: string): boolean {
  return (
    /(^|[^\w/])[\w.-]+\/[\w./-]+/.test(text) ||
    /\b[\w-]+\.(ts|tsx|js|jsx|go|py|rs|java|kt|rb|cs|cpp|c|h|swift|php|vue|svelte|css|scss|sql|md|yaml|yml|json)\b/i.test(
      text,
    ) ||
    /\b[A-Za-z_][\w]*\(\)/.test(text) ||
    /`[^`]+`/.test(text)
  );
}
