// Parses the unified diff a run reports (`git diff <base>...<commit>`) into
// files and hunks, so the diff viewer can show one file at a time. Only what
// git itself writes is handled: `diff --git` headers, extended header lines
// (modes, renames, copies, index), `---`/`+++`, `@@` hunks, "\ No newline at
// end of file" markers and binary files.

export type DiffFileStatus = "added" | "modified" | "deleted" | "renamed";

export interface DiffLine {
  kind: "context" | "add" | "del";
  /** The line without its leading " ", "+" or "-". */
  text: string;
  oldNo?: number;
  newNo?: number;
  /** The line is the last in its file and has no trailing newline. */
  noNewline?: boolean;
}

export interface DiffHunk {
  header: string;
  oldStart: number;
  oldLines: number;
  newStart: number;
  newLines: number;
  /** The function or section git puts after the second "@@". */
  section: string;
  lines: DiffLine[];
}

export interface DiffFile {
  /** The path the file has after the change (the old one when deleted). */
  path: string;
  oldPath: string;
  newPath: string;
  status: DiffFileStatus;
  binary: boolean;
  oldMode?: string;
  newMode?: string;
  hunks: DiffHunk[];
  additions: number;
  deletions: number;
}

const DEV_NULL = "/dev/null";
const HUNK_RE = /^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@ ?(.*)$/;

/** Decodes a git C-quoted path ("a/caf\303\251.txt") to text. */
export function unquoteGitPath(value: string): string {
  if (!value.startsWith('"') || !value.endsWith('"') || value.length < 2) return value;
  const body = value.slice(1, -1);
  const bytes: number[] = [];
  const pushText = (text: string) => {
    for (const byte of new TextEncoder().encode(text)) bytes.push(byte);
  };
  for (let i = 0; i < body.length; i++) {
    const ch = body[i];
    if (ch !== "\\" || i === body.length - 1) {
      pushText(ch);
      continue;
    }
    const next = body[++i];
    if (/[0-7]/.test(next)) {
      let digits = next;
      while (digits.length < 3 && /[0-7]/.test(body[i + 1] ?? "")) digits += body[++i];
      bytes.push(parseInt(digits, 8) & 0xff);
      continue;
    }
    const simple: Record<string, string> = {
      n: "\n",
      t: "\t",
      r: "\r",
      a: "\x07",
      b: "\b",
      f: "\f",
      v: "\v",
      '"': '"',
      "\\": "\\",
    };
    pushText(simple[next] ?? next);
  }
  return new TextDecoder().decode(new Uint8Array(bytes));
}

/** Reads one path token (quoted or not) from the start of `text`. */
function takeQuoted(text: string): [string, string] | null {
  if (!text.startsWith('"')) return null;
  for (let i = 1; i < text.length; i++) {
    if (text[i] === "\\") {
      i++;
      continue;
    }
    if (text[i] === '"') return [text.slice(0, i + 1), text.slice(i + 1)];
  }
  return null;
}

function stripPrefix(path: string, prefix: "a/" | "b/"): string {
  return path.startsWith(prefix) ? path.slice(prefix.length) : path;
}

/** Splits the paths out of "diff --git a/x b/y". Unquoted paths may contain
 * spaces, so an ambiguous header is split where both halves name the same
 * file; a rename's real paths come from its "rename from/to" lines anyway. */
export function parseGitHeaderPaths(rest: string): { oldPath: string; newPath: string } | null {
  const quotedA = takeQuoted(rest);
  if (quotedA) {
    const after = quotedA[1].trimStart();
    const quotedB = takeQuoted(after);
    const b = quotedB ? unquoteGitPath(quotedB[0]) : after;
    return {
      oldPath: stripPrefix(unquoteGitPath(quotedA[0]), "a/"),
      newPath: stripPrefix(b, "b/"),
    };
  }
  if (!rest.startsWith("a/")) return null;
  const quotedTail = rest.lastIndexOf(' "b/');
  if (quotedTail > 0 && rest.endsWith('"')) {
    return {
      oldPath: stripPrefix(rest.slice(0, quotedTail), "a/"),
      newPath: stripPrefix(unquoteGitPath(rest.slice(quotedTail + 1)), "b/"),
    };
  }
  const splits: number[] = [];
  for (let i = rest.indexOf(" b/"); i !== -1; i = rest.indexOf(" b/", i + 1)) splits.push(i);
  if (splits.length === 0) return null;
  const same = splits.find((i) => rest.slice(2, i) === rest.slice(i + 3));
  const at = same ?? splits[0];
  return { oldPath: rest.slice(2, at), newPath: rest.slice(at + 3) };
}

