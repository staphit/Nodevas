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
 * The checkouts of a workflow that keep a token able to write: "job: line".
 * A job without permissions of its own gets the workflow's; a workflow
 * without any gets the repository's default, which may be write.
 */
export function checkoutsKeepingWriteToken(text) {
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
    if (!writes) continue;
    body.forEach((line, k) => {
      if (!/^\s*(- )?uses:\s*actions\/checkout@/.test(code(line))) return;
      // The step: the "- " line the uses key belongs to.
      let step = k;
      while (step > 0 && !code(body[step]).trimStart().startsWith("- ")) step -= 1;
      const persists = !block(body, step).some((l) => /^\s*persist-credentials:\s*false\b/.test(code(l)));
      if (persists) found.push(`${job[1]}: line ${j + k + 1}`);
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
