import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { analyticsApi } from "../api/analytics";
import { readPreferences } from "../preferences";
import { useApp } from "../store";
import RetentionSetting from "./RetentionSetting";

beforeEach(() => {
  useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
});
afterEach(() => vi.restoreAllMocks());

// W-07: raw event retention, the administrator's.
describe("RetentionSetting", () => {
  it("is not shown to a member", () => {
    const get = vi.spyOn(analyticsApi, "retention").mockResolvedValue(180);
    const { container } = render(<RetentionSetting isAdmin={false} />);
    expect(container).toBeEmptyDOMElement();
    expect(get).not.toHaveBeenCalled();
  });

  it("saves Forever as 0", async () => {
    vi.spyOn(analyticsApi, "retention").mockResolvedValue(180);
    const put = vi.spyOn(analyticsApi, "putRetention").mockResolvedValue(0);
    render(<RetentionSetting isAdmin />);
    expect(await screen.findByRole("spinbutton", { name: "Keep raw events for" })).toHaveValue(180);
    expect(screen.getByText("Totals and scorecards are kept forever.")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("checkbox", { name: "Forever" }));
    expect(put).toHaveBeenCalledWith(0);
  });

  it("refuses a value outside 30 to 3650 days", async () => {
    vi.spyOn(analyticsApi, "retention").mockResolvedValue(180);
    const put = vi.spyOn(analyticsApi, "putRetention").mockResolvedValue(90);
    render(<RetentionSetting isAdmin />);
    const days = await screen.findByRole("spinbutton", { name: "Keep raw events for" });
    fireEvent.change(days, { target: { value: "7" } });
    fireEvent.blur(days);
    expect(put).not.toHaveBeenCalled();
    expect(days).toHaveAttribute("aria-invalid", "true");
    fireEvent.change(days, { target: { value: "90" } });
    fireEvent.blur(days);
    expect(put).toHaveBeenCalledWith(90);
  });
});
