import { spawn, spawnSync, type ChildProcess } from "node:child_process";
import { existsSync, mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

/**
 * PostgreSQL for the e2e run: one server shared by every worker, one database
 * per worker. NODEVAS_E2E_DATABASE_URL names an existing server (CI's service
 * container); otherwise global-setup starts `nodevas db test-server`, the
 * embedded PostgreSQL in the foreground, and stops it at teardown by closing
 * its stdin. Databases are made and dropped by the binary itself, so the web
 * package needs no PostgreSQL client.
 */
export const databaseEnv = "NODEVAS_E2E_DATABASE_URL";

export function nodevasBinary(): string {
  return join(
    import.meta.dirname,
    ".bin",
    process.platform === "win32" ? "nodevas.exe" : "nodevas",
  );
}

/** The admin URL global-setup published for this run. */
export function adminURL(): string {
  const url = process.env[databaseEnv];
  if (!url) {
    throw new Error(`${databaseEnv} is not set: global-setup did not start a database`);
  }
  return url;
}

/** The URL of one database on the shared server. */
export function databaseURL(name: string): string {
  const url = new URL(adminURL());
  url.pathname = `/${name}`;
  return url.toString();
}

function db(args: string[]): void {
  const result = spawnSync(nodevasBinary(), ["db", ...args], {
    encoding: "utf8",
  });
  if (result.status !== 0) {
    throw new Error(
      `nodevas db ${args[0]} failed (${result.status}): ${result.stderr}${result.stdout}`,
    );
  }
}

/** Creates an empty database on the shared server and returns its URL. */
export function createDatabase(name: string): string {
  db(["create-database", "--url", adminURL(), "--name", name]);
  return databaseURL(name);
}

export function dropDatabase(name: string): void {
  try {
    db(["drop-database", "--url", adminURL(), "--name", name]);
  } catch {
    /* best effort */
  }
}

/**
 * Starts the run's server unless one was given. Returns the teardown.
 */
export async function startSharedDatabase(): Promise<() => Promise<void>> {
  if (process.env[databaseEnv]) {
    return async () => {};
  }
  const dir = mkdtempSync(join(tmpdir(), "nodevas-e2e-pg-"));
  const urlFile = join(dir, "url");
  const server: ChildProcess = spawn(
    nodevasBinary(),
    ["db", "test-server", "--dir", join(dir, "cluster"), "--url-file", urlFile],
    { stdio: ["pipe", "inherit", "inherit"] },
  );
  const deadline = Date.now() + 180_000;
  while (!existsSync(urlFile) || readFileSync(urlFile, "utf8").trim() === "") {
    if (server.exitCode !== null) {
      throw new Error(`nodevas db test-server exited early (${server.exitCode})`);
    }
    if (Date.now() > deadline) {
      server.kill();
      throw new Error("nodevas db test-server did not publish a URL");
    }
    await new Promise((r) => setTimeout(r, 200));
  }
  process.env[databaseEnv] = readFileSync(urlFile, "utf8").trim();
  return async () => {
    server.stdin?.end();
    await new Promise((resolve) => {
      server.once("exit", resolve);
      setTimeout(() => {
        server.kill();
        resolve(undefined);
      }, 30_000);
    });
    rmSync(dir, { recursive: true, force: true });
  };
}
