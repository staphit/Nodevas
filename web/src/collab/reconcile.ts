/**
 * Merging a file that changed on disk into a live document [P2].
 *
 * While two people edit a node through the CRDT, the file underneath them can
 * still move: somebody saves it from vim, an agent rewrites it, a `git checkout`
 * lands. The watcher fires and the browser has to get that text into a `Y.Text`
 * that other people are typing into *right now*.
 *
 * Loading it the old way — replace the document with the file's text — is the
 * one thing that must not happen here. To the CRDT a clear-and-reinsert reads as
 * "every character was deleted": the peer's in-flight keystroke is dropped, every
 * remote caret collapses to the start, and the update carries the whole document.
 * So the file is diffed against what the document already holds and only the
 * difference is applied, as ordinary inserts and deletes that merge with whatever
 * else is happening.
 *
 * Pure on purpose: no socket, no store, no editor. The failures this exists to
 * prevent are invisible through an editor and obvious against two `Y.Doc`s wired
 * to each other.
 */

import * as Y from "yjs";

/**
 * How much of the document may change before a merge is refused.
 *
 * Nothing routine touches half a document. A save from another editor, an
 * agent's edit, a rename across a file — all of them are a handful of lines. A
 * change past this line is a restore, a branch switch or a truncation, i.e. an
 * event the user decided on and does not expect to be blended into somebody's
 * half-typed paragraph. The two mistakes are not symmetrical: refusing a merge
 * costs a prompt, merging a wholesale rewrite silently costs work that no undo
 * reaches, because to everyone else it looks like their collaborator typed it.
 */
export const DEFAULT_MAX_CHANGED_RATIO = 0.5;

/**
 * Below this many changed characters the ratio is not consulted at all.
 *
 * A ratio alone makes small documents unusable: swapping one emoji for another
 * in a four-character note is four characters of a four-character file, which no
 * ratio can tell apart from a restore. The floor is roughly a paragraph — under
 * it there is no meaningful amount of somebody else's work to bury, and stopping
 * to ask about it is worse than the merge.
 */
export const MIN_GUARDED_CHANGE = 200;

/**
 * Ceiling on the line-diff table, in cells.
 *
 * The table is quadratic, so an unbounded one turns a big file landing on
 * another big file into a frozen tab. Past this the middle is replaced in one
 * span instead — a worse merge, but such a change is almost always over the
 * ratio above and refused anyway.
 */
const MAX_LCS_CELLS = 1_000_000;

export interface ReconcileResult {
  /** What happened, for the caller to report to the user. */
  outcome: "merged" | "unchanged" | "too-large";
  /** Characters inserted + deleted. */
  changed: number;
}

export interface ReconcileOptions {
  /** Fraction of the document that may change; see DEFAULT_MAX_CHANGED_RATIO. */
  maxChangedRatio?: number;
  /**
   * Carried by the transaction so the caller can tell this apart from a local
   * keystroke — without it a merge looks like typing and gets echoed back out to
   * the room, or worse, saved back to the file it just came from.
   */
  origin?: unknown;
}

/**
 * Merges the text a file now holds into a live Y.Text.
 */
export function reconcileText(
  ytext: Y.Text,
  fileText: string,
  options: ReconcileOptions = {},
): ReconcileResult {
  const current = ytext.toString();
  if (current === fileText) return { outcome: "unchanged", changed: 0 };

  const ops = diffOps(current, fileText);
  const changed = ops.reduce((total, op) => total + op.remove + op.insert.length, 0);
  if (changed === 0) return { outcome: "unchanged", changed: 0 };

  const ratio = options.maxChangedRatio ?? DEFAULT_MAX_CHANGED_RATIO;
  // An empty document is exempt: there is no concurrent work to bury, and this
  // is the case where a session opened before its file had been read.
  if (changed >= MIN_GUARDED_CHANGE && current.length > 0 && changed > current.length * ratio) {
    return { outcome: "too-large", changed };
  }

  const apply = () => {
    // Back to front, so an earlier op cannot shift the offsets of a later one.
    for (let i = ops.length - 1; i >= 0; i -= 1) {
      const op = ops[i];
      if (op.remove > 0) ytext.delete(op.at, op.remove);
      if (op.insert.length > 0) ytext.insert(op.at, op.insert);
    }
  };
  // One transaction, so peers see the file's arrival as a single event rather
  // than a stutter of half-applied states. A `Y.Text` that is not in a document
  // yet has no transaction to run in and nobody observing it either.
  if (ytext.doc) ytext.doc.transact(apply, options.origin);
  else apply();

  return { outcome: "merged", changed };
}

interface Op {
  at: number;
  remove: number;
  insert: string;
}

