import assert from "node:assert/strict";
import { readdirSync, readFileSync } from "node:fs";
import { join, resolve } from "node:path";
import { test } from "node:test";

// actions/checkout leaves the job's token in the clone's git config unless
// told not to, where every later step can read it: npm ci, the web build,
// electron-builder. A job whose token can write (publish a release, push an
// image) checks out with persist-credentials: false, and hands the token
// only to the step that needs it.

const workflows = resolve(import.meta.dirname, "..", "..", ".github", "workflows");

const indent = (line) => line.length - line.trimStart().length;
const code = (line) => line.replace(/(^|\s)#.*$/, "");
const blank = (line) => code(line).trim() === "";

/** The line at i and every line nested under it. */
function block(lines, i) {
  let end = i + 1;
  while (end < lines.length && (blank(lines[end]) || indent(lines[end]) > indent(lines[i]))) end += 1;
  return lines.slice(i, end);
}

/** Whether the permissions key at i grants any write scope. */
const grantsWrite = (lines, i) => block(lines, i).some((line) => /:\s*write\b|\bwrite-all\b/.test(code(line)));

/**
 * The jobs of a workflow whose token can write: { name, start, body }, start
 * being the job's line index. A job without permissions of its own gets the
 * workflow's; a workflow without any gets the repository's default, which
 * may be write.
 */
function writingJobs(text) {
  const lines = text.split(/\r?\n/);
  const top = lines.findIndex((line) => /^permissions:/.test(line));
  const workflowWrites = top === -1 || grantsWrite(lines, top);
  const jobs = lines.findIndex((line) => /^jobs:/.test(line));
  const found = [];
  if (jobs === -1) return found;
  for (let j = jobs + 1; j < lines.length; j += 1) {
    const job = /^ {2}([\w-]+):\s*$/.exec(code(lines[j]));
    if (!job) continue;
    const body = block(lines, j);
    const own = body.findIndex((line) => /^ {4}permissions:/.test(line));
    const writes = own === -1 ? workflowWrites : grantsWrite(body, own);
    if (writes) found.push({ name: job[1], start: j, body });
  }
  return found;
}

/** The checkouts of a workflow that keep a token able to write: "job: line". */
export function checkoutsKeepingWriteToken(text) {
  const found = [];
  for (const { name, start, body } of writingJobs(text)) {
    body.forEach((line, k) => {
      if (!/^\s*(- )?uses:\s*actions\/checkout@/.test(code(line))) return;
      // The step: the "- " line the uses key belongs to.
      let step = k;
      while (step > 0 && !code(body[step]).trimStart().startsWith("- ")) step -= 1;
      const persists = !block(body, step).some((l) => /^\s*persist-credentials:\s*false\b/.test(code(l)));
      if (persists) found.push(`${name}: line ${start + k + 1}`);
    });
  }
  return found;
}

// persist-credentials only keeps the token out of git config. The runner
// holds a job's token in memory from the job's first step, and every step
// runs as an administrator (Windows) or with sudo (macOS, Linux): a
// compromised build dependency can read it there, as the tj-actions
// compromise did. So a job whose token can write runs no project or
// dependency code: building happens in a job that can only read, and a
// separate job publishes what it built.
const buildTool = /(^|[\s;&|(])(npm|npx|pnpm|yarn|node|go|electron-builder)\s/;

/** The run steps of jobs able to write that start a build tool: "job: line". */
export function writersRunningBuildCode(text) {
  const found = [];
  for (const { name, start, body } of writingJobs(text)) {
    body.forEach((line, k) => {
      if (!/^\s*(- )?run:/.test(code(line))) return;
      const script = block(body, k).map((l, i) => {
        const text = code(l);
        return i === 0 ? text.replace(/^\s*(- )?run:\s*[|>]?[-+]?/, " ") : text;
      });
      if (script.some((l) => buildTool.test(l))) found.push(`${name}: line ${start + k + 1}`);
    });
  }
  return found;
}

test("the check finds a checkout that keeps a token able to write", () => {
  const workflow = (permissions, withBlock) =>
    [
      "permissions:",
      `  contents: ${permissions}`,
      "jobs:",
      "  build:",
      "    runs-on: ubuntu-latest",
      "    steps:",
      "      - name: Check out source",
      "        uses: actions/checkout@0123456789abcdef0123456789abcdef01234567 # v7",
      ...withBlock,
      "      - run: npm ci",
    ].join("\n");
  assert.deepEqual(checkoutsKeepingWriteToken(workflow("write", [])), ["build: line 8"]);
  assert.deepEqual(checkoutsKeepingWriteToken(workflow("write", ["        with:", "          fetch-depth: 0"])), [
    "build: line 8",
  ]);
  assert.deepEqual(
    checkoutsKeepingWriteToken(workflow("write", ["        with:", "          persist-credentials: false"])),
    [],
  );
  assert.deepEqual(checkoutsKeepingWriteToken(workflow("read", [])), []);
  // A job's own permissions win over the workflow's.
  const own = workflow("read", []).replace("    runs-on:", "    permissions:\n      packages: write\n    runs-on:");
  assert.deepEqual(checkoutsKeepingWriteToken(own), ["build: line 10"]);
});

test("no job that can write leaves its token to the steps after checkout", () => {
  const found = [];
  for (const name of readdirSync(workflows).filter((file) => /\.ya?ml$/.test(file)).sort()) {
    for (const where of checkoutsKeepingWriteToken(readFileSync(join(workflows, name), "utf8"))) {
      found.push(`${name} ${where}`);
    }
  }
  assert.deepEqual(found, []);
});

test("the check finds a job able to write that runs build code", () => {
  const workflow = (permissions, steps) =>
    [
      "permissions:",
      `  contents: ${permissions}`,
      "jobs:",
      "  release:",
      "    runs-on: ubuntu-latest",
      "    steps:",
      "      - name: Set up Go",
      "        uses: actions/setup-go@0123456789abcdef0123456789abcdef01234567 # v7",
      "        with:",
      "          go-version-file: go.mod",
      ...steps,
    ].join("\n");
  const build = ["      - name: Build", "        run: |", "          mkdir -p out", "          CGO_ENABLED=0 go build ./cmd/x"];
  assert.deepEqual(writersRunningBuildCode(workflow("write", build)), ["release: line 12"]);
  assert.deepEqual(writersRunningBuildCode(workflow("write", ["      - run: npm ci --prefix web"])), [
    "release: line 11",
  ]);
  assert.deepEqual(writersRunningBuildCode(workflow("read", build)), []);
  // Publishing alone, and words that only look like a tool, are fine.
  const publish = [
    "      - name: Publish",
    "        run: |",
    "          # npm ci ran in the build job",
    '          gh release upload "${tag}" dist/nodevas-* --clobber',
    '          echo "go.mod and node_modules stay behind"',
  ];
  assert.deepEqual(writersRunningBuildCode(workflow("write", publish)), []);
  // A job's own permissions win over the workflow's.
  const own = workflow("write", build).replace("    runs-on:", "    permissions:\n      contents: read\n    runs-on:");
  assert.deepEqual(writersRunningBuildCode(own), []);
});

test("no job that can write runs project or dependency code", () => {
  const found = [];
  for (const name of readdirSync(workflows).filter((file) => /\.ya?ml$/.test(file)).sort()) {
    for (const where of writersRunningBuildCode(readFileSync(join(workflows, name), "utf8"))) {
      found.push(`${name} ${where}`);
    }
  }
  assert.deepEqual(found, []);
});
