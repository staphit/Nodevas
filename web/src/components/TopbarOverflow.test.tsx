import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { TopbarOverflow } from "./TopbarOverflow";

/**
 * jsdom's matchMedia always reports "no match" and has no listener plumbing, so
 * the breakpoint is faked here. Width is the whole trigger for this component:
 * above the breakpoint it must render nothing at all.
 */
function mockViewport(narrow: boolean) {
  vi.stubGlobal(
    "matchMedia",
    vi.fn((query: string) => ({
      matches: narrow,
      media: query,
      onchange: null,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      addListener: vi.fn(),
      removeListener: vi.fn(),
      dispatchEvent: vi.fn(),
    })),
  );
}

function items(onSelect = vi.fn()) {
  return [
    { id: "settings", label: "專案設定", icon: null, onSelect },
    { id: "theme", label: "切換深色模式", icon: null, onSelect: vi.fn() },
  ];
}

describe("TopbarOverflow", () => {
  beforeEach(() => mockViewport(true));
  afterEach(() => vi.unstubAllGlobals());

  // The desktop bar has room for the real buttons, and hiding them behind an
  // extra press there would be a regression, not a layout fix.
  it("renders nothing above the breakpoint", () => {
    mockViewport(false);
    const { container } = render(<TopbarOverflow items={items()} />);

    expect(container).toBeEmptyDOMElement();
  });

  it("opens a menu listing every action", async () => {
    const user = userEvent.setup();
    render(<TopbarOverflow items={items()} />);

    const trigger = screen.getByRole("button", { name: "More actions" });
    expect(trigger).toHaveAttribute("aria-expanded", "false");
    await user.click(trigger);

    expect(trigger).toHaveAttribute("aria-expanded", "true");
    expect(screen.getByRole("menuitem", { name: "專案設定" })).toBeInTheDocument();
    expect(screen.getByRole("menuitem", { name: "切換深色模式" })).toBeInTheDocument();
  });

  it("runs the chosen action and closes", async () => {
    const user = userEvent.setup();
    const onSelect = vi.fn();
    render(<TopbarOverflow items={items(onSelect)} />);

    await user.click(screen.getByRole("button", { name: "More actions" }));
    await user.click(screen.getByRole("menuitem", { name: "專案設定" }));

    expect(onSelect).toHaveBeenCalledTimes(1);
    expect(screen.queryByRole("menu")).not.toBeInTheDocument();
  });

  // A menu that opens without focus is unreachable from the keyboard: the
  // trigger is still focused and the items are elsewhere in the tab order.
  it("moves focus into the menu and cycles with the arrow keys", async () => {
    const user = userEvent.setup();
    render(<TopbarOverflow items={items()} />);

    await user.click(screen.getByRole("button", { name: "More actions" }));
    expect(screen.getByRole("menuitem", { name: "專案設定" })).toHaveFocus();

    await user.keyboard("{ArrowDown}");
    expect(screen.getByRole("menuitem", { name: "切換深色模式" })).toHaveFocus();

    // Past the end it wraps rather than trapping the caret on the last row.
    await user.keyboard("{ArrowDown}");
    expect(screen.getByRole("menuitem", { name: "專案設定" })).toHaveFocus();
  });

  it("closes on Escape and hands the keyboard back to the trigger", async () => {
    const user = userEvent.setup();
    render(<TopbarOverflow items={items()} />);

    const trigger = screen.getByRole("button", { name: "More actions" });
    await user.click(trigger);
    await user.keyboard("{Escape}");

    expect(screen.queryByRole("menu")).not.toBeInTheDocument();
    expect(trigger).toHaveFocus();
  });

  it("closes when a press lands outside it", async () => {
    const user = userEvent.setup();
    render(
      <div>
        <TopbarOverflow items={items()} />
        <button type="button">畫布</button>
      </div>,
    );

    await user.click(screen.getByRole("button", { name: "More actions" }));
    expect(screen.getByRole("menu")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "畫布" }));

    expect(screen.queryByRole("menu")).not.toBeInTheDocument();
  });
});
