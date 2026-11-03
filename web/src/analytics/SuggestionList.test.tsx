import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { analyticsApi, StaleSuggestionError } from "../api/analytics";
import { readPreferences } from "../preferences";
import { useApp } from "../store";
import SuggestionList from "./SuggestionList";
import { cheaperSuggestion, testerFirstSuggestion } from "./testFixtures";

beforeEach(() => {
  useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
});
afterEach(() => vi.restoreAllMocks());

// W-05: suggestions are applied only after the person confirms them.
describe("SuggestionList", () => {
  it("confirms before applying, then posts the key and change it showed", async () => {
    const apply = vi.spyOn(analyticsApi, "applySuggestion").mockResolvedValue(undefined);
    const onChanged = vi.fn();
    render(<SuggestionList suggestions={[cheaperSuggestion]} onChanged={onChanged} />);
    expect(screen.getByText("executor: codex/gpt-5.5 delivers at the same tier for 40% less than claude/sonnet.")).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Apply…" }));
    const dialog = screen.getByRole("dialog", { name: "Apply this change?" });
    expect(within(dialog).getByText("Routing rule “main”: claude/sonnet → codex/gpt-5.5")).toBeInTheDocument();
    expect(apply).not.toHaveBeenCalled();
    await userEvent.click(within(dialog).getByRole("button", { name: "Apply" }));
    expect(apply).toHaveBeenCalledWith(cheaperSuggestion.key, cheaperSuggestion.change, undefined);
    expect(await screen.findByText("Applied")).toBeInTheDocument();
    expect(onChanged).toHaveBeenCalled();
  });

  it("applies nothing when cancelled", async () => {
    const apply = vi.spyOn(analyticsApi, "applySuggestion").mockResolvedValue(undefined);
    render(<SuggestionList suggestions={[cheaperSuggestion]} />);
    await userEvent.click(screen.getByRole("button", { name: "Apply…" }));
    await userEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Cancel" }));
    expect(apply).not.toHaveBeenCalled();
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("shows the new suggestion when the old one went stale", async () => {
    const fresh = { ...cheaperSuggestion, params: { ...cheaperSuggestion.params, saving: 25 } };
    vi.spyOn(analyticsApi, "applySuggestion").mockRejectedValue(new StaleSuggestionError(fresh));
    render(<SuggestionList suggestions={[cheaperSuggestion]} />);
    await userEvent.click(screen.getByRole("button", { name: "Apply…" }));
    await userEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Apply" }));
    expect(await screen.findByText("This suggestion changed; review it again.")).toBeInTheDocument();
    expect(screen.getByText(/for 25% less/)).toBeInTheDocument();
  });

  it("opens a Settings row instead of applying an open_settings change", async () => {
    const apply = vi.spyOn(analyticsApi, "applySuggestion");
    const onOpenSettings = vi.fn();
    render(<SuggestionList suggestions={[testerFirstSuggestion]} onOpenSettings={onOpenSettings} />);
    expect(screen.queryByRole("button", { name: "Apply…" })).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Open" }));
    expect(onOpenSettings).toHaveBeenCalledWith("stages");
    expect(apply).not.toHaveBeenCalled();
  });

  it("dismisses", async () => {
    const dismiss = vi.spyOn(analyticsApi, "dismissSuggestion").mockResolvedValue(undefined);
    render(<SuggestionList suggestions={[cheaperSuggestion]} project="main" />);
    await userEvent.click(screen.getByRole("button", { name: "Dismiss" }));
    expect(dismiss).toHaveBeenCalledWith(cheaperSuggestion.key, "main");
    expect(screen.queryByText(/40% less/)).not.toBeInTheDocument();
  });
});
