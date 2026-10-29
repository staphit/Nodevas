import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { NodeTransferResult } from "../api";
import { readPreferences } from "../preferences";
import { useApp } from "../store";
import { pasteNodeClipboard } from "./canvas/nodeTransfer";
import { ConfirmDialogHost } from "./ConfirmDialog";

// A transfer that left something behind has already happened: there is
// nothing to confirm or cancel. The dialog offered "Close" and "Confirm" side
// by side, which reads as a choice. It is a notice with one way out.

const result: NodeTransferResult = {
  ok: true,
  mode: "copy",
  ids: { a: "b" },
  order: ["b"],
  warnings: ["Dependency on “Gamma” stayed behind"],
};

let original: ReturnType<typeof useApp.getState>;

beforeEach(() => {
  original = useApp.getState();
  useApp.setState({
    preferences: { ...readPreferences(), language: "en" },
    activeProject: "beta",
    nodeClipboard: { project: "alpha", ids: ["a"], labels: ["A"], mode: "copy" },
    transferNodes: vi.fn(async () => result) as unknown as ReturnType<typeof useApp.getState>["transferNodes"],
  });
});

afterEach(() => {
  useApp.setState(original);
});

async function openNotice() {
  render(<ConfirmDialogHost />);
  let pasted: Promise<NodeTransferResult | null> = Promise.resolve(null);
  act(() => {
    pasted = pasteNodeClipboard();
  });
  const dialog = await screen.findByRole("alertdialog");
  return { dialog, pasted };
}

describe("the 'completed with items left behind' dialog", () => {
  it("has one primary action, not Close beside Confirm", async () => {
    const { dialog } = await openNotice();
    expect(within(dialog).getByText("Completed with items left behind")).toBeTruthy();
    const footer = dialog.querySelector("footer") as HTMLElement;
    const actions = within(footer).getAllByRole("button");
    expect(actions).toHaveLength(1);
    expect(actions[0].textContent).toBe("OK");
    expect(actions[0].className).toContain("primary");
    expect(within(dialog).queryByRole("button", { name: "Confirm" })).toBeNull();
    expect(document.activeElement).toBe(actions[0]);
  });

  it("goes away with its one button and lets the paste finish", async () => {
    const user = userEvent.setup();
    const { dialog, pasted } = await openNotice();
    await user.click(within(dialog.querySelector("footer") as HTMLElement).getByRole("button"));
    await waitFor(() => expect(screen.queryByRole("alertdialog")).toBeNull());
    await expect(pasted).resolves.toBe(result);
  });

  it("goes away with Escape too", async () => {
    const user = userEvent.setup();
    const { pasted } = await openNotice();
    await user.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("alertdialog")).toBeNull());
    await expect(pasted).resolves.toBe(result);
  });

  it("is worded for a Chinese reader too", async () => {
    useApp.setState({ preferences: { ...readPreferences(), language: "zh-TW" } });
    const { dialog } = await openNotice();
    const footer = dialog.querySelector("footer") as HTMLElement;
    expect(within(footer).getByRole("button").textContent).toBe("確定");
  });
});
