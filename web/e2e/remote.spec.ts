import { type Page } from "@playwright/test";
import { expect, freshProject, test } from "./fixture";

// W6: step-up, auto-accept policies and external session changes. The hub's
// routes are answered here: these tests are about the dialogs' and the Runs
// tab's layout and flow, not about a runner or a mail server.

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
    capabilities: ["headless"],
    agents: [
      {
        kind: "claude",
        installed: true,
        version: "2.1.0 (Claude Code)",
        models: [{ id: "default", label: "CLI default", default: true }],
      },
    ],
    repos: [{ id: "repo1", name: "nodevas", path: "H:\\visual-editor" }],
    autoAccept: {
      enabled: true,
      requireStepUp: true,
      repos: ["repo1"],
      agents: ["claude", "codex"],
      modes: ["", "interactive"],
    },
  },
};

async function expectNoHorizontalOverflow(page: Page) {
  const overflow = await page.evaluate(() => {
    const root = document.documentElement;
    return root.scrollWidth - root.clientWidth;
  });
  expect(overflow).toBeLessThanOrEqual(1);
}

async function expectInside(locator: ReturnType<Page["locator"]>, width: number) {
  const box = await locator.boundingBox();
  expect(box).not.toBeNull();
  expect(box!.x).toBeGreaterThanOrEqual(0);
  expect(box!.x + box!.width).toBeLessThanOrEqual(width);
}

/** The step-up routes: a wrong code first, then the right one. */
async function mockStepUp(page: Page) {
  const until = new Date(Date.now() + 10 * 60 * 1000).toISOString();
  let active = false;
  const codes: string[] = [];
  await page.route("**/api/auth/step-up/request", (route) =>
    route.fulfill({ status: 202, json: { ok: true } }),
  );
  await page.route("**/api/auth/step-up", async (route) => {
    if (route.request().method() === "GET") {
      return route.fulfill({ json: active ? { required: true, active, until } : { required: true, active } });
    }
    const code = (route.request().postDataJSON() as { code: string }).code;
    codes.push(code);
    if (code !== "ABCD2468") {
      return route.fulfill({ status: 401, json: { error: "invalid code" } });
    }
    active = true;
    return route.fulfill({ json: { required: true, active, until } });
  });
  return { codes, until };
}

async function mockRunners(page: Page) {
  const status = await (await page.request.get("/api/auth/status")).json();
  const owned = { ...runner, ownerId: status.actor?.id ?? "" };
  await page.route("**/api/runners", (route) => route.fulfill({ json: { runners: [owned] } }));
  await page.route(/\/api\/runs\?/, (route) => route.fulfill({ json: { runs: [] } }));
}

const viewports = {
  desktop: { width: 1440, height: 900 },
  phone: { width: 390, height: 844 },
};

for (const [name, viewport] of Object.entries(viewports)) {
  test(`step-up from the dispatch dialog fits on ${name}`, async ({ page }) => {
    await page.setViewportSize(viewport);
    await mockRunners(page);
    const stepUp = await mockStepUp(page);
    await freshProject(page, ["設計稿"]);
    await page.getByText("設計稿").first().click();

    const drawer = page.getByRole("dialog", { name: "節點編輯" });
    await drawer.getByRole("tab", { name: "執行" }).click();
    await drawer.getByRole("button", { name: "派給 agent…" }).click();

    const dispatch = page.getByRole("dialog", { name: "派給 agent" });
    const note = dispatch.getByTestId("dispatch-auto-accept");
    await expect(note).toHaveAttribute("data-outcome", "verify");
    await expect(note).toContainText("驗證身分後就會自動接受");
    await expectNoHorizontalOverflow(page);

    await note.getByRole("button", { name: "驗證" }).click();
    const dialog = page.getByRole("dialog", { name: "確認是你本人" });
    await expect(dialog).toBeVisible();
    await expectInside(dialog, viewport.width);
    await dialog.getByRole("button", { name: "寄送驗證碼" }).click();

    const field = dialog.getByRole("textbox", { name: "8 碼驗證碼" });
    await expect(field).toBeFocused();
    await expect(dialog.getByRole("button", { name: /秒後可重寄/ })).toBeDisabled();
    await field.fill("WWWW2222");
    await dialog.getByRole("button", { name: "驗證", exact: true }).click();
    await expect(dialog.getByRole("alert")).toHaveText("驗證碼錯誤或已過期。");
    await expectNoHorizontalOverflow(page);
    await expectInside(dialog, viewport.width);

    await field.fill("abcd 2468");
    await dialog.getByRole("button", { name: "驗證", exact: true }).click();
    await expect(dialog.getByTestId("stepup-done")).toContainText("已驗證，有效至");
    expect(stepUp.codes).toEqual(["WWWW2222", "ABCD2468"]);
    // A wrong code did not sign the browser out.
    await expect(dispatch).toBeVisible();

    await dialog.getByRole("button", { name: "完成" }).click();
    await expect(dialog).toHaveCount(0);
    await expect(note).toHaveAttribute("data-outcome", "auto");
    await expect(note).toContainText("會自動接受");
    await expectNoHorizontalOverflow(page);
  });

  test(`runners dialog shows the auto-accept policy on ${name}`, async ({ page }) => {
    await page.setViewportSize(viewport);
    await mockRunners(page);
    await mockStepUp(page);
    await freshProject(page, ["設計稿"]);
    await page.evaluate(() => window.dispatchEvent(new CustomEvent("nodevas-open-runners")));

    const modal = page.getByRole("dialog", { name: "Runner" });
    const policy = modal.getByTestId("runner-policy-r1");
    await expect(policy).toBeVisible();
    await expect(policy.getByText("nodevas", { exact: true })).toBeVisible();
    await expect(policy.getByText("互動式")).toBeVisible();
    await expect(policy.getByText("nodevas runner autoaccept on|off|show")).toBeVisible();
    await expectInside(modal, viewport.width);
    await expectNoHorizontalOverflow(page);

    await modal.getByRole("button", { name: "驗證身分" }).click();
    await expect(page.getByRole("dialog", { name: "確認是你本人" })).toBeVisible();
    await page.keyboard.press("Escape");
    await expect(page.getByRole("dialog", { name: "確認是你本人" })).toHaveCount(0);
    // One Escape closes only the step-up dialog.
    await expect(modal).toBeVisible();
  });
}

