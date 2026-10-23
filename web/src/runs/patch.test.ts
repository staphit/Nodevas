import { describe, expect, it } from "vitest";
import {
  findDiffFile,
  parseGitHeaderPaths,
  parsePatch,
  splitRows,
  unquoteGitPath,
} from "./patch";

const MODIFY = `diff --git a/internal/server/routes.go b/internal/server/routes.go
index 3b18e51..a9c2f04 100644
--- a/internal/server/routes.go
+++ b/internal/server/routes.go
@@ -10,7 +10,7 @@ func routes(r *gin.Engine) {
 	r.GET("/api/runs", listRuns)
 	r.GET("/api/runs/:id", getRun)
-	r.POST("/api/runs/:id/cancel", cancelRun)
+	r.POST("/api/runs/:id/cancel", cancel)
+	r.GET("/api/runs/:id/changes", changes)
 	r.GET("/api/runs/:id/events", events)
 	// --- a comment that looks like a header
-- a removed line that starts with dashes
 }
`;

const ADD = `diff --git a/docs/new.md b/docs/new.md
new file mode 100644
index 0000000..e69de29
--- /dev/null
+++ b/docs/new.md
@@ -0,0 +1,2 @@
+# New
+Hello.
`;

const DELETE = `diff --git a/old.txt b/old.txt
deleted file mode 100644
index 5716ca5..0000000
--- a/old.txt
+++ /dev/null
@@ -1,2 +0,0 @@
-one
-two
`;

const RENAME = `diff --git a/src/a b c.ts b/src/d e f.ts
similarity index 90%
rename from src/a b c.ts
rename to src/d e f.ts
index 1111111..2222222 100644
--- a/src/a b c.ts
+++ b/src/d e f.ts
@@ -1 +1 @@
-export const x = 1;
+export const x = 2;
`;

const PURE_RENAME = `diff --git a/x.txt b/y.txt
similarity index 100%
rename from x.txt
rename to y.txt
`;

const BINARY = `diff --git a/logo.png b/logo.png
index 1111111..2222222 100644
Binary files a/logo.png and b/logo.png differ
`;

const GIT_BINARY_PATCH = `diff --git a/blob.bin b/blob.bin
new file mode 100644
index 0000000..3333333
GIT binary patch
literal 12
TcmZ?wbhEHbWMp6f0Dg)Ce*gdg

literal 0
HcmV?d00001

`;

const NO_NEWLINE = `diff --git a/a.txt b/a.txt
index 1111111..2222222 100644
--- a/a.txt
+++ b/a.txt
@@ -1,2 +1,2 @@
 keep
-old
\\ No newline at end of file
+new
\\ No newline at end of file
`;

const MULTI_HUNK = `diff --git a/m.go b/m.go
index 1111111..2222222 100644
--- a/m.go
+++ b/m.go
@@ -1,3 +1,3 @@ package m
 a
-b
+B
 c
@@ -20,2 +20,3 @@ func f() {
 x
+y
 z
`;

const MODE_ONLY = `diff --git a/run.sh b/run.sh
old mode 100644
new mode 100755
`;

