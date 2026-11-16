import { describe, expect, it } from "vitest";
import { translate } from "../i18n";
import type { Language } from "../preferences";
import { LOCAL_RUNNER_NAME, runnerAgentProblem, runnerDisplayName } from "./runnerName";

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

describe("runnerAgentProblem", () => {
  const t = tFor("en");

  it("says a program added by hand is gone, in plain words", () => {
    expect(runnerAgentProblem(t, "claude: the program added for it is no longer there")).toBe(
      "The Claude program you added isn't there any more. Add it again or stop using it.",
    );
    expect(runnerAgentProblem(tFor("zh-TW"), "antigravity: the program added for it is no longer there")).toBe(
      "你加入的 Antigravity 程式已經不在原本的位置了，請重新加入或不再使用它。",
    );
  });

  it("keeps the messages it already knew, and anything else as sent", () => {
    expect(runnerAgentProblem(t, "opencode: version 3 is not supported yet; runs need OpenCode 1.x or 2.x")).toBe(
      t("runners.opencodeUnsupported", { major: "3" }),
    );
    expect(
      runnerAgentProblem(
        t,
        'antigravity: runs need "mcp(nodevas)" in permissions.allow of C:\\x.json; run `nodevas setup antigravity`',
      ),
    ).toBe(
      t("runners.antigravityBlocked", { rule: "mcp(nodevas)", path: "C:\\x.json", command: "nodevas setup antigravity" }),
    );
    expect(runnerAgentProblem(t, "claude: the program added for it is no longer there!")).toBe(
      "claude: the program added for it is no longer there!",
    );
    expect(runnerAgentProblem(t, "boom")).toBe("boom");
  });
});
