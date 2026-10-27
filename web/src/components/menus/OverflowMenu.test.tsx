import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { OverflowMenu } from "./OverflowMenu";

describe("OverflowMenu", () => {
  it("keeps the menu open for a setting and closes it for an action", async () => {
    const user = userEvent.setup();
    const toggle = vi.fn();
    const act = vi.fn();
    render(
      <OverflowMenu
        className="board-overflow"
        label="More"
        items={[
          { id: "snap", label: "Snap", checked: false, onSelect: toggle },
          { id: "fit", label: "Fit", onSelect: act },
        ]}
      />,
    );

    await user.click(screen.getByRole("button", { name: "More" }));
    const snap = screen.getByRole("menuitemcheckbox", { name: "Snap" });
    expect(snap).toHaveAttribute("aria-checked", "false");
    await user.click(snap);
    expect(toggle).toHaveBeenCalledTimes(1);
    expect(screen.getByRole("menu", { name: "More" })).toBeInTheDocument();

    await user.click(screen.getByRole("menuitem", { name: "Fit" }));
    expect(act).toHaveBeenCalledTimes(1);
    expect(screen.queryByRole("menu")).not.toBeInTheDocument();
  });

  it("skips disabled items when it moves focus in", async () => {
    const user = userEvent.setup();
    render(
      <OverflowMenu
        className="board-overflow"
        label="More"
        items={[
          { id: "off", label: "Unavailable", disabled: true, onSelect: vi.fn() },
          { id: "on", label: "Available", onSelect: vi.fn() },
        ]}
      />,
    );

    await user.click(screen.getByRole("button", { name: "More" }));
    expect(screen.getByRole("menuitem", { name: "Available" })).toHaveFocus();
  });

  it("puts extra controls beside the items, outside the menu", async () => {
    const user = userEvent.setup();
    render(
      <OverflowMenu className="board-overflow" label="More" placement="fixed" items={[]}>
        <input type="range" aria-label="Scale" />
      </OverflowMenu>,
    );

    await user.click(screen.getByRole("button", { name: "More" }));
    const slider = screen.getByRole("slider", { name: "Scale" });
    expect(screen.getByRole("menu").contains(slider)).toBe(false);
  });
});
