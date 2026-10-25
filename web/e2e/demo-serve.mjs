// Boots the real app for the demo videos (playwright.demo.config.ts):
//   vite build → go build → serve a throwaway workspace seeded with one
// English project whose tasks have dependencies, so the board looks like a
// real piece of work rather than two test nodes.

import { spawn, spawnSync } from "node:child_process";
import { mkdtempSync, mkdirSync, writeFileSync, rmSync } from "node:fs";
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

// Keep the workspace catalog away from the developer's real config.
const configDir = join(scratch, "config");
mkdirSync(configDir, { recursive: true });

console.log(`[demo] workspace ${workspace}`);
const server = spawn(binary, ["serve", "--project", workspace, "--port", port], {
  stdio: "inherit",
  env: {
    ...process.env,
    APPDATA: configDir,
    XDG_CONFIG_HOME: configDir,
    HOME: configDir,
  },
});

const cleanup = () => {
  server.kill();
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