/** A "---"/"+++" path: quoted or not, "/dev/null", without a/ b/ prefix. */
function markerPath(raw: string, prefix: "a/" | "b/"): string {
  let value = raw;
  if (!value.startsWith('"')) {
    // GNU diff appends "\t<timestamp>"; git quotes any path holding a tab.
    const tab = value.indexOf("\t");
    if (tab !== -1) value = value.slice(0, tab);
  } else {
    value = unquoteGitPath(value);
  }
  if (value === DEV_NULL) return DEV_NULL;
  return stripPrefix(value, prefix);
}

function newFile(oldPath: string, newPath: string): DiffFile {
  return {
    path: newPath,
    oldPath,
    newPath,
    status: "modified",
    binary: false,
    hunks: [],
    additions: 0,
    deletions: 0,
  };
}

function finish(file: DiffFile): DiffFile {
  if (file.oldPath === DEV_NULL) file.status = "added";
  if (file.newPath === DEV_NULL) file.status = "deleted";
  if (file.status === "added") file.oldPath = file.oldPath === DEV_NULL ? file.newPath : file.oldPath;
  if (file.status === "deleted") {
    if (file.newPath === DEV_NULL) file.newPath = file.oldPath;
    file.path = file.oldPath;
  } else {
    file.path = file.newPath;
  }
  if (file.status === "modified" && file.oldPath !== file.newPath) file.status = "renamed";
  return file;
}

/** Splits a run's unified patch into files and hunks. Never throws: a line
 * it does not understand outside a hunk is skipped. */
