// Agent activity in Simple (single-user edition): the server room's rack is
// this computer, with no "runner" in what it says; full mode keeps it.

import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it } from "vitest";

import { translate } from "../../i18n";
import { readPreferences } from "../../preferences";
import { EditionProvider, type Edition } from "../../runners/edition";
import { useApp } from "../../store";
import { GameActionsProvider } from "../GameActions";
import { rackLabel, runnersActionLabel } from "../labels";
import type { Rack } from "../world";
import { ServerRoom } from "./ServerRoom";

const t = (key: string, values?: Record<string, string | number>) => translate(key, "en", values);

const rack: Rack = {
  runnerId: "local1",
  name: "this machine",
  online: true,
  slots: 2,
  busy: 1,
  agentKinds: ["claude"],
  target: { kind: "runners" },
};

function show(edition: Edition, experience: "simple" | "full") {
  useApp.setState({ preferences: { ...readPreferences(), language: "en", experience } });
  return render(
    <EditionProvider edition={edition}>
      <GameActionsProvider>
        <ServerRoom racks={[rack]} />
      </GameActionsProvider>
    </EditionProvider>,
  );
}

beforeEach(() => {
  useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
});

describe("the server room in Simple", () => {
  it("names the rack this computer and opens its settings, without runner words", () => {
    const { container } = show("single", "simple");
    const button = screen.getByRole("button", { name: /This computer/ });
    expect(button).toHaveAccessibleName("This computer: Online, 1 of 2 at work. Open its settings");
    expect(button).toHaveAttribute("title", "This computer: Online, 1 of 2 at work. Open its settings");
    expect(container.textContent).not.toMatch(/runner|this machine/i);
  });

  it.each([
    ["single", "full"],
    ["team", "simple"],
  ] as const)("keeps the runner words in %s with %s", (edition, experience) => {
    show(edition, experience);
    expect(screen.getByRole("button", { name: /Manage runners/ })).toHaveTextContent("this machine");
  });

  it("says the same in Chinese", () => {
    const zh = (key: string, values?: Record<string, string | number>) => translate(key, "zh-TW", values);
    const said = `${rackLabel(zh, rack, true)} ${runnersActionLabel(zh, true)}`;
    expect(said).not.toMatch(/runner/i);
    expect(said).toMatch(/這台電腦/);
    expect(rackLabel(t, rack, false)).toMatch(/^Runner this machine/);
    expect(runnersActionLabel(t, false)).toBe("Manage runners");
  });
});
