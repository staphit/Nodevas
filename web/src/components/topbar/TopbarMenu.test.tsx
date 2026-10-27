import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { TopbarMenu, type TopbarMenuEntry } from "./TopbarMenu";

function entries(onSelect = vi.fn()): TopbarMenuEntry[] {
  return [
    { kind: "heading", id: "h", label: "專案" },
    { id: "a", label: "alpha", checked: false, onSelect },
    { id: "b", label: "beta", checked: true, onSelect: vi.fn() },
    { id: "layout", label: "版型：工作流程", disabled: true, separatorBefore: true, onSelect: vi.fn() },
    { id: "settings", label: "專案設定", onSelect: vi.fn() },
  ];
}

function setup(onSelect = vi.fn()) {
  const user = userEvent.setup();
  render(
    <div>
      <TopbarMenu label="專案：beta" trigger={<span>beta</span>} entries={entries(onSelect)} />
      <button type="button">畫布</button>
    </div>,
  );
  return { user, trigger: screen.getByRole("button", { name: "專案：beta" }) };
}

describe("TopbarMenu", () => {
  it("lists choices, headings and a separator", async () => {
    const { user, trigger } = setup();
    expect(trigger).toHaveAttribute("aria-expanded", "false");
    await user.click(trigger);

    expect(trigger).toHaveAttribute("aria-expanded", "true");
    expect(screen.getByRole("menuitemradio", { name: "beta" })).toHaveAttribute("aria-checked", "true");
    expect(screen.getByRole("menuitemradio", { name: "alpha" })).toHaveAttribute("aria-checked", "false");
    // Disabled, yet focusable: aria-disabled rather than the native attribute.
    const layout = screen.getByRole("menuitem", { name: "版型：工作流程" });
    expect(layout).toHaveAttribute("aria-disabled", "true");
    expect(layout).not.toBeDisabled();
    expect(screen.getByRole("separator")).toBeInTheDocument();
  });

  it("ignores a press on a disabled entry and stays open", async () => {
    const onSelect = vi.fn();
    const user = userEvent.setup();
    render(
      <TopbarMenu
        label="m"
        trigger={<span>m</span>}
        entries={[{ id: "x", label: "Fixed", disabled: true, onSelect }]}
      />,
    );
    await user.click(screen.getByRole("button", { name: "m" }));
    await user.click(screen.getByRole("menuitem", { name: "Fixed" }));
    expect(onSelect).not.toHaveBeenCalled();
    expect(screen.getByRole("menu")).toBeInTheDocument();
  });

  it("wraps a group's items in a group named by its heading", async () => {
    const user = userEvent.setup();
    render(
      <TopbarMenu
        label="m"
        trigger={<span>m</span>}
        entries={[
          {
            kind: "group",
            id: "g",
            label: "Projects",
            items: [
              { id: "a", label: "alpha", checked: true, onSelect: vi.fn() },
              { id: "b", label: "beta", checked: false, onSelect: vi.fn() },
            ],
          },
          { id: "s", label: "Settings", onSelect: vi.fn(), separatorBefore: true },
        ]}
      />,
    );
    await user.click(screen.getByRole("button", { name: "m" }));
    const group = screen.getByRole("group", { name: "Projects" });
    expect(within(group).getAllByRole("menuitemradio").map((item) => item.textContent)).toEqual(["✓alpha", "beta"]);
    expect(within(group).queryByRole("menuitem", { name: "Settings" })).toBeNull();
  });

  // The current choice takes focus, so Enter on open keeps where you are.
  it("focuses the checked choice and cycles enabled items with the arrows", async () => {
    const { user, trigger } = setup();
    await user.click(trigger);
    expect(screen.getByRole("menuitemradio", { name: "beta" })).toHaveFocus();

    await user.keyboard("{ArrowDown}");
    // The disabled layout line is reached too: it says why it cannot change.
    expect(screen.getByRole("menuitem", { name: "版型：工作流程" })).toHaveFocus();
    await user.keyboard("{ArrowDown}");
    expect(screen.getByRole("menuitem", { name: "專案設定" })).toHaveFocus();
    await user.keyboard("{ArrowDown}");
    expect(screen.getByRole("menuitemradio", { name: "alpha" })).toHaveFocus();
    await user.keyboard("{End}");
    expect(screen.getByRole("menuitem", { name: "專案設定" })).toHaveFocus();
    await user.keyboard("{Home}");
    expect(screen.getByRole("menuitemradio", { name: "alpha" })).toHaveFocus();
  });

  it("runs the chosen entry and closes", async () => {
    const onSelect = vi.fn();
    const { user, trigger } = setup(onSelect);
    await user.click(trigger);
    await user.click(screen.getByRole("menuitemradio", { name: "alpha" }));

    expect(onSelect).toHaveBeenCalledTimes(1);
    expect(screen.queryByRole("menu")).not.toBeInTheDocument();
  });

  it("closes on Escape and hands the keyboard back to the trigger", async () => {
    const { user, trigger } = setup();
    await user.click(trigger);
    await user.keyboard("{Escape}");

    expect(screen.queryByRole("menu")).not.toBeInTheDocument();
    expect(trigger).toHaveFocus();
  });

  it("closes when a press lands outside it", async () => {
    const { user, trigger } = setup();
    await user.click(trigger);
    expect(screen.getByRole("menu")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "畫布" }));
    expect(screen.queryByRole("menu")).not.toBeInTheDocument();
  });
});
