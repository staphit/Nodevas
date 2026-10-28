import { render, screen } from "@testing-library/react";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { createRef } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { readPreferences } from "../../preferences";
import { useApp } from "../../store";
import { SubpageControls } from "./SubpageControls";
import type { NodePages } from "./useNodePages";

// jsdom applies no CSS, so this reads the contract from 06-editor.css. The
// new-subpage form styled only its format select: the title field and the
// Create / Import / Cancel buttons rendered as user-agent boxes (2px inset
// and outset borders, a grey fill) beside every other dark-theme control.
const css = readFileSync(resolve(process.cwd(), "src/styles/06-editor.css"), "utf8").replace(
  /\/\*[\s\S]*?\*\//g,
  "",
);

function rules(selector: string): string {
  const blocks: string[] = [];
  const pattern = /([^{}]+)\{([^}]*)\}/g;
  for (let match = pattern.exec(css); match; match = pattern.exec(css)) {
    const selectors = match[1].split(",").map((part) => part.trim().replace(/\s+/g, " "));
    if (selectors.includes(selector)) blocks.push(match[2]);
  }
  return blocks.join("\n");
}

beforeEach(() => {
  useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
});

describe("new-subpage form controls", () => {
  it.each([".content-page-create input", ".content-page-create button"])(
    "%s is themed like the format select beside it",
    (selector) => {
      const declarations = rules(selector);
      expect(declarations).toMatch(/border:\s*1px solid var\(--border\)/);
      expect(declarations).toMatch(/border-radius:\s*5px/);
      expect(declarations).toMatch(/background:\s*var\(--surface(-2)?\)/);
      expect(declarations).toMatch(/color:\s*var\(--text\)/);
      expect(declarations).toMatch(/font-size:\s*calc\([^;]*var\(--font-scale/);
    },
  );

  it("marks Create page as the form's primary action, filled with the accent", () => {
    const pages = {
      pages: [],
      activePageID: null,
      pageCreateOpen: true,
      pageTitle: "Notes",
      setPageTitle: vi.fn(),
      pageFormat: "md",
      setPageFormat: vi.fn(),
      setPageCreateOpen: vi.fn(),
      pageBusy: false,
      pageError: null,
      pageImportInputRef: createRef<HTMLInputElement>(),
      createPage: vi.fn(),
    } as unknown as NodePages;
    render(<SubpageControls pages={pages} />);
    expect(screen.getByRole("button", { name: "Create page" })).toHaveClass("primary");
    const primary = rules(".content-page-create button.primary");
    expect(primary).toMatch(/background:\s*var\(--accent\)/);
    expect(primary).toMatch(/color:\s*var\(--accent-text\)/);
  });
});
