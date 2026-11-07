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
    // GPT: its name, OpenAI's, Codex's, or an o-series id as a word of its own.
    ["codex", "gpt-5", "gpt"],
    ["copilot", "gpt-5", "gpt"],
    ["opencode", "gpt-x", "gpt"],
    ["opencode", "openai/gpt-4.1-mini", "gpt"],
    ["opencode", "OpenAI/whatever", "gpt"],
    ["claude", "gpt-5-codex", "gpt"],
    ["antigravity", "codex-mini-latest", "gpt"],
    ["codex", "o3", "gpt"],
    ["opencode", "o1", "gpt"],
    ["opencode", "o4-mini", "gpt"],
    ["opencode", "openrouter/o3-pro", "gpt"],
    ["opencode", "O3", "gpt"],
    // …but not "o" and a digit inside another word.
    ["opencode", "neo3", "human"],
    ["opencode", "pro4-turbo", "human"],
    ["opencode", "o3x", "human"],
    ["opencode", "kimi-k2-video4", "human"],
    // No model that says which: the agent kind.
    ["claude", "default", "claude"],
    ["Claude", "", "claude"],
    ["gemini", "default", "star"],
    ["GEMINI", "", "star"],
    ["codex", "default", "gpt"],
    ["Codex", "", "gpt"],
    // Everyone else is a person.
    ["opencode", "default", "human"],
    ["antigravity", "default", "human"],
    ["copilot", "default", "human"],
    ["", "", "human"],
  ];

  it.each(CASES)("draws %s with %s as %s", (agent, model, character) => {
    expect(characterOf(agent, model)).toBe(character);
  });

  it("lists every character once, the person first", () => {
    expect(CHARACTERS[0]).toBe("human");
    expect(CHARACTERS).toContain("gpt");
    expect(new Set(CHARACTERS).size).toBe(CHARACTERS.length);
    for (const [agent, model, character] of CASES) expect(CHARACTERS, `${agent} ${model}`).toContain(character);
  });
});
