// Boots the real app for the demo videos (playwright.demo.config.ts):
//   vite build → go build → serve a throwaway workspace seeded with an
// English project whose tasks have dependencies, so the board looks like a
// real piece of work rather than two test nodes, and a few smaller ones.

import { spawn, spawnSync } from "node:child_process";
import { existsSync, mkdtempSync, mkdirSync, readFileSync, writeFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";

const port = process.env.VISED_E2E_PORT ?? "5702";
const webDir = resolve(import.meta.dirname, "..");
const repoDir = resolve(webDir, "..");

function run(command, args, cwd) {
  const result = spawnSync(command, args, { cwd, stdio: "inherit", shell: true });
  if (result.status !== 0) {
    throw new Error(`${command} ${args.join(" ")} failed (${result.status})`);
  }
}

run("npx", ["vite", "build"], webDir);

const binary = join(webDir, "e2e", ".bin", process.platform === "win32" ? "nodevas.exe" : "nodevas");
mkdirSync(join(webDir, "e2e", ".bin"), { recursive: true });
run("go", ["build", "-tags", "nomsgpack", "-o", binary, "./cmd/nodevas"], repoDir);

const nodes = [
  ["goal", "Ship the rate-limited public API", "", "The public API gets per-key quotas before launch."],
  ["schema", "Design the quota table", "", "One row per API key: limit, window, used."],
  ["limiter", "Add rate limiting middleware", "schema", "Token bucket in front of every /api route; 429 with Retry-After."],
  ["dashboard", "Show quota in the dashboard", "limiter", "A meter on the keys page."],
  ["docs", "Document the limits", "limiter", "Limits, headers and how to ask for more."],
];

// The workspace is named after its folder, which shows in the explorer.
const scratch = mkdtempSync(join(tmpdir(), "nodevas-demo-"));
const workspace = join(scratch, "Workspace");
const project = join(workspace, "public-api");
mkdirSync(join(project, "nodes"), { recursive: true });
const lines = ["version: 1", "nodes:"];
for (const [id, title, requires] of nodes) {
  lines.push(`  - id: ${id}`, `    title: ${title}`, "    kind: task");
  if (requires) lines.push(`    requires: ${requires}`);
}
lines.push("edges:");
for (const [id, , requires] of nodes) {
  if (requires) lines.push(`  - from: ${requires}`, `    to: ${id}`);
}
lines.push("  - from: dashboard", "    to: goal", "  - from: docs", "    to: goal", "");
writeFileSync(join(project, "graph.yaml"), lines.join("\n"), "utf8");
for (const [id, title, , body] of nodes) {
  writeFileSync(join(project, "nodes", `${id}.md`), `# ${title}\n\n${body}\n`, "utf8");
}

// The workspace's other projects: the explorer lists them, and Station's
// building draws an office for each (demo-stills.spec.ts fills those in).
const others = {
  "billing-app": ["Invoice PDF", "Tax rules", "Refunds"],
  handbook: ["Onboarding guide", "Code review norms"],
  "mobile-app": ["Sign-in screen", "Push notifications"],
  site: ["Pricing page", "Changelog"],
};
for (const [name, titles] of Object.entries(others)) {
  const dir = join(workspace, name);
  mkdirSync(join(dir, "nodes"), { recursive: true });
  const ids = titles.map((title) => title.toLowerCase().replace(/[^a-z0-9]+/g, "-"));
  const graph = ["version: 1", "nodes:"];
  titles.forEach((title, i) => graph.push(`  - id: ${ids[i]}`, `    title: ${title}`, "    kind: task"));
  graph.push("edges: []", "");
  writeFileSync(join(dir, "graph.yaml"), graph.join("\n"), "utf8");
  titles.forEach((title, i) => writeFileSync(join(dir, "nodes", `${ids[i]}.md`), `# ${title}\n`, "utf8"));
}

// Keep the workspace catalog away from the developer's real config.
const configDir = join(scratch, "config");
mkdirSync(configDir, { recursive: true });

// PostgreSQL, as e2e/serve.mjs: NODEVAS_E2E_DATABASE_URL names a server, or
// a throwaway one (`nodevas db test-server`) runs in the scratch directory
// and stops when its stdin closes. The demo gets a database of its own on it.
let dbServer = null;
let adminURL = process.env.NODEVAS_E2E_DATABASE_URL;
if (!adminURL) {
  const urlFile = join(scratch, "pg-url");
  dbServer = spawn(binary, ["db", "test-server", "--dir", join(scratch, "pg"), "--url-file", urlFile], {
    stdio: ["pipe", "inherit", "inherit"],
  });
  const deadline = Date.now() + 180_000;
  while (!existsSync(urlFile) || readFileSync(urlFile, "utf8").trim() === "") {
    if (dbServer.exitCode !== null || Date.now() > deadline) {
      throw new Error("nodevas db test-server did not start");
    }
    await new Promise((r) => setTimeout(r, 200));
  }
  adminURL = readFileSync(urlFile, "utf8").trim();
}
const databaseName = `demo_${process.pid}`;
const created = spawnSync(binary, ["db", "create-database", "--url", adminURL, "--name", databaseName], {
  stdio: "inherit",
});
if (created.status !== 0) {
  throw new Error(`nodevas db create-database failed (${created.status})`);
}
const databaseURL = new URL(adminURL);
databaseURL.pathname = `/${databaseName}`;

console.log(`[demo] workspace ${workspace}`);
const server = spawn(binary, ["serve", "--project", workspace, "--port", port, "--no-runner"], {
  stdio: "inherit",
  env: {
    ...process.env,
    APPDATA: configDir,
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
    rmSync(scratch, { recursive: true, force: true });
  } catch {
    /* best effort */
  }
};
process.on("SIGINT", cleanup);
process.on("SIGTERM", cleanup);
process.on("exit", cleanup);
server.on("exit", (code) => process.exit(code ?? 0));