/**
 * The difference as a list of non-overlapping replacements, left to right.
 *
 * Lines are the unit because that is the unit external edits come in, and
 * because a character diff over a document-sized input is the quadratic cost
 * this is trying to avoid. Each replacement is then trimmed character-wise, so a
 * typo fixed on disk moves one character and not the line around it — which is
 * what keeps a peer editing the same line from losing their edit.
 */
function diffOps(current: string, next: string): Op[] {
  const a = splitLines(current);
  const b = splitLines(next);

  const shorter = Math.min(a.length, b.length);
  let pre = 0;
  while (pre < shorter && a[pre] === b[pre]) pre += 1;
  let suf = 0;
  while (suf < shorter - pre && a[a.length - 1 - suf] === b[b.length - 1 - suf]) suf += 1;

  let offset = 0;
  for (let i = 0; i < pre; i += 1) offset += a[i].length;

  const aMid = a.slice(pre, a.length - suf);
  const bMid = b.slice(pre, b.length - suf);
  if (aMid.length === 0 && bMid.length === 0) return [];
  if (aMid.length * bMid.length > MAX_LCS_CELLS) {
    return [trim(offset, aMid.join(""), bMid.join(""))].filter(isSomething);
  }

  const ops: Op[] = [];
  let at = offset;
  let removed = "";
  let inserted = "";
  const flush = () => {
    if (removed.length > 0 || inserted.length > 0) {
      const op = trim(at, removed, inserted);
      if (isSomething(op)) ops.push(op);
    }
    removed = "";
    inserted = "";
  };

  const width = bMid.length + 1;
  const lcs = lcsTable(aMid, bMid);
  let i = 0;
  let j = 0;
  while (i < aMid.length || j < bMid.length) {
    if (i < aMid.length && j < bMid.length && aMid[i] === bMid[j]) {
      flush();
      offset += aMid[i].length;
      i += 1;
      j += 1;
    } else if (j < bMid.length && (i >= aMid.length || lcs[i * width + j + 1] >= lcs[(i + 1) * width + j])) {
      if (removed.length === 0 && inserted.length === 0) at = offset;
      inserted += bMid[j];
      j += 1;
    } else {
      if (removed.length === 0 && inserted.length === 0) at = offset;
      removed += aMid[i];
      offset += aMid[i].length;
      i += 1;
    }
  }
  flush();
  return ops;
}

/** Longest common subsequence lengths, read from the end backwards. */
function lcsTable(a: string[], b: string[]): Uint32Array {
  const width = b.length + 1;
  const table = new Uint32Array((a.length + 1) * width);
  for (let i = a.length - 1; i >= 0; i -= 1) {
    for (let j = b.length - 1; j >= 0; j -= 1) {
      table[i * width + j] =
        a[i] === b[j]
          ? table[(i + 1) * width + j + 1] + 1
          : Math.max(table[(i + 1) * width + j], table[i * width + j + 1]);
    }
  }
  return table;
}

/**
 * Narrows a replacement to the span that actually differs.
 *
 * The boundaries back off the low half of a surrogate pair: a split there ships
 * a lone surrogate, and the emoji is then broken for everybody in the session
 * and in the file the leader writes back.
 */
function trim(at: number, removed: string, inserted: string): Op {
  const limit = Math.min(removed.length, inserted.length);
  let start = 0;
  while (start < limit && removed.charCodeAt(start) === inserted.charCodeAt(start)) start += 1;
  let end = 0;
  while (
    end < limit - start &&
    removed.charCodeAt(removed.length - 1 - end) === inserted.charCodeAt(inserted.length - 1 - end)
  ) {
    end += 1;
  }
  const head = start < removed.length ? removed.charCodeAt(start) : inserted.charCodeAt(start);
  if (start > 0 && isLowSurrogate(head)) start -= 1;
  const tail =
    removed.length > 0
      ? removed.charCodeAt(removed.length - end)
      : inserted.charCodeAt(inserted.length - end);
  if (end > 0 && isLowSurrogate(tail)) end -= 1;

  return {
    at: at + start,
    remove: removed.length - start - end,
    insert: inserted.slice(start, inserted.length - end),
  };
}

function isSomething(op: Op): boolean {
  return op.remove > 0 || op.insert.length > 0;
}

function isLowSurrogate(code: number): boolean {
  return code >= 0xdc00 && code <= 0xdfff;
}

/** Terminators stay on their line, so the pieces still join back into the text. */
function splitLines(text: string): string[] {
  const lines: string[] = [];
  let start = 0;
  for (let i = 0; i < text.length; i += 1) {
    if (text.charCodeAt(i) === 10) {
      lines.push(text.slice(start, i + 1));
      start = i + 1;
    }
  }
  if (start < text.length) lines.push(text.slice(start));
  return lines;
}
