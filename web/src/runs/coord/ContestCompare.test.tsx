import { fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import {
  api,
  type Contest,
  type ContestCompare as ContestCompareData,
  type ContestCompareEntry,
  type PickActionResult,
  type Run,
  type ThemePack,
} from "../../api";
import { translate } from "../../i18n";
import { readPreferences, writePreference } from "../../preferences";
import { useApp } from "../../store";
import { parseThemeManifest } from "../../theme-pack/themeManifest";
import { useThemePacks } from "../../theme-pack/themePackStore";
import { ContestCompare } from "./ContestCompare";

const PACK: ThemePack = {
  id: "studio",
  name: { en: "Studio" },
  version: "1.0.0",
  author: "tester",
  owner: "u1",
  sizeBytes: 1,
  createdAt: "2026-11-18T00:00:00Z",
  updatedAt: "2026-11-18T00:00:00Z",
  mine: true,
  canDelete: true,
};

const MANIFEST = {
  schema: 1,
  id: "studio",
  name: { en: "Studio" },
  version: "1.0.0",
  author: "tester",
  canvas: [200, 240],
  characters: {
    sage: {
      match: { agents: ["claude"], models: ["claude*", "opus*"] },
      title: { en: "Sage" },
      scenes: {
        idle: { image: "sage/idle.webp", x: 0, y: 0, w: 160, h: 200, box: [20, 20, 140, 180] },
      },
    },
    smith: {
      match: { agents: ["codex"], models: ["o3*", "gpt*"] },
      title: { en: "Smith" },
      scenes: {
        idle: { image: "smith/idle.webp", x: 0, y: 0, w: 160, h: 200, box: [20, 20, 140, 180] },
      },
    },
  },
};

function withPack() {
  const value = parseThemeManifest(MANIFEST);
  if (!value) throw new Error("manifest parse failed");
  useThemePacks.setState({ loaded: true, packs: [PACK], defaultId: PACK.id, manifest: { id: PACK.id, value } });
}

function mockWide(wide: boolean) {
  Object.defineProperty(window, "matchMedia", {
    writable: true,
    value: vi.fn().mockImplementation((query: string) => ({
      matches: wide,
      media: query,
      onchange: null,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      addListener: vi.fn(),
      removeListener: vi.fn(),
      dispatchEvent: vi.fn(),
    })),
  });
}

function makeContest(overrides: Partial<Contest> = {}): Contest {
  return {
    id: "contest-1",
    title: "Add dark mode",
    global: false,
    state: "ready",
    entryCount: 2,
    createdBy: "local",
    createdByName: "local",
    createdAt: "2026-11-18T00:00:00Z",
    ...overrides,
  };
}

function makeEntry(overrides: Partial<ContestCompareEntry> = {}): ContestCompareEntry {
  return {
    label: "A",
    runId: "run-a",
    agent: "claude",
    model: "opus",
    status: "succeeded",
    files: [{ path: "src/theme.ts", status: "modified", additions: 24, deletions: 4 }],
    additions: 24,
    deletions: 4,
    summary: "Implemented theme tokens cleanly.",
    ...overrides,
  };
}

function makeCompare(overrides: Partial<ContestCompareData> = {}): ContestCompareData {
  return {
    entries: [
      makeEntry({ label: "A", runId: "run-a", agent: "claude", model: "opus" }),
      makeEntry({
        label: "B",
        runId: "run-b",
        agent: "codex",
        model: "o3",
        files: [{ path: "src/app.css", status: "modified", additions: 12, deletions: 2 }],
        additions: 12,
        deletions: 2,
        summary: "Added CSS variables for dark theme.",
      }),
    ],
    scores: null,
    ...overrides,
  };
}

function makeRun(overrides: Partial<Run> = {}): Run {
  return {
    id: "run-a",
    project: "demo",
    nodeId: "node-1",
    runnerId: "r1",
    runnerName: "work-mac",
    agent: "claude",
    model: "opus",
    repoId: "repo-1",
    repoName: "api",
    base: "main",
    branch: "agent/node-1-aaaa",
    prompt: "",
    status: "succeeded",
    commit: "abc123",
    filesChanged: 1,
    additions: 24,
    deletions: 4,
    exitCode: 0,
    createdBy: "local",
    createdByName: "local",
    createdAt: "2026-11-18T00:00:00Z",
    ...overrides,
  };
}

beforeEach(() => {
  mockWide(true);
  vi.spyOn(api, "listThemePacks").mockResolvedValue({ packs: [], default: "" });
  useThemePacks.getState().reset();
  useThemePacks.setState({ loaded: true, packs: [], defaultId: "", manifest: null });
  writePreference("language", "en");
  writePreference("themePack", "");
  useApp.setState({ preferences: readPreferences() });
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe("ContestCompare", () => {
  it("renders side by side at >= 900px and stacked tabs at phone width", () => {
    mockWide(true);
    const { container, rerender } = render(
      <ContestCompare contest={makeContest()} compare={makeCompare()} onPick={vi.fn()} />,
    );

    expect(container.querySelector(".coord-compare-grid")).not.toBeNull();
    expect(screen.queryByRole("tablist")).toBeNull();
    expect(screen.getByText("Implemented theme tokens cleanly.")).toBeInTheDocument();
    expect(screen.getByText("Added CSS variables for dark theme.")).toBeInTheDocument();

    mockWide(false);
    rerender(<ContestCompare key="phone" contest={makeContest()} compare={makeCompare()} onPick={vi.fn()} />);

    const tablist = screen.getByRole("tablist");
    const tabs = within(tablist).getAllByRole("tab");
    expect(tabs).toHaveLength(2);
    expect(tabs[0]).toHaveAttribute("aria-selected", "true");
    expect(screen.getByText("Implemented theme tokens cleanly.")).toBeInTheDocument();
    expect(screen.queryByText("Added CSS variables for dark theme.")).toBeNull();

    fireEvent.click(tabs[1]);
    expect(tabs[1]).toHaveAttribute("aria-selected", "true");
    expect(screen.getByText("Added CSS variables for dark theme.")).toBeInTheDocument();
    expect(screen.queryByText("Implemented theme tokens cleanly.")).toBeNull();
  });

  it("shows labels and NOT agent/model names before scores, then shows score chips when scored", () => {
    const compareNoScores = makeCompare({ scores: null });
    const { container, rerender } = render(
      <ContestCompare contest={makeContest()} compare={compareNoScores} onPick={vi.fn()} />,
    );

    expect(screen.getByText(translate("contest.entry", "en", { label: "A" }))).toBeInTheDocument();
    expect(screen.getByText(translate("contest.entry", "en", { label: "B" }))).toBeInTheDocument();
    expect(container.textContent).not.toMatch(/claude|codex|opus|\bo3\b/i);

    const compareScored = makeCompare({
      scores: {
        scores: [
          { entry: "A", score: 9, why: "Cleaner separation of tokens" },
          { entry: "B", score: 7, why: "Minimal CSS diff" },
        ],
        pick: "A",
      },
    });
    rerender(<ContestCompare contest={makeContest()} compare={compareScored} onPick={vi.fn()} />);

    expect(screen.getByText(translate("contest.score", "en", { score: 9 }))).toBeInTheDocument();
    expect(screen.getByText(translate("contest.score", "en", { score: 7 }))).toBeInTheDocument();
    expect(screen.getByText("Cleaner separation of tokens")).toBeInTheDocument();
    expect(screen.getByText(translate("contest.advice", "en"))).toBeInTheDocument();
    expect(container.textContent).toMatch(/Claude/i);
    expect(container.textContent).toMatch(/Codex/i);
  });

  it("renders character stills when a pack is active", () => {
    withPack();
    const { container } = render(
      <ContestCompare contest={makeContest()} compare={makeCompare()} onPick={vi.fn()} />,
    );
    const imgs = container.querySelectorAll("img");
    expect(imgs).toHaveLength(2);
    expect(imgs[0].getAttribute("src")).toBe("/api/themes/studio/files/sage/idle.webp");
    expect(imgs[1].getAttribute("src")).toBe("/api/themes/studio/files/smith/idle.webp");
  });

  it("runs pick -> apply -> inline discard confirm listing labels, and submits pick", () => {
    const onPick = vi.fn();
    const compare3 = makeCompare({
      entries: [
        makeEntry({ label: "A", runId: "run-a" }),
        makeEntry({ label: "B", runId: "run-b", agent: "codex", model: "o3" }),
        makeEntry({ label: "C", runId: "run-c", agent: "gemini", model: "2.5-pro" }),
      ],
    });
    render(<ContestCompare contest={makeContest({ entryCount: 3 })} compare={compare3} onPick={onPick} />);

    // Click "Use this one" on Entry A.
    const pickButtons = screen.getAllByRole("button", { name: translate("contest.pick", "en") });
    fireEvent.click(pickButtons[0]);

    // Apply options and "Throw away the others" checkbox (checked by default).
    const discardBox = screen.getByRole("checkbox", { name: translate("contest.discardOthers", "en") });
    expect(discardBox).toBeChecked();

    fireEvent.click(screen.getByRole("button", { name: translate("contest.apply.local", "en") }));

    // Inline confirm lists the labels to be discarded (B and C).
    const expectedList = translate("contest.discardList", "en", { labels: "B, C" });
    expect(screen.getByText(expectedList)).toBeInTheDocument();
    expect(onPick).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole("button", { name: translate("common.confirm", "en") }));
    expect(onPick).toHaveBeenCalledWith({
      runId: "run-a",
      apply: "local",
      discardOthers: true,
    });
  });

  it("displays refused discards with the entry label and reason", () => {
    const pickActions: PickActionResult[] = [
      { runId: "run-a", action: "merge_local", status: "done" },
      { runId: "run-b", action: "discard", status: "failed", code: "unsaved_commits", reason: "unsaved commits" },
    ];
    render(
      <ContestCompare
        contest={makeContest({ state: "decided", winnerRunId: "run-a" })}
        compare={makeCompare()}
        pickActions={pickActions}
      />,
    );

    expect(
      screen.getByText(translate("contest.discardRefused", "en", { label: "B", reason: "unsaved commits" })),
    ).toBeInTheDocument();
  });

  it("opens the diff viewer when Show changes is clicked", () => {
    const onShowChanges = vi.fn();
    render(
      <ContestCompare
        contest={makeContest()}
        compare={makeCompare()}
        runs={[makeRun({ id: "run-a" }), makeRun({ id: "run-b" })]}
        onShowChanges={onShowChanges}
      />,
    );

    const viewButtons = screen.getAllByRole("button", { name: translate("runs.viewChanges", "en") });
    fireEvent.click(viewButtons[0]);
    expect(onShowChanges).toHaveBeenCalledWith("run-a");
  });

  it("shows none of the non-Simple keys or developer words in Simple mode", () => {
    const compareScored = makeCompare({
      scores: {
        scores: [
          { entry: "A", score: 8, why: "Clean change" },
          { entry: "B", score: 6, why: "Longer change" },
        ],
        pick: "A",
      },
    });
    const { container } = render(
      <ContestCompare
        contest={makeContest({ createdBy: "local", createdByName: "local" })}
        compare={compareScored}
        simple={true}
        onPick={vi.fn()}
      />,
    );

    // Trigger pick flow so apply options are rendered too.
    const pickButtons = screen.getAllByRole("button", { name: translate("contest.pick", "en") });
    fireEvent.click(pickButtons[0]);

    const forbiddenKeys = [
      "contest.apply.pr",
      "contest.err.shape",
      "contest.err.outdated",
      "contest.toggle",
      "contest.reviewer",
      "contest.reviewerNone",
      "contest.budget",
    ];
    for (const key of forbiddenKeys) {
      expect(container.textContent).not.toContain(translate(key, "en"));
    }

    // Apply local & keep are present; PR is absent; no developer words or actor "local".
    expect(screen.getByRole("button", { name: translate("contest.apply.local", "en") })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: translate("contest.apply.keep", "en") })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: translate("contest.apply.pr", "en") })).toBeNull();
    expect(container.textContent).not.toMatch(/\b(branch|pull request|PR|runner|worktree|local)\b/i);
  });

  it("shows noScores note when reviewer returned no scores, and noChange when entry changed nothing", () => {
    const compare = makeCompare({
      entries: [
        makeEntry({ label: "A", runId: "run-a", files: [], additions: 0, deletions: 0, summary: "" }),
        makeEntry({ label: "B", runId: "run-b" }),
      ],
      scores: null,
    });
    render(
      <ContestCompare
        contest={makeContest({ state: "ready", judgeRunId: "run-judge" })}
        compare={compare}
        onPick={vi.fn()}
      />,
    );

    expect(screen.getByText(translate("contest.noScores", "en"))).toBeInTheDocument();
    expect(screen.getByText(translate("contest.noChange", "en"))).toBeInTheDocument();
  });

  it("submits pick immediately when Throw away the others is unchecked", () => {
    const onPick = vi.fn();
    render(<ContestCompare contest={makeContest()} compare={makeCompare()} onPick={onPick} />);

    const pickButtons = screen.getAllByRole("button", { name: translate("contest.pick", "en") });
    fireEvent.click(pickButtons[0]);

    const discardBox = screen.getByRole("checkbox", { name: translate("contest.discardOthers", "en") });
    fireEvent.click(discardBox);
    expect(discardBox).not.toBeChecked();

    fireEvent.click(screen.getByRole("button", { name: translate("contest.apply.keep", "en") }));
    expect(onPick).toHaveBeenCalledWith({
      runId: "run-a",
      apply: "keep",
      discardOthers: false,
    });
  });
});
