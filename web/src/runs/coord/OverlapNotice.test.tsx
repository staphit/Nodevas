import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { Run, RunOverlap } from "../../api";
import { translate } from "../../i18n";
import { readPreferences, writePreference } from "../../preferences";
import { useApp } from "../../store";
import { OverlapNotice } from "./OverlapNotice";

function makeRun(overrides: Partial<Run> = {}): Run {
  return {
    id: "run-1",
    project: "demo",
    nodeId: "node-1",
    runnerId: "runner-1",
    runnerName: "this machine",
    agent: "claude",
    model: "opus",
    repoId: "repo-1",
    repoName: "api",
    base: "main",
    branch: "agent/fix-1",
    prompt: "",
    status: "running",
    exitCode: 0,
    createdBy: "u1",
    createdByName: "Ann",
    createdAt: "2026-11-18T00:00:00Z",
    ...overrides,
  };
}

function makeOverlap(overrides: Partial<RunOverlap> = {}): RunOverlap {
  return {
    runId: "run-2",
    title: "Refactor auth",
    nodeId: "node-2",
    agent: "codex",
    status: "running",
    mine: true,
    hidden: false,
    paths: ["src/auth.ts", "src/api.ts"],
    pathCount: 2,
    strongCount: 2,
    since: "2026-11-18T00:05:00Z",
    ...overrides,
  };
}

beforeEach(() => {
  writePreference("language", "en");
  useApp.setState({ preferences: readPreferences() });
});

