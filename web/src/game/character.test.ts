import { describe, expect, it } from "vitest";

import { CHARACTERS, characterOf, type CharacterId } from "./character";

describe("characterOf", () => {
  const CASES: [agent: string, model: string, character: CharacterId][] = [
    // The model first: whoever runs it.
    ["opencode", "deepseek-v4.1-flash-free", "whale"],
    ["opencode", "DeepSeek-V4", "whale"],
    ["antigravity", "claude-opus-5-5", "claude"],
    ["antigravity", "gemini-4-argon", "star"],
    ["opencode", "anthropic/claude-sonnet-4", "claude"],
    ["codex", "opus", "claude"],
    ["codex", "Sonnet", "claude"],
    ["opencode", "haiku-4-5", "claude"],
    ["gemini", "deepseek-r2", "whale"],
    ["claude", "gemini-3.8-flash-high", "star"],
    // No model that says which: the agent kind.
    ["claude", "default", "claude"],
    ["Claude", "", "claude"],
    ["gemini", "default", "star"],
    ["GEMINI", "", "star"],
    // Everyone else is a person.
    ["codex", "gpt-5", "human"],
    ["codex", "default", "human"],
    ["opencode", "default", "human"],
    ["antigravity", "default", "human"],
    ["copilot", "gpt-5", "human"],
    ["", "", "human"],
  ];

  it.each(CASES)("draws %s with %s as %s", (agent, model, character) => {
    expect(characterOf(agent, model)).toBe(character);
  });

  it("lists every character once, the person first", () => {
    expect(CHARACTERS[0]).toBe("human");
    expect(new Set(CHARACTERS).size).toBe(CHARACTERS.length);
    for (const [agent, model, character] of CASES) expect(CHARACTERS, `${agent} ${model}`).toContain(character);
  });
});
