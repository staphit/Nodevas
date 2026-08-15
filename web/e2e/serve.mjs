// Boots the real app for end-to-end tests:
//   vite build → go build (the binary embeds web/dist) → serve a temp workspace.
// The workspace is disposable, so tests may create, edit and delete freely.

import { spawn, spawnSync } from "node:child_process";
import { mkdtempSync, mkdirSync, writeFileSync, rmSync } from "node:fs";
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

const binary = join(webDir, "e2e", ".bin", process.platform === "win32" ? "nodevas.exe" : "nodevas");
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

console.log(`[e2e] workspace ${workspace}`);
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
    rmSync(workspace, { recursive: true, force: true });
  } catch {
    /* best effort */
  }
};
process.on("SIGINT", cleanup);
process.on("SIGTERM", cleanup);
process.on("exit", cleanup);
server.on("exit", (code) => process.exit(code ?? 0));
