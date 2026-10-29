import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createRef } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { translate } from "../../i18n";
import { readPreferences } from "../../preferences";
import { useApp } from "../../store";
import { SubpageControls, SubpageMenu } from "./SubpageControls";
import type { NodePages } from "./useNodePages";

beforeEach(() => {
  useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
});

function fakePages(over: Partial<NodePages> = {}): NodePages {
  return {
    pages: [{ id: "p1", title: "Draft" }],
    pagesLoading: false,
    activePageID: null,
    activeFormat: "md",
    pageDoc: null,
    setPageDoc: vi.fn(),
    pageError: null,
    setPageError: vi.fn(),
    pageCreateOpen: false,
    setPageCreateOpen: vi.fn(),
    pageTitle: "",
    setPageTitle: vi.fn(),
    pageFormat: "md",
    setPageFormat: vi.fn(),
    pageBusy: false,
    pageRename: "",
    setPageRename: vi.fn(),
    pageImportInputRef: createRef<HTMLInputElement>(),
    selectPage: vi.fn(),
    importPage: vi.fn(),
    createPage: vi.fn(),
    convertPage: vi.fn(),
    renamePage: vi.fn(),
    movePage: vi.fn(),
    removePage: vi.fn(),
    ...over,
  } as unknown as NodePages;
}

// A node's pages are tabs in plain sight, with adding one and popping one
// out as buttons beside them: no dropdown to open first.
describe("SubpageMenu", () => {
  it("shows every page as a tab, the open one selected, and no dropdown", async () => {
    const pages = fakePages({ activePageID: "p1" });
    render(<SubpageMenu pages={pages} onPopout={vi.fn()} />);
    const tabs = screen.getByRole("tablist", { name: "Content pages" });
    expect(within(tabs).getByRole("tab", { name: "Main page" })).toHaveAttribute("aria-selected", "false");
    expect(within(tabs).getByRole("tab", { name: "Draft" })).toHaveAttribute("aria-selected", "true");
    expect(screen.queryByRole("menu")).not.toBeInTheDocument();
    expect(document.querySelector("[aria-haspopup]")).toBeNull();
    await userEvent.click(within(tabs).getByRole("tab", { name: "Main page" }));
    expect(pages.selectPage).toHaveBeenCalledWith(null);
  });

  it("uses the language's parentheses in a tab hint", () => {
    useApp.setState({
      preferences: { ...useApp.getState().preferences, language: "zh-TW" },
    });
    const pages = fakePages({ pages: [{ id: "p1", title: "草稿", format: "html" }] });
    render(<SubpageMenu pages={pages} onPopout={vi.fn()} />);
    expect(screen.getByRole("tab", { name: "草稿" })).toHaveAttribute(
      "title",
      `草稿（${translate("format.html", "zh-TW")}）`,
    );
  });

  it("adds a subpage and pops out from buttons beside the tabs", async () => {
    const pages = fakePages();
    const onPopout = vi.fn();
    render(<SubpageMenu pages={pages} onPopout={onPopout} />);
    await userEvent.click(screen.getByRole("button", { name: "Add subpage" }));
    expect(pages.setPageCreateOpen).toHaveBeenCalledWith(true);
    await userEvent.click(screen.getByRole("button", { name: "Pop out current page" }));
    expect(onPopout).toHaveBeenCalled();
  });

  it("offers importing a file inside the new-subpage form", async () => {
    const pages = fakePages({ pageCreateOpen: true });
    const input = document.createElement("input");
    const click = vi.spyOn(input, "click").mockImplementation(() => {});
    (pages.pageImportInputRef as { current: HTMLInputElement | null }).current = input;
    render(<SubpageControls pages={pages} />);
    await userEvent.click(screen.getByRole("button", { name: "Import a file as a subpage" }));
    expect(click).toHaveBeenCalled();
  });
});
