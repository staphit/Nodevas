import { afterEach, beforeEach, describe, expect, it } from "vitest";
import type { Run, Runner } from "../api";
import { useAgents } from "../agents/agentStore";
import { translate } from "../i18n";
import { localRunner } from "../local/localFixtures";
import { readPreferences } from "../preferences";
import { useRunners } from "../runners/runnerStore";
import { useApp } from "../store";
import { declineText } from "./offerReasons";
import { runNotifyText } from "./runNotify";
import { runSignedOut, signInCommand, signInReader, signedOutText } from "./signedOut";

const t = (key: string, values?: Record<string, string | number>) => translate(key, undefined, values);

function run(overrides: Partial<Run> = {}): Run {
  return {
    id: "run7",
    project: "p",
    nodeId: "alpha",
    runnerId: "r1",
    runnerName: "studio-mac",
    agent: "claude",
    model: "opus",
    repoId: "repo1",
    repoName: "site",
    base: "main",
    branch: "agent/alpha-run7",
    prompt: "",
    status: "failed",
    exitCode: 1,
    error: "Not logged in · Please run /login",
    failureKind: "agent_unavailable",
    failureCode: "signed_out",
    createdBy: "u-ann",
    createdByName: "ann",
    createdAt: "2026-11-18T08:00:00Z",
    finishedAt: "2026-11-18T08:00:05Z",
    ...overrides,
  };
}

function hubRunner(ownerId: string): Runner {
  return { ...localRunner(), id: "r1", name: "studio-mac", ownerId, builtIn: false, hubLocal: false };
}

const ann = { id: "u-ann", name: "ann", role: "member" as const };

function language(lang: "en" | "zh-TW") {
  useApp.setState({ preferences: { ...readPreferences(), language: lang } });
}

beforeEach(() => language("en"));
afterEach(() => {
  useRunners.setState({ runners: [], loaded: false });
  useAgents.setState({ viewer: null });
});

describe("a signed-out agent", () => {
  it("is told only by its code, never by the CLI's words", () => {
    expect(runSignedOut(run())).toBe(true);
    expect(runSignedOut(run({ failureCode: undefined }))).toBe(false);
    expect(runSignedOut(run({ status: "succeeded" }))).toBe(false);
  });

  it("names the command that signs each agent in", () => {
    expect(signInCommand("claude")).toBe("claude");
    expect(signInCommand("codex")).toBe("codex login");
    expect(signInCommand("antigravity")).toBe("agy");
  });

  it("is fixed where the agent runs: this computer, the owner's, or someone else's", () => {
    const builtIn = { ...localRunner(), id: "r1" };
    expect(signInReader(run(), [builtIn], ann)).toBe("local");
    expect(signInReader(run(), [hubRunner("u-ann")], ann)).toBe("owner");
    expect(signInReader(run(), [hubRunner("u-bob")], ann)).toBe("other");
    // A server without accounts has one owner.
    expect(signInReader(run(), [hubRunner("local")], null)).toBe("owner");
    // A runner this viewer cannot see is someone else's.
    expect(signInReader(run(), [], ann)).toBe("other");
  });

  it("says how to sign in, in each reader's words", () => {
    expect(signedOutText(t, run(), "local")).toBe(
      "Claude is on this computer but isn't signed in yet. Open the Terminal app on this computer, type claude, press Enter and sign in. Nodevas notices within a minute; then try again.",
    );
    expect(signedOutText(t, run({ agent: "codex" }), "owner")).toBe(
      "Codex isn't signed in on studio-mac. Open a terminal there, run codex login and sign in. Nodevas notices within a minute; then start this task again here.",
    );
    const other = signedOutText(t, run(), "other");
    expect(other).toBe(
      "Claude isn't signed in on studio-mac. Only the person that computer belongs to can sign it in: ask them, or pick another agent.",
    );
    expect(other).not.toMatch(/claude login|run claude/);
    language("zh-TW");
    expect(signedOutText(t, run(), "local")).toBe(
      "Claude 已經在這台電腦上，但還沒登入。請在這台電腦打開「終端機」，輸入 claude 後按 Enter 鍵並登入。Nodevas 會在一分鐘內發現，之後再試一次。",
    );
    expect(signedOutText(t, run(), "owner")).toBe(
      "Claude 在 studio-mac 上還沒登入。請在那台電腦打開終端機執行 claude 並登入。Nodevas 會在一分鐘內發現，之後再回到這裡重新執行這個工作。",
    );
  });
});

describe("its toast", () => {
  it("on this computer, says it plainly instead of the CLI's English", () => {
    useRunners.setState({ runners: [localRunner()], loaded: true });
    const text = runNotifyText(run({ runnerId: "local1", runnerName: "this machine" }), "failed");
    expect(text.title).toBe("Claude needs you to sign in first");
    expect(text.body).toMatch(/^Claude is on this computer but isn't signed in yet\./);
    expect(`${text.title} ${text.body}`).not.toMatch(/\/login|Not logged in/);
    const global = runNotifyText(
      run({ runnerId: "local1", runnerName: "this machine", global: true, nodeId: "", title: "Bakery page" }),
      "failed",
    );
    expect(global.title).toBe("Claude needs you to sign in first");
  });

  it("on a team hub, gives the owner the command and anyone else whom to ask", () => {
    useRunners.setState({ runners: [hubRunner("u-ann")], loaded: true });
    useAgents.setState({ viewer: ann });
    expect(runNotifyText(run(), "failed").body).toMatch(/Open a terminal there, run claude/);
    expect(runNotifyText(run({ global: true, nodeId: "", title: "Fix it" }), "failed").body).toMatch(/run claude/);
    useRunners.setState({ runners: [hubRunner("u-bob")], loaded: true });
    expect(runNotifyText(run(), "failed").body).toMatch(/Only the person that computer belongs to/);
  });

  it("keeps the error of any other failure", () => {
    useRunners.setState({ runners: [hubRunner("u-ann")], loaded: true });
    expect(runNotifyText(run({ failureCode: undefined, error: "tests failed" }), "failed").body).toBe("tests failed");
  });
});

describe("an offer declined for it", () => {
  it("says it is not signed in, not that it is missing", () => {
    expect(declineText(t, run({ status: "declined", declineCode: "agent_signed_out" }), false)).toBe(
      "Claude isn't signed in on studio-mac.",
    );
    expect(declineText(t, run({ status: "declined", declineCode: "agent_signed_out" }), true)).toBe(
      "Claude isn't signed in on this computer.",
    );
  });
});
