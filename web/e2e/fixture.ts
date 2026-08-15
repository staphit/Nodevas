import { test as base, expect, type Page } from "@playwright/test";
import { spawn } from "node:child_process";
import { mkdtempSync, mkdirSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

let counter = 0;
let previous: string | null = null;

/**
 * Each worker gets its own server on its own port with its own throwaway
 * workspace. The server holds one active project at a time, so tests sharing
 * a server would trample each other's project switches — separate instances
 * are what make parallel workers safe. The binary is built once in
 * global-setup.ts; booting another instance of it costs almost nothing.
 *
 * Ports start clear of 5700 (serve.mjs default) and 5701 (mobile preview) so
 * an e2e run never fights a preview run for a listener.
 */
export const test = base.extend<{}, { workerServer: { baseURL: string } }>({
  workerServer: [
    async ({}, use, workerInfo) => {
      const port = 5720 + workerInfo.parallelIndex;
      const baseURL = `http://127.0.0.1:${port}`;
      const binary = join(
        import.meta.dirname,
        ".bin",
        process.platform === "win32" ? "nodevas.exe" : "nodevas",
      );

      // Same seed as serve.mjs: one project with two nodes and one dependency.
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

      // The workspace catalog lives in the user config directory; point it at
      // the throwaway workspace so workers neither share state nor touch the
      // developer's real setup.
      const configDir = join(workspace, "config");
      mkdirSync(configDir, { recursive: true });

      const server = spawn(
        binary,
        ["serve", "--project", workspace, "--port", String(port)],
        {
          stdio: "inherit",
          env: {
            ...process.env,
            APPDATA: configDir,
            XDG_CONFIG_HOME: configDir,
            HOME: configDir,
          },
        },
      );

      const deadline = Date.now() + 60_000;
      for (;;) {
        try {
          const res = await fetch(`${baseURL}/api/projects`);
          if (res.ok) break;
        } catch {
          /* not up yet */
        }
        if (server.exitCode !== null) {
          throw new Error(`e2e server exited early (${server.exitCode})`);
        }
        if (Date.now() > deadline) {
          server.kill();
          throw new Error(`e2e server did not come up on port ${port}`);
        }
        await new Promise((r) => setTimeout(r, 200));
      }

      await use({ baseURL });

      server.kill();
      await new Promise((resolve) => {
        server.once("exit", resolve);
        setTimeout(resolve, 5_000);
      });
      try {
        rmSync(workspace, { recursive: true, force: true });
      } catch {
        /* best effort */
      }
    },
    { scope: "worker" },
  ],
  baseURL: async ({ workerServer }, use) => {
    await use(workerServer.baseURL);
  },
});

export { expect };

/**
 * Answers the first-run tour invitation before the app can ask, and pins the
 * UI language to Traditional Chinese.
 *
 * Every test opens a workspace that has never been used, so the tour's welcome
 * dialog appears over the UI with a backdrop that swallows clicks — and it
 * comes back with the next fresh project, so dismissing it in the UI is not
 * enough. None of these tests are about the tour; declining it up front is
 * what makes them about what they say they are.
 *
 * The language must be pinned for the same reason: the app defaults to
 * English, but the specs assert on zh-TW labels, so a fresh profile would
 * fail every locator that names a control.
 */
export async function declineTour(page: Page) {
  await page.addInitScript(() => {
    localStorage.setItem("vised-tour-prompt", "declined");
    localStorage.setItem("vised-language", "zh-TW");
  });
}

/**
 * Gives one test its own project, so tests cannot inherit each other's
 * milestones, order or statuses. A server holds one active project at a time,
 * but every worker owns a private server (see workerServer above), so switching
 * projects here never disturbs another worker.
 */
export async function freshProject(page: Page, titles = ["設計稿", "實作"]) {
  await declineTour(page);
  counter += 1;
  // Projects are disposable: drop the previous one so the workspace does not
  // grow to dozens of directories over a run, which slows every project scan.
  if (previous) {
    await page.request
      .post("/api/projects/remove", { data: { name: previous, mode: "disk" } })
      .catch(() => undefined);
  }
  const name = `e2e-${process.pid}-${counter}`;
  previous = name;
  const open = await page.request.post("/api/projects/open", {
    data: { name, create: true },
  });
  expect(open.ok()).toBeTruthy();
  for (const title of titles) {
    const created = await page.request.post("/api/nodes", {
      data: { title, kind: "task", body: "" },
    });
    expect(created.ok()).toBeTruthy();
  }
  // Wait for the server to actually be on the new project: opening one is a
  // filesystem operation, and loading the app before it lands races with it.
  await expect
    .poll(async () => (await (await page.request.get("/api/projects")).json()).active, {
      timeout: 15_000,
    })
    .toBe(name);

  await page.goto("/");
  await expect(page.getByRole("heading", { name: "Nodevas" })).toBeVisible({
    timeout: 15_000,
  });
  for (const title of titles) {
    await expect(page.getByText(title).first()).toBeVisible();
  }
  return name;
}
