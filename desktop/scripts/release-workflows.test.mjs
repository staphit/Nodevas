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
 * The jobs of a workflow: { name, start, body, writes }, start being the
 * job's line index and writes whether its token can write. A job without
 * permissions of its own gets the workflow's; a workflow without any gets
 * the repository's default, which may be write.
 */
function jobsOf(text) {
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
    found.push({ name: job[1], start: j, body, writes });
  }
  return found;
}

/** The jobs of a workflow whose token can write. */
const writingJobs = (text) => jobsOf(text).filter((job) => job.writes);

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

/** The run steps of a job that start a build tool: "job: line". */
function buildSteps({ name, start, body }) {
  const found = [];
  body.forEach((line, k) => {
    if (!/^\s*(- )?run:/.test(code(line))) return;
    const script = block(body, k).map((l, i) => {
      const text = code(l);
      return i === 0 ? text.replace(/^\s*(- )?run:\s*[|>]?[-+]?/, " ") : text;
    });
    if (script.some((l) => buildTool.test(l))) found.push(`${name}: line ${start + k + 1}`);
  });
  return found;
}

/** The run steps of jobs able to write that start a build tool: "job: line". */
export function writersRunningBuildCode(text) {
  return writingJobs(text).flatMap(buildSteps);
}

// Signing and notarization credentials (a Developer ID certificate and its
// password, an Apple ID app password) are worth more than any job token:
// they outlive the run, and whoever holds them can ship code as the
// project. Every step of a job can read the secrets the job is handed from
// the runner's memory, so a job that names one runs no project or
// dependency code: it checks nothing out, sets up no toolchain and starts
// no build tool. It signs, with Apple's own tools, what a job without
// secrets built. And it runs in the release environment, whose required
// reviewer approves every use: environment secrets reach only its jobs.
const secretRef = /\bsecrets\.(?!GITHUB_TOKEN\b)\w/;
const untrustedAction = /^\s*(- )?uses:\s*(\.\/|actions\/(checkout|setup-node|setup-go|setup-python|cache)@)/;

/** The jobs naming a secret other than their token. */
const secretHolders = (text) => jobsOf(text).filter(({ body }) => body.some((line) => secretRef.test(code(line))));

const byLine = (a, b) => Number(a.split(" line ")[1]) - Number(b.split(" line ")[1]);

/** The steps of jobs holding a secret that run project or dependency code: "job: line". */
export function secretHoldersRunningUntrustedCode(text) {
  const found = [];
  for (const job of secretHolders(text)) {
    job.body.forEach((line, k) => {
      if (untrustedAction.test(code(line))) found.push(`${job.name}: line ${job.start + k + 1}`);
    });
    found.push(...buildSteps(job));
  }
  return found.sort(byLine);
}

/** The jobs holding a secret outside the release environment. */
export function secretHoldersOutsideRelease(text) {
  return secretHolders(text)
    .filter(({ body }) => !body.some((line) => /^ {4}environment:\s*release\s*$/.test(code(line))))
    .map(({ name }) => name);
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

test("the check finds a job holding signing secrets that runs untrusted code", () => {
  const workflow = (steps, extra = []) =>
    [
      "permissions:",
      "  contents: read",
      "jobs:",
      "  sign:",
      "    runs-on: macos-14",
      ...extra,
      "    steps:",
      ...steps,
      "      - name: Sign",
      "        env:",
      "          CSC_LINK: ${{ secrets.MAC_CSC_LINK }}",
      '        run: codesign --sign "${identity}" app',
    ].join("\n");
  const checkout = ["      - uses: actions/checkout@0123456789abcdef0123456789abcdef01234567 # v7"];
  assert.deepEqual(secretHoldersRunningUntrustedCode(workflow(checkout)), ["sign: line 7"]);
  assert.deepEqual(secretHoldersRunningUntrustedCode(workflow(["      - run: npm ci --prefix desktop"])), [
    "sign: line 7",
  ]);
  const node = ["      - uses: actions/setup-node@0123456789abcdef0123456789abcdef01234567 # v7"];
  assert.deepEqual(secretHoldersRunningUntrustedCode(workflow(node)), ["sign: line 7"]);
  assert.deepEqual(secretHoldersRunningUntrustedCode(workflow(["      - uses: ./.github/actions/sign"])), [
    "sign: line 7",
  ]);
  // Downloading what another job built and signing it with Apple's tools is fine.
  const download = ["      - uses: actions/download-artifact@0123456789abcdef0123456789abcdef01234567 # v8"];
  assert.deepEqual(secretHoldersRunningUntrustedCode(workflow(download)), []);
  // The job's own token is not such a secret.
  const token = workflow(checkout).replace("secrets.MAC_CSC_LINK", "secrets.GITHUB_TOKEN");
  assert.deepEqual(secretHoldersRunningUntrustedCode(token), []);
  assert.deepEqual(secretHoldersOutsideRelease(workflow(download)), ["sign"]);
  assert.deepEqual(secretHoldersOutsideRelease(workflow(download, ["    environment: release"])), []);
});

test("no job holding signing secrets runs project or dependency code", () => {
  const found = [];
  for (const name of readdirSync(workflows).filter((file) => /\.ya?ml$/.test(file)).sort()) {
    const text = readFileSync(join(workflows, name), "utf8");
    for (const where of secretHoldersRunningUntrustedCode(text)) found.push(`${name} ${where}`);
    for (const job of secretHoldersOutsideRelease(text)) found.push(`${name} ${job}: outside the release environment`);
  }
  assert.deepEqual(found, []);
});

// Signing left the build job; the release still publishes what was signed,
// not the unsigned build.
test("the macOS release publishes what its signing job made", () => {
  const text = readFileSync(join(workflows, "release-macos.yml"), "utf8");
  const jobs = Object.fromEntries(jobsOf(text).map((job) => [job.name, job.body.join("\n")]));
  assert.ok(jobs["sign-macos"], "release-macos.yml has a sign-macos job");
  assert.match(jobs["sign-macos"], /needs:\s*build-macos/);
  assert.match(jobs["publish-macos"], /needs:[^\n]*sign-macos/);
  assert.match(jobs["publish-macos"], /name:\s*macos-signed/);
  assert.doesNotMatch(jobs["build-macos"], /secrets\./, "the build job is handed no secret");
});
