import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { afterEach, describe, expect, it } from "vitest";
import { installTruncationTitles } from "./truncationTitle";

// Any text cut with an ellipsis shows its whole value on hover or focus, even
// where the component gave it no title: one listener covers the whole app.

const source = (path: string) => readFileSync(resolve(process.cwd(), "src", path), "utf8");

function cut(text: string, { width = 100, full = 300 } = {}): HTMLElement {
  const el = document.createElement("span");
  el.textContent = text;
  el.style.overflow = "hidden";
  el.style.textOverflow = "ellipsis";
  el.style.whiteSpace = "nowrap";
  Object.defineProperty(el, "clientWidth", { configurable: true, get: () => width });
  Object.defineProperty(el, "scrollWidth", { configurable: true, get: () => full });
  document.body.appendChild(el);
  return el;
}

const hover = (el: Element) => el.dispatchEvent(new MouseEvent("pointerover", { bubbles: true }));

let uninstall: () => void = () => {};
afterEach(() => {
  uninstall();
  document.body.innerHTML = "";
});

describe("truncation titles", () => {
  it("gives a cut text its whole value on hover", () => {
    uninstall = installTruncationTitles(document);
    const el = cut("D:\\work\\a-very-long\\repo\\path");
    hover(el);
    expect(el).toHaveAttribute("title", "D:\\work\\a-very-long\\repo\\path");
  });

  it("covers a cut text whose hovered part is a child", () => {
    uninstall = installTruncationTitles(document);
    const el = cut("");
    const inner = document.createElement("b");
    inner.textContent = "settings value that is long";
    el.appendChild(inner);
    hover(inner);
    expect(el).toHaveAttribute("title", "settings value that is long");
  });

  it("gives the title on keyboard focus too", () => {
    uninstall = installTruncationTitles(document);
    const el = cut("focused long label");
    el.tabIndex = 0;
    el.dispatchEvent(new FocusEvent("focusin", { bubbles: true }));
    expect(el).toHaveAttribute("title", "focused long label");
  });

  it("leaves text that fits, and an explicit title, alone", () => {
    uninstall = installTruncationTitles(document);
    const fits = cut("short", { width: 100, full: 100 });
    hover(fits);
    expect(fits).not.toHaveAttribute("title");

    const own = cut("long text cut", { width: 10, full: 300 });
    own.title = "the component's own tooltip";
    hover(own);
    expect(own).toHaveAttribute("title", "the component's own tooltip");
  });

  it("drops its title once the text fits again, and follows text changes", () => {
    uninstall = installTruncationTitles(document);
    let width = 50;
    const el = cut("first long value");
    Object.defineProperty(el, "clientWidth", { configurable: true, get: () => width });
    hover(el);
    expect(el).toHaveAttribute("title", "first long value");
    el.textContent = "second long value";
    hover(el);
    expect(el).toHaveAttribute("title", "second long value");
    width = 400;
    hover(el);
    expect(el).not.toHaveAttribute("title");
  });

  it("stops listening once uninstalled", () => {
    installTruncationTitles(document)();
    const el = cut("long text cut");
    hover(el);
    expect(el).not.toHaveAttribute("title");
  });

  it("is installed for the whole app at start-up", () => {
    expect(source("main.tsx")).toMatch(/installTruncationTitles\(document\)/);
  });

  it("lets a runner's repo path wrap whole instead of cutting it", () => {
    const css = source("styles/17-runners.css");
    const rule = /\.runner-repo-path\s*\{([^}]*)\}/.exec(css)?.[1] ?? "";
    expect(rule).toMatch(/overflow-wrap:\s*anywhere/);
    expect(rule).toMatch(/user-select:\s*text/);
    expect(rule).not.toMatch(/text-overflow:\s*ellipsis/);
  });
});
