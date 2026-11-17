import { type Page } from "@playwright/test";
import { expect, freshProject, test } from "./fixture";

// The copilot's model is answered here (the e2e server has no model): the
// draft turn streams what the drafter would. Everything after it is real:
// the dry-run check, Create as the person, the batch record and its undo.

const draft = {
  summary: "上線三步",
  nodes: [
    { key: "n1", title: "寫使用文件", kind: "task", after: [] },
    { key: "n2", title: "整合測試", kind: "task", after: ["n1"] },
    { key: "n3", title: "發布上線", kind: "task", after: ["n2"] },
  ],
};

async function stubCopilotModel(page: Page) {
  const conversation = { id: "c-e2e", project: "", title: "", mode: "default", createdAt: 1, updatedAt: 1 };
  let data = { draft, issues: [], provider: "openai", model: "qwen3:8b", state: "open" } as Record<string, unknown>;
  const entry = () => ({
    id: "e-draft",
    conversationId: conversation.id,
    seq: 2,
    kind: "draft",
    text: draft.summary,
    data: JSON.stringify(data),
    createdAt: Date.now(),
  });
  await page.route(/\/api\/copilot\/status/, (route) =>
    route.fulfill({
      json: { enabled: true, provider: "openai", model: "qwen3:8b", modes: ["plan", "default", "acceptEdits"], allowBypass: false },
    }),
  );
  await page.route(/\/api\/copilot\/conversations(\?.*)?$/, (route) =>
    route.request().method() === "POST"
      ? route.fulfill({ status: 201, json: { conversation } })
      : route.fulfill({ json: { conversations: [] } }),
  );
  await page.route(/\/api\/copilot\/conversations\/c-e2e\/turns$/, async (route) => {
    const body = route.request().postDataJSON() as { intent?: string; text: string };
    expect(body.intent).toBe("draft");
    const lines = [
      { type: "turn_started", turnId: "t1", entryId: "e-user" },
      { type: "entry", entry: { id: "e-user", conversationId: conversation.id, seq: 1, kind: "user", source: "text", text: body.text, createdAt: Date.now() } },
      { type: "draft_status", code: "reading" },
      { type: "draft_status", code: "drafting" },
      { type: "entry", entry: entry() },
      { type: "turn_finished", stop: "end_turn" },
    ];
    await route.fulfill({
      headers: { "Content-Type": "application/x-ndjson; charset=utf-8" },
      body: lines.map((line) => JSON.stringify(line)).join("\n") + "\n",
    });
  });
  await page.route(/\/api\/copilot\/conversations\/c-e2e\/entries\/e-draft\/draft-state$/, async (route) => {
    const patch = route.request().postDataJSON() as { state: string; batchId?: string; ids?: string[] };
    data = { ...data, state: patch.state, batchId: patch.batchId, createdIds: patch.ids };
    await route.fulfill({ json: entry() });
  });
}

async function titles(page: Page): Promise<string[]> {
  const graph = await (await page.request.get("/api/graph")).json();
  return (graph.graph?.nodes ?? []).map((node: { title: string }) => node.title);
}

test("type a description, fix the draft, create it and undo it", async ({ page }) => {
  await stubCopilotModel(page);
  await freshProject(page, ["寫規格"]);

  await page.getByRole("button", { name: /^Copilot · 開啟 Copilot/ }).click();
  // Make nodes is the default; typing is the way in.
  await expect(page.getByRole("button", { name: "建立節點" })).toHaveAttribute("aria-pressed", "true");
  const box = page.getByRole("textbox", { name: "給 Copilot 的訊息" });
  await box.fill("規劃產品上線：寫文件、測試、發布");
  await box.press("Enter");

  await expect(page.getByText("草稿：3 個節點、2 條依賴")).toBeVisible();
  // Nothing exists until Create.
  expect(await titles(page)).toEqual(["寫規格"]);

  // The test step goes; release then waits for the docs, so the chain holds.
  await page.getByRole("button", { name: "移除「整合測試」" }).click();
  await expect(page.getByText("草稿：2 個節點、1 條依賴")).toBeVisible();

  const create = page.getByRole("button", { name: "建立 2 個節點" });
  await expect(create).toBeEnabled();
  await create.click();
  await expect(page.getByText("已建立 2 個節點")).toBeVisible();
  await expect.poll(() => titles(page)).toEqual(expect.arrayContaining(["寫規格", "寫使用文件", "發布上線"]));
  expect(await titles(page)).not.toContain("整合測試");

  await page.getByRole("button", { name: "復原" }).click();
  await expect.poll(() => titles(page)).toEqual(["寫規格"]);
});
