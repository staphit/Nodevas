import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createRef } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { readPreferences } from "../../preferences";
import { useApp } from "../../store";
import type { EditorMode } from "./editorExtensions";
import { EditorToolbar } from "./EditorToolbar";
import type { Attachments } from "./useAttachments";
import type { TableEditing } from "./useTableEditing";

beforeEach(() => {
  useApp.setState({ preferences: { ...readPreferences(), language: "zh-TW" } });
});

function renderToolbar({
  markdownTools = true,
  editorMode = "live" as EditorMode,
} = {}) {
  const setEditorMode = vi.fn();
  const toggleOutline = vi.fn();
  const insertTable = vi.fn();
  const attachments = {
    attachBusy: false,
    attachInputRef: createRef<HTMLInputElement>(),
    attachFiles: vi.fn(),
  } as unknown as Attachments;
  const tables = {
    tableAt: null,
    tableRows: 3,
    setTableRows: vi.fn(),
    tableColumns: 3,
    setTableColumns: vi.fn(),
    tableMenuRef: createRef<HTMLDetailsElement>(),
    syncTable: vi.fn(),
    insertTable,
    applyTableEdit: vi.fn(),
  } as unknown as TableEditing;
  render(
    <EditorToolbar
      markdownTools={markdownTools}
      view={() => undefined}
      onInsertNodeLink={vi.fn()}
      attachments={attachments}
      tables={tables}
      fontSize={15}
      setFontSize={vi.fn()}
      outlineOpen={false}
      toggleOutline={toggleOutline}
      editorMode={editorMode}
      setEditorMode={setEditorMode}
      nodeId="alpha"
      pageId="main"
      content="# draft"
    />,
  );
  return { setEditorMode, toggleOutline, insertTable };
}

describe("EditorToolbar", () => {
  it("keeps four markers in reach and the rest behind More formatting", () => {
    renderToolbar();
    const toolbar = screen.getByRole("toolbar");
    for (const name of ["粗體", "斜體", "項目清單", "插入超連結"]) {
      expect(screen.getByRole("button", { name })).toBeInTheDocument();
    }
    // Headings, export and the modes are not toolbar buttons any more.
    expect(toolbar.querySelectorAll("button")).toHaveLength(5);
    expect(screen.queryByRole("button", { name: "匯出檔案" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "預覽" })).not.toBeInTheDocument();
  });

  it("holds every other action in the menu, which Escape closes", async () => {
    const user = userEvent.setup();
    renderToolbar();
    const trigger = screen.getByRole("button", { name: "更多格式" });
    await user.click(trigger);

    const menu = screen.getByRole("menu", { name: "更多格式" });
    expect(menu).toBeInTheDocument();
    for (const name of ["標題 1", "標題 3", "連結到節點", "匯出檔案…"]) {
      expect(screen.getByRole("menuitem", { name })).toBeInTheDocument();
    }
    expect(screen.getByRole("menuitemcheckbox", { name: "文件目錄" })).toBeInTheDocument();
    expect(screen.getByRole("menuitemradio", { name: "即時編輯" })).toHaveAttribute(
      "aria-checked",
      "true",
    );
    expect(screen.getByRole("form", { name: "插入表格" })).toBeInTheDocument();

    await user.keyboard("{Escape}");
    expect(screen.queryByRole("menu")).not.toBeInTheDocument();
    expect(trigger).toHaveFocus();
  });

  it("switches the editing mode from the menu", async () => {
    const user = userEvent.setup();
    const { setEditorMode } = renderToolbar();
    await user.click(screen.getByRole("button", { name: "更多格式" }));
    await user.click(screen.getByRole("menuitemradio", { name: "預覽" }));
    expect(setEditorMode).toHaveBeenCalledWith("preview");
    expect(screen.queryByRole("menu")).not.toBeInTheDocument();
  });

  it("disables the markers in preview and shows the way back to editing", async () => {
    const user = userEvent.setup();
    const { setEditorMode } = renderToolbar({ editorMode: "preview" });
    expect(screen.getByRole("button", { name: "粗體" })).toBeDisabled();
    await user.click(screen.getByRole("button", { name: "回到編輯" }));
    expect(setEditorMode).toHaveBeenCalledWith("live");
  });

  it("inserts a table from the form in the menu and closes it", async () => {
    const user = userEvent.setup();
    const { insertTable } = renderToolbar();
    await user.click(screen.getByRole("button", { name: "更多格式" }));
    await user.click(screen.getByRole("button", { name: "插入" }));
    expect(insertTable).toHaveBeenCalledWith(3, 3);
    expect(screen.queryByRole("menu")).not.toBeInTheDocument();
  });

  it("opens the export dialog from the menu", async () => {
    const user = userEvent.setup();
    renderToolbar({ markdownTools: false });
    await user.click(screen.getByRole("button", { name: "更多格式" }));
    // A plain-text page has no Markdown markers and no live mode.
    expect(screen.queryByRole("menuitem", { name: "標題 1" })).not.toBeInTheDocument();
    expect(screen.queryByRole("menuitemradio", { name: "即時編輯" })).not.toBeInTheDocument();
    await user.click(screen.getByRole("menuitem", { name: "匯出檔案…" }));
    expect(screen.getByRole("dialog", { name: "匯出檔案" })).toBeInTheDocument();
  });
});
