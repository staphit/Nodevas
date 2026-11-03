import { act, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, expect, it } from "vitest";
import { readPreferences } from "../preferences";
import { useApp } from "../store";
import { confirmAction, ConfirmDialogHost } from "./ConfirmDialog";

// A confirmation usually opens from something that finished in the background
// (a paste, a save), not from a click, so React commits it without flushing
// its effects at once. The button took focus in a passive effect: for that gap
// the dialog was on screen with focus left behind, and a keystroke went to the
// page under it. Under load the gap was long enough to fail a focus assertion
// (transferWarningsDialog.test.tsx). Focus now moves in the commit that shows
// the dialog.

let original: ReturnType<typeof useApp.getState>;

beforeEach(() => {
  original = useApp.getState();
  useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
});

afterEach(() => {
  useApp.setState(original);
});

it("focuses its button in the same commit that shows it", async () => {
  render(<ConfirmDialogHost />);
  const focusWhenShown = new Promise<Element | null>((resolve) => {
    const observer = new MutationObserver(() => {
      if (!document.querySelector('[role="alertdialog"]')) return;
      observer.disconnect();
      resolve(document.activeElement);
    });
    observer.observe(document.body, { childList: true, subtree: true });
  });
  // Outside act, from a resolved promise: the way a background task asks.
  let answer: Promise<boolean> = Promise.resolve(false);
  await Promise.resolve().then(() => {
    answer = confirmAction({ title: "Replace it?", description: "The file changed.", confirmLabel: "Replace" });
  });
  const focused = await focusWhenShown;
  expect(focused).toBe(screen.getByRole("button", { name: "Replace" }));
  act(() => screen.getByRole("button", { name: "Replace" }).click());
  await expect(answer).resolves.toBe(true);
});