export function parsePatch(patch: string): DiffFile[] {
  const files: DiffFile[] = [];
  if (!patch) return files;
  const lines = patch.split("\n");
  // A patch that ends in "\n" leaves one empty string behind; it is not a line.
  if (lines.length > 0 && lines[lines.length - 1] === "") lines.pop();

  let file: DiffFile | null = null;
  let hunk: DiffHunk | null = null;
  let oldLeft = 0;
  let newLeft = 0;
  let oldNo = 0;
  let newNo = 0;
  let inBinaryPatch = false;

  const close = () => {
    if (file) files.push(finish(file));
    file = null;
    hunk = null;
    oldLeft = newLeft = 0;
    inBinaryPatch = false;
  };

  for (const rawLine of lines) {
    const line = rawLine.endsWith("\r") ? rawLine.slice(0, -1) : rawLine;

    // Inside a hunk, the counts say how many lines belong to it, so content
    // that happens to look like a header ("--- a", "diff --git") stays content.
    if (hunk && (oldLeft > 0 || newLeft > 0)) {
      const current: DiffHunk = hunk;
      const mark = line[0];
      const text = line.slice(1);
      if (mark === "+" ) {
        current.lines.push({ kind: "add", text, newNo: newNo++ });
        newLeft--;
        file!.additions++;
        continue;
      }
      if (mark === "-") {
        current.lines.push({ kind: "del", text, oldNo: oldNo++ });
        oldLeft--;
        file!.deletions++;
        continue;
      }
      if (mark === " " || line === "") {
        current.lines.push({ kind: "context", text, oldNo: oldNo++, newNo: newNo++ });
        oldLeft--;
        newLeft--;
        continue;
      }
      if (mark === "\\") {
        const last = current.lines[current.lines.length - 1];
        if (last) last.noNewline = true;
        continue;
      }
      // A malformed hunk: stop trusting its counts and read headers again.
      oldLeft = newLeft = 0;
    }

    if (line.startsWith("\\") && hunk) {
      const last = (hunk as DiffHunk).lines[(hunk as DiffHunk).lines.length - 1];
      if (last) last.noNewline = true;
      continue;
    }

    if (line.startsWith("diff --git ")) {
      close();
      const paths = parseGitHeaderPaths(line.slice("diff --git ".length));
      file = newFile(paths?.oldPath ?? "", paths?.newPath ?? "");
      continue;
    }
    if (!file) {
      // A bare "---"/"+++" pair without a git header starts a file too.
      if (line.startsWith("--- ")) {
        file = newFile(markerPath(line.slice(4), "a/"), "");
      }
      continue;
    }
    const current: DiffFile = file;
    if (inBinaryPatch) continue;

    const hunkMatch = HUNK_RE.exec(line);
    if (hunkMatch) {
      const oldStart = Number(hunkMatch[1]);
      const oldLines = hunkMatch[2] === undefined ? 1 : Number(hunkMatch[2]);
      const newStart = Number(hunkMatch[3]);
      const newLines = hunkMatch[4] === undefined ? 1 : Number(hunkMatch[4]);
      hunk = {
        header: line,
        oldStart,
        oldLines,
        newStart,
        newLines,
        section: hunkMatch[5] ?? "",
        lines: [],
      };
      current.hunks.push(hunk);
      oldLeft = oldLines;
      newLeft = newLines;
      oldNo = oldStart;
      newNo = newStart;
      continue;
    }
    if (line.startsWith("--- ")) {
      current.oldPath = markerPath(line.slice(4), "a/");
      continue;
    }
    if (line.startsWith("+++ ")) {
      current.newPath = markerPath(line.slice(4), "b/");
      continue;
    }
    if (line.startsWith("new file mode ")) {
      current.status = "added";
      current.newMode = line.slice("new file mode ".length);
      current.oldPath = DEV_NULL;
      continue;
    }
    if (line.startsWith("deleted file mode ")) {
      current.status = "deleted";
      current.oldMode = line.slice("deleted file mode ".length);
      current.newPath = DEV_NULL;
      continue;
    }
    if (line.startsWith("old mode ")) {
      current.oldMode = line.slice("old mode ".length);
      continue;
    }
    if (line.startsWith("new mode ")) {
      current.newMode = line.slice("new mode ".length);
      continue;
    }
    if (line.startsWith("rename from ")) {
      current.status = "renamed";
      current.oldPath = unquoteGitPath(line.slice("rename from ".length));
      continue;
    }
    if (line.startsWith("rename to ")) {
      current.status = "renamed";
      current.newPath = unquoteGitPath(line.slice("rename to ".length));
      continue;
    }
    if (line.startsWith("copy from ")) {
      current.oldPath = unquoteGitPath(line.slice("copy from ".length));
      continue;
    }
    if (line.startsWith("copy to ")) {
      current.status = "added";
      current.newPath = unquoteGitPath(line.slice("copy to ".length));
      continue;
    }
    if (line.startsWith("Binary files ") && line.endsWith(" differ")) {
      current.binary = true;
      continue;
    }
    if (line === "GIT binary patch") {
      current.binary = true;
      inBinaryPatch = true;
      continue;
    }
    // "index …", "similarity index …", "dissimilarity index …": nothing to show.
  }
  close();
  return files;
}

/** One row of a side-by-side view: the old line on the left, the new on the
 * right; either side may be empty. */
export interface SplitRow {
  left?: DiffLine;
  right?: DiffLine;
}

/** Pairs a hunk's lines for a side-by-side view: context on both sides, and a
 * run of removals followed by a run of additions side by side, row by row. */
export function splitRows(lines: DiffLine[]): SplitRow[] {
  const rows: SplitRow[] = [];
  let i = 0;
  while (i < lines.length) {
    const line = lines[i];
    if (line.kind === "context") {
      rows.push({ left: line, right: line });
      i++;
      continue;
    }
    const dels: DiffLine[] = [];
    const adds: DiffLine[] = [];
    while (i < lines.length && lines[i].kind === "del") dels.push(lines[i++]);
    while (i < lines.length && lines[i].kind === "add") adds.push(lines[i++]);
    const count = Math.max(dels.length, adds.length);
    for (let k = 0; k < count; k++) rows.push({ left: dels[k], right: adds[k] });
  }
  return rows;
}

/** Finds the parsed diff for a file the run reported, by either of its paths. */
export function findDiffFile(
  files: DiffFile[],
  path: string,
  oldPath?: string,
): DiffFile | undefined {
  return (
    files.find((file) => file.path === path) ??
    files.find((file) => file.newPath === path || file.oldPath === path) ??
    (oldPath ? files.find((file) => file.oldPath === oldPath) : undefined)
  );
}
