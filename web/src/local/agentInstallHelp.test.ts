import { describe, expect, it } from "vitest";

import { AGENT_INSTALL_HELP, AGENT_KINDS, agentProgram, EXTERNAL_LINK, installHelp } from "./agentInstallHelp";

// Each official page's host, pinned: a link that moves to another host is a
// change a person reviews here, not one that slips in with an edit.
const PINNED_HOSTS: Record<string, string> = {
  claude: "code.claude.com",
  codex: "developers.openai.com",
  gemini: "github.com",
  opencode: "opencode.ai",
  antigravity: "antigravity.google",
};

describe("agentInstallHelp", () => {
  it("covers all five agents, in the runner's order", () => {
    expect([...AGENT_KINDS]).toEqual(["claude", "codex", "gemini", "opencode", "antigravity"]);
    expect(Object.keys(AGENT_INSTALL_HELP).sort()).toEqual([...AGENT_KINDS].sort());
  });

  it.each(Object.entries(PINNED_HOSTS))("sends %s to its official https page on %s", (kind, host) => {
    const help = installHelp(kind);
    expect(help).toBeDefined();
    const url = new URL(help!.url);
    expect(url.protocol).toBe("https:");
    expect(url.host).toBe(host);
    expect(url.username + url.password).toBe("");
  });

  it("knows no help for an unknown agent", () => {
    expect(installHelp("cursor")).toBeUndefined();
    expect(installHelp("__proto__")).toBeUndefined();
  });

  it("opens links in a new tab without handing over this page", () => {
    expect(EXTERNAL_LINK.target).toBe("_blank");
    expect(EXTERNAL_LINK.rel.split(" ").sort()).toEqual(["noopener", "noreferrer"]);
  });

  it("names each agent's program file as the runner looks for it", () => {
    expect(agentProgram("claude")).toBe("claude");
    expect(agentProgram("antigravity")).toBe("agy");
    expect(agentProgram("opencode")).toBe("opencode");
  });
});
