import { describe, expect, it } from "vitest";
import { translate } from "../i18n";
import type { Language } from "../preferences";
import { LOCAL_RUNNER_NAME, runnerDisplayName } from "./runnerName";

const tFor = (language: Language) => (key: string, values?: Record<string, string | number>) =>
  translate(key, language, values);

// `nodevas serve` registers its own runner under a fixed id, "this machine"
// (runnerwire.LocalRunnerName). That id is English on every screen unless the
// web says it in the reader's language.
describe("runnerDisplayName", () => {
  it("is the hub's fixed id for the local runner", () => {
    expect(LOCAL_RUNNER_NAME).toBe("this machine");
  });

  it("says the local runner in the reader's language", () => {
    expect(runnerDisplayName(tFor("zh-TW"), "this machine")).toBe("本機");
    expect(runnerDisplayName(tFor("en"), "this machine")).toBe("this machine");
  });

  it("leaves a runner's own name as it is", () => {
    expect(runnerDisplayName(tFor("zh-TW"), "mac-mini")).toBe("mac-mini");
    expect(runnerDisplayName(tFor("zh-TW"), "This Machine")).toBe("This Machine");
    expect(runnerDisplayName(tFor("zh-TW"), undefined)).toBe("");
  });
});