describe("parsePatch", () => {
  it("parses a modified file with line numbers, keeping header-like content", () => {
    const [file] = parsePatch(MODIFY);
    expect(file.status).toBe("modified");
    expect(file.path).toBe("internal/server/routes.go");
    expect(file.hunks).toHaveLength(1);
    const hunk = file.hunks[0];
    expect(hunk.section).toBe("func routes(r *gin.Engine) {");
    expect(hunk.lines.map((line) => line.kind)).toEqual([
      "context",
      "context",
      "del",
      "add",
      "add",
      "context",
      "context",
      "del",
      "context",
    ]);
    expect(hunk.lines[2]).toMatchObject({ kind: "del", oldNo: 12 });
    expect(hunk.lines[3]).toMatchObject({ kind: "add", newNo: 12 });
    expect(hunk.lines[5]).toMatchObject({ oldNo: 13, newNo: 14 });
    // "-- a removed…" is the removal of "- a removed…", not a header.
    expect(hunk.lines[7]).toMatchObject({ kind: "del", text: "- a removed line that starts with dashes" });
    expect(file.additions).toBe(2);
    expect(file.deletions).toBe(2);
  });

  it("parses an added file", () => {
    const [file] = parsePatch(ADD);
    expect(file).toMatchObject({ status: "added", path: "docs/new.md", additions: 2, deletions: 0 });
    expect(file.hunks[0].lines.map((line) => line.newNo)).toEqual([1, 2]);
  });

  it("parses a deleted file under its old path", () => {
    const [file] = parsePatch(DELETE);
    expect(file).toMatchObject({ status: "deleted", path: "old.txt", deletions: 2 });
  });

  it("parses a rename with spaces in both paths", () => {
    const [file] = parsePatch(RENAME);
    expect(file).toMatchObject({
      status: "renamed",
      path: "src/d e f.ts",
      oldPath: "src/a b c.ts",
      newPath: "src/d e f.ts",
    });
    expect(file.hunks).toHaveLength(1);
  });

  it("parses a pure rename without hunks", () => {
    const [file] = parsePatch(PURE_RENAME);
    expect(file).toMatchObject({ status: "renamed", oldPath: "x.txt", path: "y.txt" });
    expect(file.hunks).toEqual([]);
  });

  it("marks binary files, with or without a binary patch", () => {
    const [png] = parsePatch(BINARY);
    expect(png).toMatchObject({ binary: true, path: "logo.png", status: "modified" });
    const [blob, after] = parsePatch(GIT_BINARY_PATCH + MODE_ONLY);
    expect(blob).toMatchObject({ binary: true, path: "blob.bin", status: "added", hunks: [] });
    // The binary payload does not swallow the next file.
    expect(after).toMatchObject({ path: "run.sh", oldMode: "100644", newMode: "100755" });
  });

  it("flags lines without a trailing newline", () => {
    const [file] = parsePatch(NO_NEWLINE);
    const lines = file.hunks[0].lines;
    expect(lines).toHaveLength(3);
    expect(lines[0].noNewline).toBeUndefined();
    expect(lines[1]).toMatchObject({ kind: "del", text: "old", noNewline: true });
    expect(lines[2]).toMatchObject({ kind: "add", text: "new", noNewline: true });
  });

  it("parses multiple hunks and multiple files in one patch", () => {
    const files = parsePatch(MODIFY + ADD + MULTI_HUNK + DELETE);
    expect(files.map((file) => file.path)).toEqual([
      "internal/server/routes.go",
      "docs/new.md",
      "m.go",
      "old.txt",
    ]);
    const multi = files[2];
    expect(multi.hunks.map((hunk) => [hunk.oldStart, hunk.newStart])).toEqual([
      [1, 1],
      [20, 20],
    ]);
    expect(multi.hunks[1].lines[1]).toMatchObject({ kind: "add", text: "y", newNo: 21 });
  });

  it("handles CRLF line endings and an empty patch", () => {
    expect(parsePatch("")).toEqual([]);
    const [file] = parsePatch(ADD.replace(/\n/g, "\r\n"));
    expect(file.hunks[0].lines.map((line) => line.text)).toEqual(["# New", "Hello."]);
  });

  it("decodes quoted paths", () => {
    const patch = `diff --git "a/caf\\303\\251.txt" "b/caf\\303\\251.txt"
new file mode 100644
--- /dev/null
+++ "b/caf\\303\\251.txt"
@@ -0,0 +1 @@
+x
`;
    const [file] = parsePatch(patch);
    expect(file.path).toBe("café.txt");
    expect(file.status).toBe("added");
  });
});

describe("path helpers", () => {
  it("unquotes git C-style paths", () => {
    expect(unquoteGitPath('"a\\tb\\"c"')).toBe('a\tb"c');
    expect(unquoteGitPath("plain")).toBe("plain");
  });

  it("splits ambiguous headers where both halves agree", () => {
    expect(parseGitHeaderPaths("a/x b/y b/x b/y")).toEqual({ oldPath: "x b/y", newPath: "x b/y" });
    expect(parseGitHeaderPaths("a/one b/two")).toEqual({ oldPath: "one", newPath: "two" });
  });

  it("finds a parsed file by new or old path", () => {
    const files = parsePatch(RENAME + DELETE);
    expect(findDiffFile(files, "src/d e f.ts")?.status).toBe("renamed");
    expect(findDiffFile(files, "old.txt")?.status).toBe("deleted");
    expect(findDiffFile(files, "missing")).toBeUndefined();
  });
});

describe("splitRows", () => {
  it("pairs removals with additions and context with itself", () => {
    const [file] = parsePatch(MODIFY);
    const rows = splitRows(file.hunks[0].lines);
    // 2 context, del+add paired, lone add, 2 context, lone del, context.
    expect(rows).toHaveLength(8);
    expect(rows[2].left?.kind).toBe("del");
    expect(rows[2].right?.kind).toBe("add");
    expect(rows[3].left).toBeUndefined();
    expect(rows[3].right?.text).toContain("/changes");
    expect(rows[6].left?.kind).toBe("del");
    expect(rows[6].right).toBeUndefined();
  });
});
