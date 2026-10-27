import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { isChunkLoadError, ViewErrorBoundary } from "./ViewErrorBoundary";

let shouldThrow = true;
function Flaky() {
  if (shouldThrow) throw new Error("render broke");
  return <p>view content</p>;
}

describe("ViewErrorBoundary", () => {
  it("recognises a failed lazy chunk", () => {
    expect(isChunkLoadError(new TypeError("Failed to fetch dynamically imported module: http://x/assets/StationView-abc.js"))).toBe(true);
    expect(isChunkLoadError(new TypeError("Importing a module script failed."))).toBe(true);
    expect(isChunkLoadError(new Error("render broke"))).toBe(false);
  });

  it("keeps the page and lets a render error be retried", async () => {
    const spy = vi.spyOn(console, "error").mockImplementation(() => {});
    shouldThrow = true;
    render(
      <ViewErrorBoundary>
        <Flaky />
      </ViewErrorBoundary>,
    );
    expect(screen.getByRole("alert")).toHaveTextContent("render broke");
    shouldThrow = false;
    await userEvent.click(screen.getByRole("button", { name: "Try again" }));
    expect(screen.getByText("view content")).toBeInTheDocument();
    spy.mockRestore();
  });

  it("offers only a reload when a chunk is missing, and clears on a new resetKey", () => {
    const spy = vi.spyOn(console, "error").mockImplementation(() => {});
    function Missing(): never {
      throw new TypeError("Failed to fetch dynamically imported module: /assets/StationView.js");
    }
    const { rerender } = render(
      <ViewErrorBoundary resetKey="station">
        <Missing />
      </ViewErrorBoundary>,
    );
    expect(screen.getByRole("button", { name: "Reload" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Try again" })).toBeNull();
    rerender(
      <ViewErrorBoundary resetKey="settings">
        <p>other view</p>
      </ViewErrorBoundary>,
    );
    expect(screen.getByText("other view")).toBeInTheDocument();
    spy.mockRestore();
  });
});
