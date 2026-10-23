import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { readPreferences } from "../../preferences";
import { useApp } from "../../store";
import type { Graph } from "../../types";
import { ExportMenu } from "./ExportMenu";

const exportMocks = vi.hoisted(() => ({
  downloadExport: vi.fn(async () => "Alpha.docx"),
  printExport: vi.fn(async () => {}),
}));
vi.mock("../../editor/documentExport", () => exportMocks);

beforeEach(() => {
  exportMocks.downloadExport.mockClear();
  exportMocks.printExport.mockClear();
  useApp.setState({
    preferences: { ...readPreferences(), language: "zh-TW" },
    activeProject: "demo",
    graph: { nodes: [{ id: "alpha", title: "Alpha" }] } as unknown as Graph,
  });
});

function renderMenu() {
  render(<ExportMenu nodeId="alpha" pageId="notes" pageTitle="Notes" content="# draft" />);
}

describe("ExportMenu", () => {
  // The old dropdown exported on the format click; nothing may leave now
  // until a format is picked and the summary is confirmed.
  it("exports nothing until a format is chosen and confirmed", async () => {
    const user = userEvent.setup();
    renderMenu();

    await user.click(screen.getByRole("button", { name: "匯出檔案" }));
    const confirm = screen.getByRole("button", { name: "匯出" });
    expect(confirm).toBeDisabled();

    await user.click(screen.getByRole("radio", { name: /Word/ }));
    expect(exportMocks.downloadExport).not.toHaveBeenCalled();
    expect(screen.getByText(/這一頁「Alpha › Notes」匯出為 Word \(\.docx\)/)).toBeInTheDocument();

    await user.click(confirm);
    expect(exportMocks.downloadExport).toHaveBeenCalledWith({
      format: "docx",
      scope: "page",
      nodeId: "alpha",
      pageId: "notes",
      content: "# draft",
    });
    expect(screen.getByRole("status")).toHaveTextContent("已匯出 Alpha.docx");
  });

  it("sends the scope that is shown as selected", async () => {
    const user = userEvent.setup();
    renderMenu();

    await user.click(screen.getByRole("button", { name: "匯出檔案" }));
    await user.click(screen.getByRole("radio", { name: /整個專案/ }));
    await user.click(screen.getByRole("radio", { name: /Markdown/ }));
    expect(screen.getByText(/整個專案「demo」匯出為 Markdown/)).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "匯出" }));

    expect(exportMocks.downloadExport).toHaveBeenCalledWith(
      expect.objectContaining({ format: "md", scope: "project" }),
    );
  });

  it("routes PDF through the print dialog", async () => {
    const user = userEvent.setup();
    renderMenu();

    await user.click(screen.getByRole("button", { name: "匯出檔案" }));
    await user.click(screen.getByRole("radio", { name: /整個節點/ }));
    await user.click(screen.getByRole("radio", { name: /PDF/ }));
    await user.click(screen.getByRole("button", { name: "匯出" }));

    expect(exportMocks.printExport).toHaveBeenCalledWith(
      expect.objectContaining({ scope: "node", nodeId: "alpha" }),
    );
    expect(exportMocks.downloadExport).not.toHaveBeenCalled();
  });

  it("closes on cancel without exporting", async () => {
    const user = userEvent.setup();
    renderMenu();

    await user.click(screen.getByRole("button", { name: "匯出檔案" }));
    await user.click(screen.getByRole("button", { name: "取消" }));

    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(exportMocks.downloadExport).not.toHaveBeenCalled();
  });
});
