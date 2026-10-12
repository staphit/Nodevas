import { type Page } from "@playwright/test";
import { expect, freshProject, test } from "./fixture";

async function openDocument(page: Page) {
  await freshProject(page, ["筆記"]);
  await page.locator(".lane-wrap.graph .col-card").filter({ hasText: "筆記" }).first().click();
  const drawer = page.getByRole("dialog", { name: "節點編輯" });
  await expect(drawer).toBeVisible();
  return drawer;
}

const editorText = (page: Page) => page.locator(".cm-content");

test.describe("markdown editor", () => {
  test("edits in place with the markup rendered, not as raw source", async ({
    page,
  }) => {
    const drawer = await openDocument(page);
    await expect(drawer.getByRole("button", { name: "即時編輯" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );

    await editorText(page).click();
    await page.keyboard.type("# 章節一\n**重點**在這裡\n- 第一項\n");

    // The caret sits on the trailing empty line, so every line above renders.

    const heading = page.locator(".cm-live-h1");
    await expect(heading).toContainText("章節一");
    await expect(heading).not.toContainText("#");
    await expect(editorText(page)).not.toContainText("**重點**");
    await expect(editorText(page)).toContainText("重點");
    await expect(page.locator(".cm-live-bullet").first()).toBeVisible();
  });

  test("shows the raw markdown again on the line being edited", async ({ page }) => {
    await openDocument(page);
    await editorText(page).click();
    await page.keyboard.type("## 標題\n內文\n");
    await page.keyboard.press("ArrowUp");
    await page.keyboard.press("ArrowUp");

    // Caret is back on the heading line, so its marker is visible and editable.
    await expect(editorText(page)).toContainText("## 標題");
  });

  test("switches to source and back, keeping the same document", async ({ page }) => {
    const drawer = await openDocument(page);
    await editorText(page).click();
    await page.keyboard.type("# 標題\n");

    await drawer.getByRole("button", { name: "原始碼" }).click();
    await expect(editorText(page)).toContainText("# 標題");

    await drawer.getByRole("button", { name: "預覽" }).click();
    await expect(page.locator(".md-preview h1")).toContainText("標題");

    await drawer.getByRole("button", { name: "即時編輯" }).click();
    await expect(page.locator(".cm-live-h1")).toContainText("標題");
  });

  test("remembers the chosen mode", async ({ page }) => {
    const drawer = await openDocument(page);
    await drawer.getByRole("button", { name: "原始碼" }).click();

    await page.reload();
    await page.locator(".lane-wrap.graph .col-card").filter({ hasText: "筆記" }).first().click();
    await expect(
      page.getByRole("dialog", { name: "節點編輯" }).getByRole("button", { name: "原始碼" }),
    ).toHaveAttribute("aria-pressed", "true");
  });

  test("ticks a task from the rendered checkbox", async ({ page }) => {
    await openDocument(page);
    await editorText(page).click();
    await page.keyboard.type("- [ ] 待辦事項\n");
    await page.keyboard.press("ArrowUp");
    await page.keyboard.press("End");
    await page.keyboard.press("ArrowDown");

    const box = page.locator("input.cm-live-task").first();
    await expect(box).toBeVisible();
    await box.click();

    // The tick is written back into the markdown, which is what gets saved.
    await page.keyboard.press("Control+s");
    await expect(page.getByText("已儲存").first()).toBeVisible();
    await page.getByRole("dialog", { name: "節點編輯" })
      .getByRole("button", { name: "原始碼" })
      .click();
    await expect(editorText(page)).toContainText("[x]");
  });
});

test.describe("document history and structure", () => {
  test("previews a file version before restoring it", async ({ page }) => {
    const drawer = await openDocument(page);

    // Two saves inside one editing session. Now that the document saves itself
    // every second and a half, a version per save would fill the fifty a file
    // keeps in about two minutes and evict everything worth going back to, so
    // the store coalesces a session into one snapshot of what it started from
    // (internal/store/history.go). Two saves, one version.
    await editorText(page).click();
    await page.keyboard.type("第一版\n");
    await page.keyboard.press("Control+s");
    await expect(page.getByText("已儲存").first()).toBeVisible();
    await page.keyboard.type("第二版\n");
    await page.keyboard.press("Control+s");
    await expect(page.getByText("已儲存").first()).toBeVisible();

    await drawer.getByText(/歷程/).click();
    const versions = drawer.locator(".nh-files li");
    await expect(versions.first()).toBeVisible();
    await expect(versions).toHaveCount(1);

    await versions.first().getByRole("button", { name: "預覽" }).click();
    const preview = drawer.getByRole("region", { name: "版本預覽" });
    await expect(preview).toBeVisible();
    await expect(preview).not.toContainText("第一版");
    await expect(preview).not.toContainText("第二版");
    // Read-only: the current document is untouched until 還原 is pressed.
    await expect(editorText(page)).toContainText("第二版");
  });

  test("restores an earlier version of a subpage", async ({ page }) => {
    const drawer = await openDocument(page);

    await drawer.getByRole("button", { name: "新增子頁" }).click();
    await drawer.getByLabel("子頁標題").fill("草稿");
    await drawer.getByRole("button", { name: "建立頁面" }).click();
    await expect(drawer.getByLabel("檔案位置")).toContainText(".pages/");

    // Two saves of the subpage. As above, one editing session is one version:
    // what the page looked like before any of this was typed, which here is the
    // starter heading the page was created with.
    await editorText(page).click();
    await page.keyboard.press("Control+a");
    await page.keyboard.type("子頁第一版\n");
    await page.keyboard.press("Control+s");
    await expect(page.getByText("已儲存").first()).toBeVisible();
    await page.keyboard.press("Control+a");
    await page.keyboard.type("子頁第二版\n");
    await page.keyboard.press("Control+s");
    await expect(page.getByText("已儲存").first()).toBeVisible();

    // The history follows the open page, not the node's main document.
    await drawer.getByText(/歷程/).click();
    const versions = drawer.locator(".nh-files li");
    await expect(versions.first()).toBeVisible();
    await versions.first().getByRole("button", { name: "預覽" }).click();
    const preview = drawer.getByRole("region", { name: "版本預覽" });
    await expect(preview).toContainText("草稿");
    await expect(preview).not.toContainText("子頁第二版");

    await preview.getByRole("button", { name: "還原這一版" }).click();
    await page.getByRole("button", { name: "還原版本" }).click();
    await expect(editorText(page)).toContainText("草稿");
    await expect(editorText(page)).not.toContainText("子頁第二版");
  });

  test("renders a table and frames a code block", async ({ page }) => {
    await openDocument(page);
    await editorText(page).click();
    await page.keyboard.type("| 欄一 | 欄二 |\n|---|---|\n| a | b |\n\n");
    await page.keyboard.type("```js\nconst x = 1;\n```\n");

    await expect(page.locator(".cm-live-table th").first()).toContainText("欄一");
    await expect(page.locator(".cm-live-table td").first()).toContainText("a");
    await expect(page.locator(".cm-live-code").first()).toBeVisible();
  });

  test("names the file being edited", async ({ page }) => {
    const drawer = await openDocument(page);
    const path = drawer.getByLabel("檔案位置");
    await expect(path).toBeVisible();
    await expect(path).toContainText("nodes/");
    await expect(path).toContainText(".md");
  });
});

test.describe("outline", () => {
  test("lists the document's headings and jumps to one", async ({ page }) => {
    const drawer = await openDocument(page);
    await editorText(page).click();
    await page.keyboard.type("# 第一章\n內文一\n## 小節\n內文二\n# 第二章\n內文三\n");

    await drawer.getByRole("button", { name: "文件目錄" }).click();
    const outline = drawer.getByRole("navigation", { name: "文件目錄" });
    await expect(outline).toBeVisible();
    await expect(outline.getByRole("button", { name: /第一章/ })).toBeVisible();
    await expect(outline.getByRole("button", { name: /小節/ })).toBeVisible();
    await expect(outline.getByRole("button", { name: /第二章/ })).toBeVisible();

    // Jumping moves the caret, so typing continues at that heading.
    await outline.getByRole("button", { name: /第一章/ }).click();
    await page.keyboard.type("X");
    await drawer.getByRole("button", { name: "原始碼" }).click();
    await expect(editorText(page)).toContainText("X# 第一章");
  });

  test("says what to do when there are no headings", async ({ page }) => {
    const drawer = await openDocument(page);
    await editorText(page).click();
    await page.keyboard.type("只是一段內文。\n");

    await drawer.getByRole("button", { name: "文件目錄" }).click();
    await expect(drawer.getByText("尚無標題")).toBeVisible();
  });

  test("remembers that the outline is open", async ({ page }) => {
    const drawer = await openDocument(page);
    await drawer.getByRole("button", { name: "文件目錄" }).click();
    await expect(drawer.getByRole("navigation", { name: "文件目錄" })).toBeVisible();

    await page.reload();
    await page.locator(".lane-wrap.graph .col-card").filter({ hasText: "筆記" }).first().click();
    await expect(
      page.getByRole("dialog", { name: "節點編輯" }).getByRole("navigation", { name: "文件目錄" }),
    ).toBeVisible();
  });
});

test.describe("node card appearance", () => {
  test("changes a card's shape and size from the inspector", async ({ page }) => {
    await freshProject(page, ["形狀節點"]);
    const card = page.locator(".lane-wrap.graph .col-card").filter({ hasText: "形狀節點" });
    await card.first().click();

    const drawer = page.getByRole("dialog", { name: "節點編輯" });
    await drawer.getByRole("tab", { name: "外觀" }).click();
    await drawer.getByRole("button", { name: "橢圓" }).click();
    await expect(card.first()).toHaveClass(/shape-ellipse/);

    const width = drawer.getByRole("slider", { name: "寬度" });
    await width.fill("240");
    await width.dispatchEvent("change");
    // The rendered width, read from the card itself rather than a bounding box,
    // which a clipped shape distorts.
    await expect
      .poll(async () =>
        card.first().evaluate((element) => (element as HTMLElement).style.width),
      )
      .toBe("240px");

    // The choice is part of the project, so it survives a reload.
    await page.reload();
    await expect(
      page.locator(".lane-wrap.graph .col-card").filter({ hasText: "形狀節點" }).first(),
    ).toHaveClass(/shape-ellipse/);
  });

  test("resets a card back to the default look", async ({ page }) => {
    await freshProject(page, ["形狀節點"]);
    const card = page.locator(".lane-wrap.graph .col-card").filter({ hasText: "形狀節點" });
    await card.first().click();

    const drawer = page.getByRole("dialog", { name: "節點編輯" });
    await drawer.getByRole("tab", { name: "外觀" }).click();
    await drawer.getByRole("button", { name: "六角形" }).click();
    await expect(card.first()).toHaveClass(/shape-hexagon/);

    await drawer.getByRole("button", { name: "重設為預設外觀" }).click();
    await expect(card.first()).not.toHaveClass(/shape-/);
  });

  test("outlines a clipped shape on every edge, in the chosen colour", async ({
    page,
  }) => {
    await freshProject(page, ["外框節點"]);
    const card = page.locator(".lane-wrap.graph .col-card").filter({ hasText: "外框節點" });
    await card.first().click();

    const drawer = page.getByRole("dialog", { name: "節點編輯" });
    await drawer.getByRole("tab", { name: "外觀" }).click();
    await drawer.getByRole("button", { name: "菱形" }).click();
    await expect(card.first()).toHaveClass(/shape-diamond/);
    await drawer.getByRole("button", { name: "外框 #ff9f9f" }).click();

    // The diamond paints its own edge: the element is the outline colour and
    // an inset copy of the shape carries the fill.
    await expect
      .poll(() =>
        card.first().evaluate((element) => getComputedStyle(element).backgroundColor),
      )
      .toBe("rgb(255, 159, 159)");
    const drawn = await card.first().evaluate((element) => {
      const edge = getComputedStyle(element);
      const fill = getComputedStyle(element, "::before");
      return {
        edge: edge.backgroundColor,
        clipped: edge.clipPath,
        fill: fill.backgroundColor,
        band: fill.insetBlockStart || fill.top,
      };
    });
    expect(drawn.edge).toBe("rgb(255, 159, 159)");
    expect(drawn.clipped).toContain("polygon");
    expect(drawn.fill).not.toBe(drawn.edge);
    expect(parseFloat(drawn.band)).toBeGreaterThan(0);
  });

  test("recolours a plain card's border", async ({ page }) => {
    await freshProject(page, ["邊框節點"]);
    const card = page.locator(".lane-wrap.graph .col-card").filter({ hasText: "邊框節點" });
    await card.first().click();

    const drawer = page.getByRole("dialog", { name: "節點編輯" });
    await drawer.getByRole("tab", { name: "外觀" }).click();
    await drawer.getByRole("button", { name: "外框 #8fd3ff" }).click();

    await expect
      .poll(() =>
        card.first().evaluate((element) => {
          const style = getComputedStyle(element);
          return [style.borderTopColor, style.borderLeftColor];
        }),
      )
      .toEqual(["rgb(143, 211, 255)", "rgb(143, 211, 255)"]);

    // Clearing hands the outline back to the status colour.
    await drawer.getByRole("button", { name: "清除" }).click();
    await expect
      .poll(() =>
        card.first().evaluate((element) => getComputedStyle(element).borderTopColor),
      )
      .not.toBe("rgb(143, 211, 255)");
  });
});

test.describe("canvas resize handles", () => {
  test("resizes a card by dragging its handle, like a diagram editor", async ({
    page,
  }) => {
    await freshProject(page, ["可調整"]);
    const card = page.locator(".lane-wrap.graph .col-card").filter({ hasText: "可調整" });
    await card.first().click();

    const handle = page.locator(".card-resize-frame .col-card-handle.e");
    await expect(handle).toBeVisible();

    const styleWidth = (locator = card.first()) =>
      locator.evaluate((element) => Number((element as HTMLElement).style.width.replace("px", "")));
    const before = await styleWidth();
    // Coordinates come from the page: the board is zoomed, so a bounding box
    // measured outside it does not line up with what the handle sees.
    await handle.evaluate((element) => {
      const rect = element.getBoundingClientRect();
      const at = (dx: number) => ({
        bubbles: true,
        pointerId: 1,
        pointerType: "mouse",
        button: 0,
        clientX: rect.left + rect.width / 2 + dx,
        clientY: rect.top + rect.height / 2,
      });
      element.dispatchEvent(new PointerEvent("pointerdown", at(0)));
      element.dispatchEvent(new PointerEvent("pointermove", at(20)));
      element.dispatchEvent(new PointerEvent("pointermove", at(40)));
      element.dispatchEvent(new PointerEvent("pointerup", at(40)));
    });

    await expect.poll(styleWidth).toBeGreaterThan(before);

    // The size is part of the project, so it survives a reload.
    const resized = await styleWidth();
    await page.reload();
    await expect
      .poll(() =>
        styleWidth(
          page.locator(".lane-wrap.graph .col-card").filter({ hasText: "可調整" }).first(),
        ),
      )
      .toBe(resized);
  });

  test("resizes with the keyboard from a focused handle", async ({ page }) => {
    await freshProject(page, ["可調整"]);
    const card = page.locator(".lane-wrap.graph .col-card").filter({ hasText: "可調整" });
    await card.first().click();

    const handle = page.locator(".card-resize-frame .col-card-handle.e");
    await handle.focus();
    const styleWidth = () =>
      card.first().evaluate((element) => Number((element as HTMLElement).style.width.replace("px", "")));
    const before = await styleWidth();
    await page.keyboard.press("Shift+ArrowRight");

    await expect.poll(styleWidth).toBe(before + 24);
  });
});

test.describe("shapes keep their contents", () => {
  test("a diamond stays upright and keeps its handles reachable", async ({ page }) => {
    await freshProject(page, ["菱形節點"]);
    const card = page.locator(".lane-wrap.graph .col-card").filter({ hasText: "菱形節點" });
    await card.first().click();

    const drawer = page.getByRole("dialog", { name: "節點編輯" });
    await drawer.getByRole("tab", { name: "外觀" }).click();
    await drawer.getByRole("button", { name: "菱形" }).click();
    await expect(card.first()).toHaveClass(/shape-diamond/);

    // Clipped, not rotated: the text box is still axis-aligned.
    const transform = await card
      .first()
      .evaluate((el) => getComputedStyle(el).transform);
    expect(transform === "none" || transform === "matrix(1, 0, 0, 1, 0, 0)").toBe(true);

    // The title sits inside the card box rather than spilling out of it.
    const inside = await card.first().evaluate((element) => {
      const box = element.getBoundingClientRect();
      const title = element.querySelector(".col-card-title")!.getBoundingClientRect();
      return title.left >= box.left - 1 && title.right <= box.right + 1;
    });
    expect(inside).toBe(true);

    // Handles live outside the clip, so they are still usable.
    const handle = page.locator(".card-resize-frame .col-card-handle.e");
    await expect(handle).toBeVisible();
    const styleWidth = () =>
      card.first().evaluate((element) => (element as HTMLElement).style.width);
    const before = Number((await styleWidth()).replace("px", ""));
    await handle.focus();
    await page.keyboard.press("Shift+ArrowRight");
    await expect.poll(async () => await styleWidth()).toBe(`${before + 24}px`);
  });

  test("sets the card's text colour", async ({ page }) => {
    await freshProject(page, ["彩色節點"]);
    const card = page.locator(".lane-wrap.graph .col-card").filter({ hasText: "彩色節點" });
    await card.first().click();

    const drawer = page.getByRole("dialog", { name: "節點編輯" });
    await drawer.getByRole("tab", { name: "外觀" }).click();
    await drawer.getByLabel("自訂文字顏色").fill("#ff0000");

    await expect
      .poll(async () => card.first().evaluate((el) => getComputedStyle(el).color))
      .toBe("rgb(255, 0, 0)");
  });
});

test.describe("card text placement", () => {
  test("aligns the card's text left, centre or right", async ({ page }) => {
    await freshProject(page, ["對齊節點"]);
    const card = page.locator(".lane-wrap.graph .col-card").filter({ hasText: "對齊節點" });
    await card.first().click();
    const drawer = page.getByRole("dialog", { name: "節點編輯" });
    await drawer.getByRole("tab", { name: "外觀" }).click();

    const textAlign = () =>
      card.first().evaluate((element) => getComputedStyle(element).textAlign);

    await drawer.getByRole("button", { name: "靠右" }).click();
    await expect.poll(textAlign).toBe("right");

    await drawer.getByRole("button", { name: "水平置中" }).click();
    await expect.poll(textAlign).toBe("center");

    await drawer.getByRole("button", { name: "靠左" }).click();
    await expect.poll(textAlign).toBe("left");
  });

  test("beats the shape's default and survives a reload", async ({ page }) => {
    await freshProject(page, ["對齊節點"]);
    const card = page.locator(".lane-wrap.graph .col-card").filter({ hasText: "對齊節點" });
    await card.first().click();
    const drawer = page.getByRole("dialog", { name: "節點編輯" });

    // A diamond centres its text by default; asking for left must win.
    await drawer.getByRole("tab", { name: "外觀" }).click();
    await drawer.getByRole("button", { name: "菱形" }).click();
    await drawer.getByRole("button", { name: "靠左" }).click();
    await drawer.getByRole("button", { name: "靠上" }).click();

    await expect
      .poll(() => card.first().evaluate((el) => getComputedStyle(el).textAlign))
      .toBe("left");
    await expect
      .poll(() => card.first().evaluate((el) => getComputedStyle(el).justifyContent))
      .toBe("flex-start");

    await page.reload();
    await expect
      .poll(() =>
        page
          .locator(".lane-wrap.graph .col-card")
          .filter({ hasText: "對齊節點" })
          .first()
          .evaluate((el) => getComputedStyle(el).textAlign),
      )
      .toBe("left");
  });
});

test.describe("shape geometry", () => {
  test("keeps the card at the requested size in every shape", async ({ page }) => {
    await freshProject(page, ["尺寸節點"]);
    const card = page.locator(".lane-wrap.graph .col-card").filter({ hasText: "尺寸節點" });
    await card.first().click();
    const drawer = page.getByRole("dialog", { name: "節點編輯" });
    await drawer.getByRole("tab", { name: "外觀" }).click();

    const measure = () =>
      card.first().evaluate((element) => {
        const rect = element.getBoundingClientRect();
        const title = element.querySelector(".col-card-title") as HTMLElement;
        return {
          width: Math.round(rect.width),
          height: Math.round(rect.height),
          title: title.textContent,
          titleVisible: title.getBoundingClientRect().width > 0,
        };
      });

    for (const shape of ["橢圓", "菱形", "六角形", "膠囊"]) {
      await drawer.getByRole("button", { name: shape }).click();
      // Insets scale with the card, never with the board it sits on.
      await expect.poll(measure).toMatchObject({
        width: 152,
        height: 68,
        title: "尺寸節點",
        titleVisible: true,
      });
    }
  });
});

test.describe("tables", () => {
  test("inserts a table and edits its structure from the toolbar", async ({ page }) => {
    const drawer = await openDocument(page);
    await editorText(page).click();

    await drawer.getByRole("button", { name: "插入表格" }).click();
    await drawer.getByLabel("列數").fill("2");
    await drawer.getByLabel("欄數").fill("2");
    await drawer.getByRole("button", { name: "插入", exact: true }).click();

    // The caret lands in the first header cell, so the table bar is up.
    const tableBar = drawer.getByRole("toolbar", { name: "表格編輯" });
    await expect(tableBar).toBeVisible();
    await expect(tableBar).toContainText("第 1 列第 1 欄");

    await tableBar.getByRole("button", { name: "在右方插入一欄" }).click();
    await expect(tableBar).toContainText("表格 3×3");

    await tableBar.getByRole("button", { name: "在下方插入一列" }).click();
    await expect(tableBar).toContainText("表格 4×3");

    await drawer.getByRole("button", { name: "原始碼" }).click();
    await expect(editorText(page)).toContainText("欄 1");
    await expect(editorText(page)).toContainText("| --- |");
  });

  test("hides the table bar outside a table", async ({ page }) => {
    const drawer = await openDocument(page);
    await editorText(page).click();
    await page.keyboard.type("一般段落\n");
    await expect(drawer.getByRole("toolbar", { name: "表格編輯" })).toBeHidden();
  });
});

test.describe("document export", () => {
  test("downloads the open page as plain text and as Word", async ({ page }) => {
    const drawer = await openDocument(page);
    await editorText(page).click();
    await page.keyboard.type("# 匯出測試\n內文一行\n");

    for (const [format, extension] of [
      ["純文字", ".txt"],
      ["Word", ".docx"],
    ]) {
      await drawer.getByRole("button", { name: "匯出檔案" }).click();
      const download = page.waitForEvent("download");
      await drawer.getByRole("button", { name: format, exact: false }).click();
      const file = await download;
      expect(file.suggestedFilename()).toContain(extension);
    }
  });

  // The export reads the editor, not the file. That used to be the difference
  // between exporting your work and exporting the last thing you pressed Ctrl+S
  // on; with auto-save the window is narrower but it is still real, and an
  // export that quietly omits the last sentence is worse than a slow one.
  test("exports what is in the editor, not only what has reached disk", async ({
    page,
  }) => {
    const drawer = await openDocument(page);
    await editorText(page).click();
    await page.keyboard.type("尚未儲存的內容\n");

    await drawer.getByRole("button", { name: "匯出檔案" }).click();
    const download = page.waitForEvent("download");
    await drawer.getByRole("button", { name: "純文字" }).click();
    const file = await download;
    const stream = await file.createReadStream();
    const chunks: Buffer[] = [];
    for await (const chunk of stream) chunks.push(chunk as Buffer);
    expect(Buffer.concat(chunks).toString("utf8")).toContain("尚未儲存的內容");
  });
});

test.describe("page formats", () => {
  test("creates a plain-text page and keeps it as text", async ({ page }) => {
    const drawer = await openDocument(page);

    await drawer.getByRole("button", { name: "新增子頁" }).click();
    await drawer.getByLabel("子頁標題").fill("備忘");
    await drawer.getByLabel("子頁檔案格式").first().selectOption("txt");
    await drawer.getByRole("button", { name: "建立頁面" }).click();

    // The tab says what the file is, and the path shows the extension.
    await expect(drawer.getByRole("tab", { name: /備忘/ })).toContainText("純文字");
    await expect(drawer.getByLabel("檔案位置")).toContainText(".txt");
    // Markdown-only tools are gone: plain text has no headings.
    await expect(drawer.getByRole("button", { name: "設為內文" })).toBeHidden();
    await expect(drawer.getByRole("button", { name: "即時編輯" })).toBeHidden();

    await editorText(page).click();
    await page.keyboard.type("純文字內容\n");
    await page.keyboard.press("Control+s");
    await expect(page.getByText("已儲存").first()).toBeVisible();

    await page.reload();
    await page.locator(".lane-wrap.graph .col-card").filter({ hasText: "筆記" }).first().click();
    const reopened = page.getByRole("dialog", { name: "節點編輯" });
    await reopened.getByRole("tab", { name: /備忘/ }).click();
    await expect(editorText(page)).toContainText("純文字內容");
  });

  test("round-trips a Word page through the editor", async ({ page }) => {
    const drawer = await openDocument(page);

    await drawer.getByRole("button", { name: "新增子頁" }).click();
    await drawer.getByLabel("子頁標題").fill("報告");
    await drawer.getByLabel("子頁檔案格式").first().selectOption("docx");
    await expect(drawer.getByText(/存檔時重新產生 \.docx/)).toBeVisible();
    await drawer.getByRole("button", { name: "建立頁面" }).click();
    await expect(drawer.getByLabel("檔案位置")).toContainText(".docx");

    await editorText(page).click();
    await page.keyboard.press("Control+a");
    await page.keyboard.type("# 報告\n\n第一段 **粗體**\n\n- 項目一\n");
    await page.keyboard.press("Control+s");
    await expect(page.getByText("已儲存").first()).toBeVisible();

    await page.reload();
    await page.locator(".lane-wrap.graph .col-card").filter({ hasText: "筆記" }).first().click();
    const reopened = page.getByRole("dialog", { name: "節點編輯" });
    await reopened.getByRole("tab", { name: /報告/ }).click();
    // Reopening converts the .docx back to Markdown.
    await reopened.getByRole("button", { name: "原始碼" }).click();
    await expect(editorText(page)).toContainText("**粗體**");
    await expect(editorText(page)).toContainText("- 項目一");
  });

  test("converts an existing page to another format", async ({ page }) => {
    const drawer = await openDocument(page);

    await drawer.getByRole("button", { name: "新增子頁" }).click();
    await drawer.getByLabel("子頁標題").fill("轉檔");
    await drawer.getByRole("button", { name: "建立頁面" }).click();
    await editorText(page).click();
    await page.keyboard.type("\n轉換前的內容\n");
    await page.keyboard.press("Control+s");
    await expect(page.getByText("已儲存").first()).toBeVisible();

    // Picking a format in the select starts the conversion; a dialog asks for
    // confirmation before the file is rewritten.
    await drawer.getByLabel("子頁檔案格式").selectOption("html");
    await page.getByRole("button", { name: "轉換這一頁的檔案格式" }).click();

    await expect(drawer.getByRole("tab", { name: /轉檔/ })).toContainText("網頁");
    await expect(drawer.getByLabel("檔案位置")).toContainText(".html");
    await editorText(page).press("Control+End");
    await expect(editorText(page)).toContainText("轉換前的內容");
  });
});
