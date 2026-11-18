// A run's one-line meta under agent activity: full mode counts its tokens;
// Simple (single-user edition) says only what it cost, as the global agents
// list already does.

import { describe, expect, it } from "vitest";

import { makeRun } from "../game/testFixtures";
import { translate } from "../i18n";
import { runMeta } from "./runMenuItems";

const run = makeRun({ status: "succeeded", usage: { inputTokens: 10_000, outputTokens: 2_300, costUsd: 0.042 } });

describe("runMeta", () => {
  it.each(["en", "zh-TW"] as const)("counts tokens in full mode (%s)", (language) => {
    const t = (key: string, values?: Record<string, string | number>) => translate(key, language, values);
    expect(runMeta(t, run, Date.now(), "")).toMatch(/12\.3K tokens?/);
  });

  it.each(["en", "zh-TW"] as const)("says only the cost in Simple (%s)", (language) => {
    const t = (key: string, values?: Record<string, string | number>) => translate(key, language, values);
    const meta = runMeta(t, run, Date.now(), "", true);
    expect(meta).not.toMatch(/token|12\.3K/i);
    expect(meta).toMatch(/\$0\.042/);
  });
});
