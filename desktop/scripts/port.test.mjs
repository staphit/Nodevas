import assert from "node:assert/strict";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { test } from "node:test";
import port from "../port.cjs";

const { PORT_FILE, PORT_MIN, PORT_MAX, BUSY_RETRIES, readSavedPort, savePort, probePort, pickPort, backendPort } = port;

function tempDir(t) {
  const dir = mkdtempSync(join(tmpdir(), "nodevas-port-"));
  t.after(() => rmSync(dir, { recursive: true, force: true }));
  return dir;
}

// A probe that answers from a script: states[port] is a list of answers,
// the last one repeating; a port not named is free.
function scripted(states) {
  const calls = [];
  const probe = async (p) => {
    calls.push(p);
    const answers = states[p];
    if (!answers) return "free";
    return answers.length > 1 ? answers.shift() : answers[0];
  };
  return { probe, calls };
}

// random() values that land on the given ports, in order (mid-slot, so no
// rounding moves them).
function landing(...ports) {
  const values = ports.map((p) => (p - PORT_MIN + 0.5) / (PORT_MAX - PORT_MIN + 1));
  return () => values.shift() ?? 0.5;
}

const noSleep = { sleep: async () => {}, anyPort: async () => 41234 };

test("a saved port that is free is used again", async () => {
  const { probe, calls } = scripted({});
  const picked = await pickPort(23817, { probe, ...noSleep });
  assert.deepEqual(picked, { port: 23817, source: "saved", reason: "" });
  assert.deepEqual(calls, [23817]);
});

test("a busy saved port is waited for, as a backend still letting go of it", async () => {
  const slept = [];
  const { probe, calls } = scripted({ 23817: ["busy", "busy", "free"] });
  const picked = await pickPort(23817, { probe, sleep: async (ms) => slept.push(ms), anyPort: noSleep.anyPort });
  assert.deepEqual(picked, { port: 23817, source: "saved", reason: "" });
  assert.deepEqual(calls, [23817, 23817, 23817]);
  assert.deepEqual(slept, [1000, 1000]);
});

test("a saved port still busy after the retries is given up for a new one", async () => {
  const { probe, calls } = scripted({ 23817: ["busy"], 25000: ["busy"] });
  const picked = await pickPort(23817, { probe, random: landing(25000, 26001), ...noSleep });
  assert.deepEqual(picked, { port: 26001, source: "picked", reason: "busy" });
  assert.deepEqual(calls, [...Array(BUSY_RETRIES + 1).fill(23817), 25000, 26001]);
});

test("a port the system refuses (a Windows excluded range) is not retried", async () => {
  const { probe, calls } = scripted({ 23817: ["refused"] });
  const picked = await pickPort(23817, { probe, random: landing(23817, 20000), ...noSleep });
  assert.deepEqual(picked, { port: 20000, source: "picked", reason: "refused" });
  // Once for the saved port; the random pick that landed on it again is skipped.
  assert.deepEqual(calls, [23817, 20000]);
});

test("no saved port: a random one in 20000-29999", async () => {
  for (const saved of [null, undefined, 80, 51000, "23817", 23817.5]) {
    const { probe, calls } = scripted({});
    const picked = await pickPort(saved, { probe, random: landing(PORT_MAX), ...noSleep });
    assert.deepEqual(picked, { port: PORT_MAX, source: "picked", reason: "none" }, String(saved));
    assert.deepEqual(calls, [PORT_MAX], String(saved));
  }
});

test("when nothing in the range can be had, the system picks one", async () => {
  const picked = await pickPort(null, { probe: async () => "busy", random: Math.random, ...noSleep });
  assert.deepEqual(picked, { port: 41234, source: "system", reason: "none" });
});

test("the saved file: missing, broken or out of range reads as none", async (t) => {
  const dir = tempDir(t);
  assert.equal(await readSavedPort(dir), null, "missing");
  for (const text of ["", "{", "null", "[]", '{"port":"23817"}', '{"port":80}', '{"port":30000}', '{"port":23817.5}']) {
    writeFileSync(join(dir, PORT_FILE), text);
    assert.equal(await readSavedPort(dir), null, text);
  }
  writeFileSync(join(dir, PORT_FILE), '{"port":23817}\n');
  assert.equal(await readSavedPort(dir), 23817);
});

test("savePort writes the file readSavedPort reads, in a folder it makes", async (t) => {
  const dir = join(tempDir(t), "userData");
  await savePort(dir, 21000);
  assert.deepEqual(JSON.parse(readFileSync(join(dir, PORT_FILE), "utf8")), { port: 21000 });
  assert.equal(await readSavedPort(dir), 21000);
});

test("backendPort keeps a free saved port and saves a new one", async (t) => {
  const dir = tempDir(t);
  const lines = [];
  const log = { info: (...args) => lines.push(args), warn: (...args) => lines.push(args) };

  // First launch: nothing saved, one is picked and saved.
  const first = await backendPort(dir, { log, probe: async () => "free", random: landing(22222), ...noSleep });
  assert.equal(first, 22222);
  assert.equal(await readSavedPort(dir), 22222);
  assert.equal(lines.length, 1);

  // Next launch: the same port, nothing written or logged.
  const again = await backendPort(dir, { log, probe: async () => "free", random: landing(24444), ...noSleep });
  assert.equal(again, 22222);
  assert.equal(lines.length, 1);

  // Taken by another program: a new one, saved for the launch after.
  const { probe } = scripted({ 22222: ["busy"] });
  const moved = await backendPort(dir, { log, probe, random: landing(24444), ...noSleep });
  assert.equal(moved, 24444);
  assert.equal(await readSavedPort(dir), 24444);
  assert.equal(lines.at(-1)[1].previous, 22222);
  assert.equal(lines.at(-1)[1].reason, "busy");

  // A port the system chose is used once, not saved.
  const system = await backendPort(dir, { log, probe: async () => "busy", random: Math.random, ...noSleep });
  assert.equal(system, 41234);
  assert.equal(await readSavedPort(dir), 24444);
});

// The real probe against a real listener.
test("probePort tells a free port from one another program holds", async (t) => {
  const holder = createServer();
  await new Promise((resolve) => holder.listen(0, "127.0.0.1", resolve));
  t.after(() => holder.close());
  const held = holder.address().port;
  assert.equal(await probePort(held), "busy");

  const spare = createServer();
  await new Promise((resolve) => spare.listen(0, "127.0.0.1", resolve));
  const free = spare.address().port;
  await new Promise((resolve) => spare.close(resolve));
  assert.equal(await probePort(free), "free");
});

test("pickPort moves off a saved port another program holds", async (t) => {
  let holder;
  let held = 0;
  // Hold a port in the range, as another program would.
  for (let candidate = PORT_MIN + 4321; candidate < PORT_MAX && !held; candidate += 97) {
    holder = createServer();
    held = await new Promise((resolve) => {
      holder.once("error", () => resolve(0));
      holder.listen(candidate, "127.0.0.1", () => resolve(candidate));
    });
  }
  assert.ok(held, "no port in the range could be held for the test");
  t.after(() => holder.close());
  const picked = await pickPort(held, { retries: 1, retryDelayMs: 10 });
  assert.notEqual(picked.port, held);
  assert.equal(picked.source, "picked");
  assert.equal(picked.reason, "busy");
  assert.ok(picked.port >= PORT_MIN && picked.port <= PORT_MAX, String(picked.port));
});