describe("OverlapNotice", () => {
  it("renders a single non-expandable line for hidden overlaps with no paths and no count", () => {
    const hiddenOverlap: RunOverlap = {
      hidden: true,
      since: "2026-11-18T00:05:00Z",
      // Even if stray fields existed, hidden must never expose them.
      title: "Secret task",
      paths: ["secret/file.ts"],
      pathCount: 7,
      strongCount: 7,
    };
    const { container } = render(
      <OverlapNotice overlaps={[hiddenOverlap]} run={makeRun({ global: true, mode: "interactive" })} simple={false} onTell={vi.fn()} />,
    );

    expect(screen.getByText(translate("overlap.hidden", "en"))).toBeInTheDocument();
    expect(container.textContent).not.toContain("Secret task");
    expect(container.textContent).not.toContain("secret/file.ts");
    expect(container.textContent).not.toContain("7");
    expect(screen.queryByRole("button", { expanded: false })).toBeNull();
    expect(screen.queryByRole("button", { name: translate("overlap.tell", "en") })).toBeNull();
  });

  it("expands own overlaps to show strong paths and uses kept wording for succeeded runs", () => {
    const active = makeOverlap({
      title: "Refactor auth",
      paths: ["src/auth.ts", "src/api.ts"],
      pathCount: 2,
      strongCount: 2,
      status: "running",
    });
    const kept = makeOverlap({
      runId: "run-3",
      title: "Earlier fix",
      paths: ["src/login.ts"],
      pathCount: 1,
      strongCount: 1,
      status: "succeeded",
    });
    render(<OverlapNotice overlaps={[active, kept]} run={makeRun()} simple={false} />);

    expect(screen.getByText(translate("overlap.title", "en"))).toBeInTheDocument();
    expect(screen.queryByText("src/auth.ts")).toBeNull();

    const toggle = screen.getByRole("button", { expanded: false });
    fireEvent.click(toggle);

    expect(toggle).toHaveAttribute("aria-expanded", "true");
    expect(screen.getByText(translate("overlap.mine", "en", { title: "Refactor auth", count: 2 }))).toBeInTheDocument();
    expect(screen.getByText(translate("overlap.kept", "en", { count: 1 }))).toBeInTheDocument();
    expect(screen.getByText("src/auth.ts")).toBeInTheDocument();
    expect(screen.getByText("src/api.ts")).toBeInTheDocument();
    expect(screen.getByText("src/login.ts")).toBeInTheDocument();
  });

  it("does not count weak add-only paths or render when all overlaps are weak", () => {
    const weakOnly = makeOverlap({
      paths: ["web/src/i18n.ts"],
      pathCount: 1,
      strongCount: 0,
    });
    const { container, rerender } = render(<OverlapNotice overlaps={[weakOnly]} run={makeRun()} simple={false} />);
    expect(container.firstChild).toBeNull();

    const mixed = makeOverlap({
      title: "Add keys",
      paths: ["src/auth.ts", "web/src/i18n.ts", "web/src/other.ts"],
      pathCount: 3,
      strongCount: 1,
    });
    rerender(<OverlapNotice overlaps={[mixed, weakOnly]} run={makeRun()} simple={false} />);

    fireEvent.click(screen.getByRole("button", { expanded: false }));
    expect(screen.getByText(translate("overlap.mine", "en", { title: "Add keys", count: 1 }))).toBeInTheDocument();
    expect(screen.getByText("src/auth.ts")).toBeInTheDocument();
    expect(screen.getByText(translate("overlap.addOnly", "en"))).toBeInTheDocument();
  });

  it("shows Tell it only for eligible global runs and pre-fills with overlap.tellText", () => {
    const overlap = makeOverlap({ paths: ["src/auth.ts", "src/api.ts"], pathCount: 2, strongCount: 2 });
    const onTell = vi.fn();

    // Node run: never shows Tell it.
    const { rerender } = render(
      <OverlapNotice overlaps={[overlap]} run={makeRun({ global: false, mode: "interactive" })} simple={false} onTell={onTell} />,
    );
    expect(screen.queryByRole("button", { name: translate("overlap.tell", "en") })).toBeNull();

    // View-only live interactive global run: cannot type, never shows Tell it.
    rerender(
      <OverlapNotice
        overlaps={[overlap]}
        run={makeRun({ global: true, project: "", nodeId: "", mode: "interactive", viewOnly: true })}
        simple={false}
        onTell={onTell}
      />,
    );
    expect(screen.queryByRole("button", { name: translate("overlap.tell", "en") })).toBeNull();

    // Interactive global run the viewer can type into: calls onTell with overlap.tellText.
    rerender(
      <OverlapNotice
        overlaps={[overlap]}
        run={makeRun({ global: true, project: "", nodeId: "", mode: "interactive", viewOnly: false })}
        simple={false}
        onTell={onTell}
      />,
    );
    const tellBtn = screen.getByRole("button", { name: translate("overlap.tell", "en") });
    fireEvent.click(tellBtn);

    expect(onTell).toHaveBeenCalledTimes(1);
    const expectedText = translate("overlap.tellText", "en", { files: "src/auth.ts, src/api.ts" });
    expect(onTell).toHaveBeenCalledWith(expectedText);
  });

  it("opens the follow-up composer for background global runs", () => {
    const overlap = makeOverlap({ paths: ["src/auth.ts"], pathCount: 1, strongCount: 1 });
    const onTell = vi.fn();
    const onOpenFollowUp = vi.fn();

    render(
      <OverlapNotice
        overlaps={[overlap]}
        run={makeRun({ global: true, project: "", nodeId: "", mode: "", status: "running" })}
        simple={false}
        onTell={onTell}
        onOpenFollowUp={onOpenFollowUp}
      />,
    );

    fireEvent.click(screen.getByRole("button", { name: translate("overlap.tell", "en") }));
    const expectedText = translate("overlap.tellText", "en", { files: "src/auth.ts" });
    expect(onOpenFollowUp).toHaveBeenCalledWith(expectedText);
    expect(onTell).not.toHaveBeenCalled();
  });

  it("renders plain Simple wording when simple is true", () => {
    const overlap = makeOverlap({ paths: ["notes.txt"], pathCount: 1, strongCount: 1 });
    render(<OverlapNotice overlaps={[overlap]} run={makeRun({ global: true })} simple={true} />);

    expect(screen.getByText(translate("overlap.simple.title", "en"))).toBeInTheDocument();
    expect(screen.getByText(translate("overlap.simple.hint", "en"))).toBeInTheDocument();
  });
});
