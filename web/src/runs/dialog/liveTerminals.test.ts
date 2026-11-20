import { act, fireEvent, render, screen } from "@testing-library/react";
import { createElement } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  claimTerminal,
  getTerminalOwner,
  releaseTerminal,
  resetLiveTerminals,
  useLiveTerminalClaim,
} from "./liveTerminals";

function Probe({ runId, takeOver = false, label }: { runId: string; takeOver?: boolean; label: string }) {
  const { isOwner, ref, focusOwner } = useLiveTerminalClaim(runId, true, { takeOver });
  return createElement(
    "div",
    { ref, "data-testid": label },
    isOwner
      ? createElement("span", null, `${label}:owner`)
      : createElement("button", { type: "button", onClick: focusOwner }, `${label}:shown-above`),
  );
}

afterEach(() => {
  resetLiveTerminals();
});

describe("liveTerminals", () => {
  it("gives ownership to the first mount and shows 'shown above' on a second mount", () => {
    const first = render(createElement(Probe, { runId: "run-1", label: "first" }));
    const second = render(createElement(Probe, { runId: "run-1", label: "second" }));

    expect(screen.getByText("first:owner")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "second:shown-above" })).toBeInTheDocument();

    const firstEl = screen.getByTestId("first");
    const scrollIntoView = vi.fn();
    firstEl.scrollIntoView = scrollIntoView;
    fireEvent.click(screen.getByRole("button", { name: "second:shown-above" }));
    expect(scrollIntoView).toHaveBeenCalled();

    // When the first mount unmounts, ownership transfers to the second mount.
    first.unmount();
    expect(screen.getByText("second:owner")).toBeInTheDocument();
    second.unmount();
    expect(getTerminalOwner("run-1")).toBeNull();
  });

  it("lets an Open large modal temporarily take over ownership and restores the inline mount on close", () => {
    render(createElement(Probe, { runId: "run-2", label: "inline" }));
    expect(screen.getByText("inline:owner")).toBeInTheDocument();

    const modal = render(createElement(Probe, { runId: "run-2", label: "modal", takeOver: true }));
    expect(screen.getByText("modal:owner")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "inline:shown-above" })).toBeInTheDocument();

    modal.unmount();
    expect(screen.getByText("inline:owner")).toBeInTheDocument();
  });

  it("tracks imperative claims independently per runId", () => {
    expect(claimTerminal("a", "m1")).toBe(true);
    expect(claimTerminal("a", "m2")).toBe(false);
    expect(claimTerminal("b", "m2")).toBe(true);
    expect(getTerminalOwner("a")).toBe("m1");
    expect(getTerminalOwner("b")).toBe("m2");
    act(() => releaseTerminal("a", "m1"));
    expect(getTerminalOwner("a")).toBe("m2");
  });
});
