// Boots the real app for end-to-end tests:
//   vite build → go build (the binary embeds web/dist) → serve a temp workspace.
// The workspace is disposable, so tests may create, edit and delete freely.

import { spawn, spawnSync } from "node:child_process";
import { existsSync, mkdtempSync, mkdirSync, readFileSync, writeFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";

const port = process.env.VISED_E2E_PORT ?? "5700";
const webDir = resolve(import.meta.dirname, "..");
const repoDir = resolve(webDir, "..");

function run(command, args, cwd) {
  const result = spawnSync(command, args, { cwd, stdio: "inherit", shell: true });
  if (result.status !== 0) {
    throw new Error(`${command} ${args.join(" ")} failed (${result.status})`);
  }
}

run("npx", ["vite", "build"], webDir);

const binary = join(
  webDir,
  "e2e",
  ".bin",
  process.platform === "win32" ? "nodevas.exe" : "nodevas",
);
mkdirSync(join(webDir, "e2e", ".bin"), { recursive: true });
run("go", ["build", "-tags", "nomsgpack", "-o", binary, "./cmd/nodevas"], repoDir);

// Fixture workspace: one project with two nodes and one dependency.
const workspace = mkdtempSync(join(tmpdir(), "nodevas-e2e-"));
const project = join(workspace, "e2e-project");
mkdirSync(join(project, "nodes"), { recursive: true });
writeFileSync(
  join(project, "graph.yaml"),
  [
    "version: 1",
    "nodes:",
    "  - id: alpha",
    "    title: 設計稿",
    "    kind: task",
    "  - id: beta",
    "    title: 實作",
    "    kind: task",
    "    requires: alpha",
    "edges:",
    "  - from: alpha",
    "    to: beta",
    "",
  ].join("\n"),
  "utf8",
);
writeFileSync(join(project, "nodes", "alpha.md"), "# 設計稿\n\n初稿內容。\n", "utf8");
writeFileSync(join(project, "nodes", "beta.md"), "# 實作\n\n待辦。\n", "utf8");

// The workspace catalog (known workspaces, last active project) lives in the
// user config directory. Point that at the throwaway workspace so tests neither
// read nor write the developer's real setup, and cannot inherit state from a
// previous run.
const configDir = join(workspace, "config");
mkdirSync(configDir, { recursive: true });

// PostgreSQL: NODEVAS_E2E_DATABASE_URL names a server, or this starts the
// embedded one in the foreground (`nodevas db test-server`) and stops it on
// exit by closing its stdin. The app gets a database of its own on it.
let dbServer = null;
let adminURL = process.env.NODEVAS_E2E_DATABASE_URL;
if (!adminURL) {
  const urlFile = join(workspace, "pg-url");
  dbServer = spawn(
    binary,
    ["db", "test-server", "--dir", join(workspace, "pg"), "--url-file", urlFile],
    {
      stdio: ["pipe", "inherit", "inherit"],
    },
  );
  const deadline = Date.now() + 180_000;
  while (!existsSync(urlFile) || readFileSync(urlFile, "utf8").trim() === "") {
    if (dbServer.exitCode !== null || Date.now() > deadline) {
      throw new Error("nodevas db test-server did not start");
    }
    await new Promise((r) => setTimeout(r, 200));
  }
  adminURL = readFileSync(urlFile, "utf8").trim();
}
const databaseName = `e2e_serve_${process.pid}`;
// No shell: the URL may hold & and ?.
const created = spawnSync(
  binary,
  ["db", "create-database", "--url", adminURL, "--name", databaseName],
  {
    stdio: "inherit",
  },
);
if (created.status !== 0) {
  throw new Error(`nodevas db create-database failed (${created.status})`);
}
const databaseURL = new URL(adminURL);
databaseURL.pathname = `/${databaseName}`;

console.log(`[e2e] workspace ${workspace}`);
const server = spawn(binary, ["serve", "--project", workspace, "--port", port], {
  stdio: "inherit",
  env: {
    ...process.env,
    APPDATA: configDir,
    LOCALAPPDATA: configDir,
    XDG_CONFIG_HOME: configDir,
    HOME: configDir,
    NODEVAS_DATABASE_URL: databaseURL.toString(),
    NODEVAS_DATABASE_URL_FILE: "",
  },
});

let cleaned = false;
const cleanup = () => {
  if (cleaned) return;
  cleaned = true;
  server.kill();
  spawnSync(binary, ["db", "drop-database", "--url", adminURL, "--name", databaseName]);
  dbServer?.stdin?.end();
  try {
    rmSync(workspace, { recursive: true, force: true });
  } catch {
    /* best effort */
  }
};
process.on("SIGINT", cleanup);
process.on("SIGTERM", cleanup);
process.on("exit", cleanup);
server.on("exit", (code) => process.exit(code ?? 0));
