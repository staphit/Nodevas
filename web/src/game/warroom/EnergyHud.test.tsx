// The day's tokens over agent activity. Full mode names the unit once
// ("Today 12k tokens", not "Tokens today 12k tokens"); Simple (single-user
// edition) never says "token": it shows how much of today's limit is used,
// or nothing when there is no limit. An agent's own sentence drops its
// tokens in Simple the same way.

import { render } from "@testing-library/react";
import { beforeEach, describe, expect, it } from "vitest";

import { translate } from "../../i18n";
import { readPreferences, type Language } from "../../preferences";
import { EditionProvider, type Edition } from "../../runners/edition";
import { useApp } from "../../store";
import { actorLabel, type Translate } from "../labels";
import type { Actor } from "../world";
import { EnergyHud } from "./EnergyHud";

function show(energy: { used: number; cap?: number }, edition: Edition, experience: "simple" | "full", language: Language = "en") {
  useApp.setState({ preferences: { ...readPreferences(), language, experience } });
  return render(
    <EditionProvider edition={edition}>
      <EnergyHud energy={energy} />
    </EditionProvider>,
  );
}

const count = (text: string, word: RegExp) => (text.match(word) ?? []).length;

beforeEach(() => {
  useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
});

describe("EnergyHud", () => {
  it.each([
    ["en", /tokens?/gi],
    ["zh-TW", /token/gi],
  ] as const)("names the unit once in full mode (%s)", (language, word) => {
    const { container } = show({ used: 12_000 }, "single", "full", language);
    expect(container.textContent).toMatch(/12k/);
    expect(count(container.textContent ?? "", word)).toBe(1);
  });

  it("names the unit once with a daily limit too", () => {
    const { container } = show({ used: 12_000, cap: 50_000 }, "team", "full");
    expect(container.textContent).toMatch(/12k of 50k tokens/);
    expect(count(container.textContent ?? "", /tokens?/gi)).toBe(1);
  });

  it.each(["en", "zh-TW"] as const)("shows how much of today's limit is used in Simple, without tokens (%s)", (language) => {
    const { container, getByRole } = show({ used: 12_000, cap: 50_000 }, "single", "simple", language);
    expect(container.textContent).not.toMatch(/token|12k|50k/i);
    expect(container.textContent).toMatch(/24%/);
    expect(getByRole("meter")).toHaveAttribute("aria-valuetext", expect.stringMatching(/24%/));
  });

  it("shows nothing in Simple when there is no daily limit", () => {
    const { container } = show({ used: 12_000 }, "single", "simple");
    expect(container.textContent).toBe("");
  });

  it("keeps the tokens on a team hub, whatever the preference says", () => {
    const { container } = show({ used: 12_000 }, "team", "simple");
    expect(container.textContent).toMatch(/12k tokens/);
  });
});

describe("an agent's sentence", () => {
  const actor = {
    id: "a1",
    kind: "run",
    project: "alpha",
    area: "work",
    agent: "claude",
    pose: "working",
    heldMail: [],
    energy: { used: 12_000 },
  } as unknown as Actor;

  it.each(["en", "zh-TW"] as const)("leaves out the tokens in Simple (%s)", (language) => {
    const t: Translate = (key, values) => translate(key, language, values);
    expect(actorLabel(t, actor, "Office", true)).not.toMatch(/token|12k/i);
    expect(actorLabel(t, actor, "Office")).toMatch(/12k token/);
  });
});
