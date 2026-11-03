import { type Page, type WebSocketRoute } from "@playwright/test";
import { expect, freshProject, test } from "./fixture";

// W2.5: an interactive run's terminal. The run routes and the terminal socket
// are answered here; the point is the view's layout and wiring, not a PTY.

const runner = {
  id: "r1",
  name: "desktop",
  ownerId: "",
  ownerName: "",
  createdAt: "2026-10-23T00:00:00Z",
  lastSeenAt: "2026-10-24T08:00:00Z",
  online: true,
  info: {
    version: "1.4.0",
    hostname: "PC-01",
    os: "windows",
    arch: "amd64",
    capabilities: ["headless", "interactive"],
    agents: [
      {
        kind: "claude",
        installed: true,
        version: "2.1.0 (Claude Code)",
        models: [{ id: "default", label: "CLI default", default: true }],
      },
    ],
    repos: [{ id: "repo1", name: "nodevas", path: "H:\\visual-editor" }],
  },
};

const run = {
  id: "run9",
  project: "p",
  nodeId: "n",
  runnerId: "r1",
  runnerName: "desktop",
  agent: "claude",
  model: "default",
  repoId: "repo1",
  repoName: "nodevas",
  base: "main",
  branch: "agent/interactive-run9",
  prompt: "",
  status: "running",
  mode: "interactive",
  exitCode: 0,
  createdBy: "u1",
  createdByName: "alice",
  createdAt: "2026-10-24T08:00:00Z",
};

const banner =
  "\x1b[1;36m╭─ Claude Code ─────────────────────────────────────────────────────────────────╮\x1b[0m\r\n" +
  "│ \x1b[32m✔\x1b[0m Ready. A line long enough to need more than a phone's width of columns to show. │\r\n" +
  "\x1b[1;36m╰───────────────────────────────────────────────────────────────────────────────╯\x1b[0m\r\n> ";

async function mockInteractiveRun(page: Page) {
  const status = await (await page.request.get("/api/auth/status")).json();
  const owned = { ...runner, ownerId: status.actor?.id ?? "" };
  await page.route("**/api/runners", (route) => route.fulfill({ json: { runners: [owned] } }));
  await page.route(/\/api\/runs\/run9\/events/, (route) =>
    route.fulfill({ json: { events: [], more: false } }),
  );
  await page.route(/\/api\/runs\?/, (route) => {
    const nodeId = new URL(route.request().url()).searchParams.get("nodeId") ?? "n";
    return route.fulfill({ json: { runs: [{ ...run, nodeId }] } });
  });

  const received: { text: string[]; binary: string[] } = { text: [], binary: [] };
  let socket: WebSocketRoute | null = null;
  await page.routeWebSocket(/\/api\/runs\/run9\/terminal/, (ws) => {
    socket = ws;
    ws.onMessage((message) => {
      if (typeof message === "string") received.text.push(message);
      else received.binary.push(message.toString("utf8"));
    });
    ws.send(Buffer.from(banner, "utf8"));
    ws.send(JSON.stringify({ type: "status", status: "running" }));
  });
  return { received, socket: () => socket };
}

async function expectNoHorizontalOverflow(page: Page) {
  const overflow = await page.evaluate(() => {
    const root = document.documentElement;
    return root.scrollWidth - root.clientWidth;
  });
  expect(overflow).toBeLessThanOrEqual(1);
}

for (const [name, viewport] of Object.entries({
  desktop: { width: 1440, height: 900 },
  phone: { width: 390, height: 844 },
})) {
  test(`interactive run terminal fits on ${name}`, async ({ page }) => {
    const phone = viewport.width < 640;
    await page.setViewportSize(viewport);
    const mock = await mockInteractiveRun(page);
    await freshProject(page, ["設計稿"]);
    await page.getByText("設計稿").first().click();

    const drawer = page.getByRole("dialog", { name: "節點編輯" });
    await expect(drawer).toBeVisible();
    await drawer.getByRole("tab", { name: "活動", exact: true }).click();

    await expect(drawer.getByRole("tab", { name: "終端機" })).toHaveAttribute("aria-selected", "true");
    const terminal = drawer.getByTestId("run-terminal");
    await expect(terminal.locator(".xterm")).toBeVisible();
    await expect(terminal.locator(".xterm-rows")).toContainText("Ready.");
    await expect(drawer.getByText("已連線")).toBeVisible();
    await expect(drawer.getByText("執行狀態：執行中")).toBeVisible();

    // The terminal reported its size once it fitted.
    await expect.poll(() => mock.received.text.some((text) => text.includes('"resize"'))).toBe(true);

    const keys = drawer.getByRole("toolbar", { name: "終端機按鍵" });
    if (phone) {
      await expect(keys).toBeVisible();
      await expect(keys.getByRole("button", { name: "Ctrl-C" })).toBeVisible();
      await keys.getByRole("button", { name: "Esc 鍵" }).click();
      await expect.poll(() => mock.received.binary).toContain("\x1b");
      // 80 columns do not fit 390px: the terminal scrolls, the page does not.
      const inner = await drawer
        .locator(".run-term-scroll")
        .evaluate((element) => element.scrollWidth - element.clientWidth);
      expect(inner).toBeGreaterThan(0);
    } else {
      await expect(keys).toBeHidden();
      await terminal.click();
      await page.keyboard.type("hi");
      await expect.poll(() => mock.received.binary.join("")).toContain("hi");
    }
    await expectNoHorizontalOverflow(page);

    // Read-only, then the run ends.
    mock.socket()!.send(JSON.stringify({ type: "readonly" }));
    await expect(drawer.getByText("唯讀", { exact: true })).toBeVisible();
    mock.socket()!.send(JSON.stringify({ type: "status", status: "succeeded" }));
    await expect(drawer.getByText("工作階段已結束 · 成功")).toBeVisible();
    await expectNoHorizontalOverflow(page);
    // The run's own 活動 tab, inside the panel's Activity tab of the same name.
    await drawer.getByRole("region", { name: "執行詳情" }).getByRole("tab", { name: "活動" }).click();
    await expect(terminal).toBeHidden();
    await expect(drawer.getByRole("list", { name: "執行過程" })).toBeVisible();

    // Dispatch dialog: the mode choice and its note.
    await drawer.getByRole("button", { name: "派給 agent…" }).click();
    const dialog = page.getByRole("dialog", { name: "派給 agent" });
    await expect(dialog).toBeVisible();
    await dialog.getByText("互動式", { exact: true }).click();
    await expect(dialog.getByRole("radio", { name: "互動式" })).toBeChecked();
    await expect(dialog.getByText(/在 runner 上開啟 agent 自己的終端機介面/)).toBeVisible();
    await expectNoHorizontalOverflow(page);
  });
}
