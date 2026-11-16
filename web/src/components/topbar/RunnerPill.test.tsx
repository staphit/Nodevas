// The pill in the single-user edition: "This computer", and what its grey
// dot means. (The team pill is covered in TopBar.test.tsx.)

import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { RunBudgetStatus, Runner } from "../../api";
import { localRunner, restartedRunner, startingRunner } from "../../local/localFixtures";
import { readPreferences } from "../../preferences";
import { EditionProvider } from "../../runners/edition";
import { useRunners } from "../../runners/runnerStore";
import { useBudget } from "../../runs/budgetStore";
import { useApp } from "../../store";
import { RunnerPill } from "./RunnerPill";

function pill(runners: Runner[]) {
  useRunners.setState({ runners });
  render(
    <EditionProvider edition="single">
      <RunnerPill />
    </EditionProvider>,
  );
  return screen.getByRole("button");
}

beforeEach(() => {
  useApp.setState({ preferences: { ...readPreferences(), language: "en", agentWorkstation: true } });
  // Nothing here reaches the hub: the lists are already loaded.
  useRunners.setState({ loaded: true, loading: false, runners: [], refresh: vi.fn(async () => {}) });
  useBudget.setState({
    status: { used: { runs: 2, costUsd: 1.2, tokens: 10 }, exceeded: "" } as unknown as RunBudgetStatus,
    load: vi.fn(async () => {}),
  });
});

describe("RunnerPill, single-user edition", () => {
  it("is This computer and today's spend, green while agents can run", () => {
    const button = pill([localRunner()]);
    expect(button).toHaveAccessibleName("This computer, $1.20 spent today");
    expect(button.textContent).toBe("This computer · $1.20");
    expect(button.querySelector(".runner-dot.online")).not.toBeNull();
  });

  it.each([
    ["on a first start", () => startingRunner()],
    ["after a restart", () => restartedRunner()],
  ])("says it is getting ready until its runner has connected, %s", (_when, runner) => {
    const button = pill([runner()]);
    expect(button).toHaveAccessibleName("This computer: getting ready to run agents");
    expect(button).toHaveAttribute("title", "This computer: getting ready to run agents — open Station");
    expect(button.textContent).toBe("This computer · $1.20");
    expect(button.querySelector(".runner-dot.online")).toBeNull();
  });

  it("says agents cannot start once it has stopped", () => {
    const stopped = pill([{ ...localRunner(), online: false }]);
    expect(stopped).toHaveAccessibleName("This computer: agents can't start right now");
    expect(stopped.querySelector(".runner-dot.online")).toBeNull();
    expect(stopped.getAttribute("title")).not.toMatch(/runner/i);
  });

  // serve --no-runner with a runner the person connected by hand online:
  // the dispatch dialog sends runs there, so the pill must not say that
  // agents cannot start. Once the app's own runner was welcomed the dialog
  // sticks to it, so its being down is what the pill says.
  it("does not say agents cannot start while the dialog can use a runner connected by hand", () => {
    const laptop: Runner = { ...localRunner(), id: "r9", name: "laptop", builtIn: undefined, hubLocal: undefined };
    expect(pill([laptop])).toHaveAccessibleName("This computer, $1.20 spent today");
  });

  it("says agents cannot start when the app's own runner dropped, whatever else is connected", () => {
    const laptop: Runner = { ...localRunner(), id: "r9", name: "laptop", builtIn: undefined, hubLocal: undefined };
    expect(pill([{ ...localRunner(), online: false }, laptop])).toHaveAccessibleName(
      "This computer: agents can't start right now",
    );
  });

  it("says nothing about its state before the list has arrived", () => {
    useRunners.setState({ loaded: false });
    expect(pill([])).toHaveAccessibleName("This computer, $1.20 spent today");
  });
});