const patch = [
  "diff --git a/web/src/runs/NodeSessions.tsx b/web/src/runs/NodeSessions.tsx",
  "index 1111111..2222222 100644",
  "--- a/web/src/runs/NodeSessions.tsx",
  "+++ b/web/src/runs/NodeSessions.tsx",
  "@@ -1,2 +1,3 @@",
  "-export const old = 1;",
  `+export const message = "${"a long line that will not fit on a phone ".repeat(5)}";`,
  "+export const fresh = 2;",
  " export {};",
  "",
].join("\n");

const sessionChange = {
  id: "sc1",
  project: "p",
  nodeId: "alpha",
  sessionId: "s-1",
  session: "visual-editor-claude-with-a-rather-long-roster-name",
  agent: "claude",
  actorId: "u1",
  repo: "nodevas",
  branch: "feat/runner-w6-a-long-branch-name-that-should-wrap",
  files: [
    "web/src/runs/NodeSessions.tsx",
    "web/src/runs/sessionStore.ts",
    "web/src/a/really/deep/directory/structure/that/goes/on/and/on/file.tsx",
  ],
  snapshot: {
    commit: "",
    base: "feat/runner-w6",
    files: [{ path: "web/src/runs/NodeSessions.tsx", status: "modified", additions: 2, deletions: 1 }],
    additions: 2,
    deletions: 1,
    patch,
  },
  createdAt: "2026-10-24T07:00:00Z",
  updatedAt: "2026-10-24T08:00:00Z",
};

for (const [name, viewport] of Object.entries(viewports)) {
  test(`terminal session changes fit on ${name}`, async ({ page }) => {
    await page.setViewportSize(viewport);
    await page.route(/\/api\/runs\?/, (route) => route.fulfill({ json: { runs: [] } }));
    await page.route(/\/api\/nodes\/[^/]+\/session-changes/, (route) =>
      route.fulfill({ json: { changes: [sessionChange] } }),
    );
    await freshProject(page, ["設計稿"]);
    await page.getByText("設計稿").first().click();

    const drawer = page.getByRole("dialog", { name: "節點編輯" });
    await drawer.getByRole("tab", { name: "執行" }).click();

    const section = drawer.getByRole("region", { name: /終端機 session/ });
    await expect(section).toBeVisible();
    const item = section.getByTestId("session-sc1");
    await expect(item.getByText("nodevas@feat/runner-w6-a-long-branch-name-that-should-wrap")).toBeVisible();
    await item.getByText("改過的檔案（3）").click();
    await expect(item.getByText("web/src/runs/sessionStore.ts")).toBeVisible();
    await expectNoHorizontalOverflow(page);

    await item.getByRole("button", { name: "查看差異" }).click();
    const dialog = page.getByRole("dialog", { name: "Session 改動" });
    await expect(dialog).toBeVisible();
    const files = dialog.getByRole("navigation", { name: "改動的檔案" });
    await expect(files.getByRole("button")).toHaveCount(1);
    if (viewport.width < 900) await files.getByRole("button").first().click();
    await expect(dialog.getByRole("table", { name: "web/src/runs/NodeSessions.tsx" })).toBeVisible();
    await expect(dialog.getByText("export const fresh = 2;")).toBeVisible();
    await expectInside(dialog, viewport.width);
    await expectNoHorizontalOverflow(page);

    await page.keyboard.press("Escape");
    await expect(dialog).toHaveCount(0);
    await expect(drawer).toBeVisible();
  });
}
