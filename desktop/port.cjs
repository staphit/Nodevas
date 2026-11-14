"use strict";

// The local backend's port, kept from one launch to the next. The window's
// origin is http://127.0.0.1:<port>, and what the browser keeps per origin
// (localStorage, among it the warm copy of the person's preferences) goes
// with it: a new port each launch would start the page from nothing.
//
// The port is picked once in 20000-29999, below the ranges Windows
// (49152-65535) and Linux (32768-60999) hand to outgoing connections, so no
// connection takes it by chance, and saved as backend-port.json in the app's
// data folder. Later launches reuse it, and pick again only when another
// program holds it or the system refuses it (a Windows excluded port range),
// saving the new one.

const fs = require("node:fs");
const net = require("node:net");
const path = require("node:path");

const PORT_FILE = "backend-port.json";
const PORT_MIN = 20000;
const PORT_MAX = 29999;
// A saved port that is busy may be the last backend still letting go of
// it: it is tried again this many times, this far apart, before another
// is picked.
const BUSY_RETRIES = 3;
const BUSY_RETRY_DELAY_MS = 1000;
// Random ports tried before the system is asked for any free one.
const PICK_ATTEMPTS = 40;

function isValidPort(value) {
  return Number.isInteger(value) && value >= PORT_MIN && value <= PORT_MAX;
}

function portFile(dir) {
  return path.join(dir, PORT_FILE);
}

// readSavedPort is the port saved in dir, or null when there is none, the
// file does not parse, or the port in it is not one this app would pick.
async function readSavedPort(dir) {
  let text;
  try {
    text = await fs.promises.readFile(portFile(dir), "utf8");
  } catch {
    return null;
  }
  try {
    const port = JSON.parse(text)?.port;
    return isValidPort(port) ? port : null;
  } catch {
    return null;
  }
}

// savePort writes {"port": n} to a temporary file renamed over the old one,
// so a crash mid-write never leaves half a file behind.
async function savePort(dir, port) {
  await fs.promises.mkdir(dir, { recursive: true });
  const file = portFile(dir);
  const temporary = `${file}.${process.pid}.tmp`;
  await fs.promises.writeFile(temporary, `${JSON.stringify({ port })}\n`);
  await fs.promises.rename(temporary, file);
}

// probePort listens on host:port for a moment, as the backend will: "free"
// when it could, "refused" when the system does not allow the port
// (EACCES), "busy" otherwise (EADDRINUSE: another program holds it).
function probePort(port, host = "127.0.0.1") {
  return new Promise((resolve) => {
    const server = net.createServer();
    server.unref();
    server.once("error", (error) => resolve(error?.code === "EACCES" ? "refused" : "busy"));
    server.listen({ port, host }, () => server.close(() => resolve("free")));
  });
}

// anyFreePort is a port the system picks, for when no port in the range
// could be had.
function anyFreePort(host = "127.0.0.1") {
  return new Promise((resolve, reject) => {
    const server = net.createServer();
    server.unref();
    server.once("error", reject);
    server.listen({ port: 0, host }, () => {
      const address = server.address();
      const port = typeof address === "object" && address ? address.port : 0;
      server.close((error) => (error ? reject(error) : resolve(port)));
    });
  });
}

const wait = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

// pickPort is the port to start the backend on, and where it came from:
// "saved" when the saved port is free (waiting out a backend that is still
// releasing it), "picked" for a random free one in the range, "system" for
// one the system chose when none in the range could be had. reason says
// why the saved port was not used: "busy", "refused", or "none" (no valid
// saved port). options replace the probe, the randomness and the waiting,
// for tests.
async function pickPort(saved, options = {}) {
  const probe = options.probe ?? probePort;
  const random = options.random ?? Math.random;
  const sleep = options.sleep ?? wait;
  const any = options.anyPort ?? anyFreePort;
  const retries = options.retries ?? BUSY_RETRIES;
  const retryDelay = options.retryDelayMs ?? BUSY_RETRY_DELAY_MS;
  let reason = "none";
  if (isValidPort(saved)) {
    let state = await probe(saved);
    for (let attempt = 0; state === "busy" && attempt < retries; attempt += 1) {
      await sleep(retryDelay);
      state = await probe(saved);
    }
    if (state === "free") return { port: saved, source: "saved", reason: "" };
    reason = state;
  }
  const tried = new Set([saved]);
  for (let attempt = 0; attempt < PICK_ATTEMPTS; attempt += 1) {
    const port = PORT_MIN + Math.floor(random() * (PORT_MAX - PORT_MIN + 1));
    if (tried.has(port)) continue;
    tried.add(port);
    if ((await probe(port)) === "free") return { port, source: "picked", reason };
  }
  return { port: await any(), source: "system", reason };
}

// backendPort is the port for this launch's backend: the one saved in dir
// when it can be had, else a new one, saved for the next launch. A port the
// system chose is not saved (the next launch tries the range again), and a
// failed save only costs the next launch a new port.
async function backendPort(dir, options = {}) {
  const log = options.log;
  const saved = await readSavedPort(dir);
  const picked = await pickPort(saved, options);
  if (picked.source === "saved") return picked.port;
  if (picked.source === "picked") {
    try {
      await savePort(dir, picked.port);
    } catch (error) {
      log?.warn("Could not save the backend port", { port: picked.port, error: String(error) });
    }
  }
  log?.info("Backend port picked", { port: picked.port, previous: saved, reason: picked.reason, source: picked.source });
  return picked.port;
}

module.exports = {
  PORT_FILE,
  PORT_MIN,
  PORT_MAX,
  BUSY_RETRIES,
  isValidPort,
  readSavedPort,
  savePort,
  probePort,
  pickPort,
  backendPort,
};
