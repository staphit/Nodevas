import { spawnSync } from "node:child_process";
import { mkdirSync } from "node:fs";
import { join, resolve } from "node:path";

const e2eDir = import.meta.dirname;
const webDir = resolve(e2eDir, "..");
const repoDir = resolve(webDir, "..");

function run(command: string, args: string[], cwd: string) {
  const result = spawnSync(command, args, { cwd, stdio: "inherit", shell: true });
  if (result.status !== 0) {
    throw new Error(`${command} ${args.join(" ")} failed (${result.status})`);
  }
}

/**
 * Builds the app once for the whole run: vite build → go build. Each worker
 * then boots its own server from the shared binary (see fixture.ts), so the
 * expensive part is never repeated per worker.
 */
export default function globalSetup() {
  run("npx", ["vite", "build"], webDir);
  const binary = join(
    e2eDir,
    ".bin",
    process.platform === "win32" ? "nodevas.exe" : "nodevas",
  );
  mkdirSync(join(e2eDir, ".bin"), { recursive: true });
  run("go", ["build", "-tags", "nomsgpack", "-o", binary, "./cmd/nodevas"], repoDir);
}
