import { expect, type Page } from "@playwright/test";

let counter = 0;
let previous: string | null = null;

/**
 * Answers the first-run tour invitation before the app can ask.
 *
 * Every test opens a workspace that has never been used, so the tour's welcome
 * dialog appears over the UI with a backdrop that swallows clicks — and it
 * comes back with the next fresh project, so dismissing it in the UI is not
 * enough. None of these tests are about the tour; declining it up front is
 * what makes them about what they say they are.
 */
export async function declineTour(page: Page) {
  await page.addInitScript(() => {
    localStorage.setItem("vised-tour-prompt", "declined");
  });
}

/**
 * Gives one test its own project, so tests cannot inherit each other's
 * milestones, order or statuses. The server holds one active project at a
 * time, which is why the suite runs with a single worker.
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
