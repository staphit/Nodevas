import { fireEvent, render, screen, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { CAP_COORD, type Runner } from "../../api";
import { translate } from "../../i18n";
import { readPreferences, writePreference } from "../../preferences";
import { useApp } from "../../store";
import { ContestForm, type ContestDraft } from "./ContestForm";

function makeRunner(overrides: Partial<Runner> = {}): Runner {
  return {
    id: "runner-1",
    name: "work-mac",
    ownerId: "u1",
    ownerName: "Ann",
    createdAt: "2026-11-18T00:00:00Z",
    lastSeenAt: "2026-11-18T00:00:00Z",
    online: true,
    info: {
      version: "1.0.0",
      hostname: "work-mac",
      os: "darwin",
      arch: "arm64",
      capabilities: [CAP_COORD],
      agents: [
        {
          kind: "claude",
          installed: true,
          models: [
            { id: "opus", label: "Opus", default: true },
            { id: "sonnet", label: "Sonnet" },
          ],
        },
        {
          kind: "codex",
          installed: true,
          models: [{ id: "o3", label: "o3", default: true }],
        },
        {
          kind: "gemini",
          installed: true,
          models: [{ id: "2.5-pro", label: "2.5 Pro", default: true }],
        },
      ],
      repos: [{ id: "repo-1", name: "api" }],
    },
    ...overrides,
  };
}

beforeEach(() => {
  writePreference("language", "en");
  useApp.setState({ preferences: readPreferences() });
});

describe("ContestForm", () => {
  it("supports 2 to 3 entries and enforces min 2 / max 3", () => {
    const onChange = vi.fn();
    render(<ContestForm runners={[makeRunner()]} onChange={onChange} />);

    // Starts with 2 entries: Entry A and Entry B.
    expect(screen.getByText(translate("contest.entry", "en", { label: "A" }))).toBeInTheDocument();
    expect(screen.getByText(translate("contest.entry", "en", { label: "B" }))).toBeInTheDocument();
    expect(screen.queryByText(translate("contest.entry", "en", { label: "C" }))).toBeNull();
    // Cannot remove when at 2 entries.
    expect(screen.queryByRole("button", { name: translate("contest.removeEntry", "en") })).toBeNull();

    // Add a 3rd entry.
    fireEvent.click(screen.getByRole("button", { name: translate("contest.addEntry", "en") }));
    expect(screen.getByText(translate("contest.entry", "en", { label: "C" }))).toBeInTheDocument();
    // Cannot add a 4th entry.
    expect(screen.queryByRole("button", { name: translate("contest.addEntry", "en") })).toBeNull();

    // Remove the 3rd entry returns to 2 entries.
    const removeButtons = screen.getAllByRole("button", { name: translate("contest.removeEntry", "en") });
    expect(removeButtons.length).toBeGreaterThan(0);
    fireEvent.click(removeButtons[removeButtons.length - 1]);
    expect(screen.queryByText(translate("contest.entry", "en", { label: "C" }))).toBeNull();
    expect(screen.getByRole("button", { name: translate("contest.addEntry", "en") })).toBeInTheDocument();
  });

  it("refuses duplicate agent + model combinations", () => {
    const onSubmit = vi.fn();
    const duplicateDraft: ContestDraft = {
      entries: [
        { runnerId: "runner-1", agent: "claude", model: "opus" },
        { runnerId: "runner-1", agent: "claude", model: "opus" },
      ],
      reviewer: null,
    };
    render(<ContestForm runners={[makeRunner()]} value={duplicateDraft} onSubmit={onSubmit} />);

    expect(screen.getByRole("alert")).toBeInTheDocument();
    const submitBtn = screen.getByRole("button", { name: translate("common.confirm", "en") });
    expect(submitBtn).toBeDisabled();
    fireEvent.click(submitBtn);
    expect(onSubmit).not.toHaveBeenCalled();
  });

  it("allows an optional reviewer and updates the budget note accordingly", () => {
    const onSubmit = vi.fn();
    render(<ContestForm runners={[makeRunner()]} onSubmit={onSubmit} />);

    // Default: 2 entries, no reviewer => 2 runs.
    expect(screen.getByText(translate("contest.budget", "en", { count: 2 }))).toBeInTheDocument();
    expect(screen.getByText(translate("contest.reviewerNone", "en"))).toBeInTheDocument();

    // Turn on reviewer => 3 runs.
    const reviewerToggle = screen.getByRole("radio", { name: translate("contest.reviewer", "en") });
    fireEvent.click(reviewerToggle);
    expect(screen.getByText(translate("contest.budget", "en", { count: 3 }))).toBeInTheDocument();

    // Add 3rd entry => 4 runs.
    fireEvent.click(screen.getByRole("button", { name: translate("contest.addEntry", "en") }));
    expect(screen.getByText(translate("contest.budget", "en", { count: 4 }))).toBeInTheDocument();

    // Submit delivers 3 entries + reviewer.
    fireEvent.click(screen.getByRole("button", { name: translate("common.confirm", "en") }));
    expect(onSubmit).toHaveBeenCalledTimes(1);
    const submitted = onSubmit.mock.calls[0][0] as ContestDraft;
    expect(submitted.entries).toHaveLength(3);
    expect(submitted.reviewer).not.toBeNull();

    // Turn off reviewer => back to 3 runs.
    const noReviewerToggle = screen.getByRole("radio", { name: translate("contest.reviewerNone", "en") });
    fireEvent.click(noReviewerToggle);
    expect(screen.getByText(translate("contest.budget", "en", { count: 3 }))).toBeInTheDocument();
  });

  it("shows outdated runner message when a runner lacks coord capability", () => {
    const outdated = makeRunner({
      info: {
        ...makeRunner().info!,
        capabilities: [],
      },
    });
    render(<ContestForm runners={[outdated]} onSubmit={vi.fn()} />);

    expect(screen.getByText(translate("contest.err.outdated", "en"))).toBeInTheDocument();
    const form = screen.getByRole("button", { name: translate("common.confirm", "en") });
    expect(form).toBeDisabled();
  });

  it("supports switching agent/model via its radio controls", () => {
    const onChange = vi.fn();
    render(<ContestForm runners={[makeRunner()]} onChange={onChange} />);

    const entryA = screen.getByTestId("contest-entry-0");
    const geminiRadio = within(entryA).getByRole("radio", { name: /Gemini/i });
    fireEvent.click(geminiRadio);

    expect(onChange).toHaveBeenCalled();
    const latest = onChange.mock.calls[onChange.mock.calls.length - 1][0] as ContestDraft;
    expect(latest.entries[0].agent).toBe("gemini");
    expect(latest.entries[0].model).toBe("2.5-pro");
  });

  it("supports picking the reviewer's model and shows shapeError when set", () => {
    const onChange = vi.fn();
    const { rerender } = render(<ContestForm runners={[makeRunner()]} onChange={onChange} />);

    fireEvent.click(screen.getByRole("radio", { name: translate("contest.reviewer", "en") }));
    const reviewerBox = screen.getByTestId("contest-reviewer-pickers");
    fireEvent.click(within(reviewerBox).getByRole("radio", { name: /Sonnet/i }));

    const latest = onChange.mock.calls[onChange.mock.calls.length - 1][0] as ContestDraft;
    expect(latest.reviewer?.agent).toBe("claude");
    expect(latest.reviewer?.model).toBe("sonnet");

    rerender(<ContestForm runners={[makeRunner()]} shapeError={true} onSubmit={vi.fn()} />);
    expect(screen.getByText(translate("contest.err.shape", "en"))).toBeInTheDocument();
    expect(screen.getByRole("button", { name: translate("common.confirm", "en") })).toBeDisabled();
  });
});
