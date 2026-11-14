import { describe, expect, it } from "vitest";

import type { ThemeManifest, ThemeScene } from "../../theme-pack/themeManifest";
import type { Actor } from "../world";
import { characterFor, figureFor, sceneWanted } from "./themeCast";

const scene = (image: string): ThemeScene => ({ image, x: 0, y: 0, w: 100, h: 120, box: [10, 10, 90, 110] });

const scenes = (id: string, names: string[]) => Object.fromEntries(names.map((name) => [name, scene(`${id}/${name}.webp`)]));

function manifest(change?: (m: ThemeManifest) => void): ThemeManifest {
  const m: ThemeManifest = {
    schema: 1,
    id: "magic",
    name: { en: "Magic" },
    version: "1",
    author: "",
    canvas: [100, 120],
    rooms: {},
    characters: {
      sage: {
        match: { agents: ["claude"], models: ["claude*"] },
        sign: "signs/sage.png",
        scenes: scenes("sage", ["idle", "read", "cast", "sleep", "listen"]),
      },
      scribe: { match: { agents: ["codex"], models: ["gpt*", "o3*"] }, scenes: scenes("scribe", ["read"]) },
      fox: { match: { agents: [], models: ["*deepseek*"] }, scenes: scenes("fox", ["idle", "hike"]) },
    },
  };
  change?.(m);
  return m;
}

type Seat = Pick<Actor, "id" | "pose" | "area" | "role" | "bubble">;
const seat = (over: Partial<Seat> = {}): Seat => ({ id: "run:1", pose: "working", area: "work", ...over });

describe("which character an agent is", () => {
  it("goes by its model first, then by its agent", () => {
    expect(characterFor(manifest(), "claude", "claude-opus-5")).toBe("sage");
    // An agent that runs another character's model is drawn as that model.
    expect(characterFor(manifest(), "claude", "gpt-5")).toBe("scribe");
    expect(characterFor(manifest(), "codex", "default")).toBe("scribe");
    expect(characterFor(manifest(), "codex", "")).toBe("scribe");
  });

  it("matches a model pattern whole, whatever its case", () => {
    expect(characterFor(manifest(), "opencode", "opencode/DeepSeek-v4-flash")).toBe("fox");
    expect(characterFor(manifest(), "opencode", "o3-mini")).toBe("scribe");
    expect(characterFor(manifest(), "opencode", "turbo3")).toBeUndefined();
    expect(characterFor(manifest(), "opencode", "my-gpt")).toBeUndefined();
  });

  it("takes a pattern as plain text but for its stars", () => {
    const m = manifest((x) => (x.characters.sage.match.models = ["a.c(*"]));
    expect(characterFor(m, "x", "a.c(1")).toBe("sage");
    expect(characterFor(m, "x", "abc(1")).toBeUndefined();
  });

  it("falls to the pack's fallback character, or to nobody", () => {
    expect(characterFor(manifest(), "gemini", "gemini-4")).toBeUndefined();
    const m = manifest((x) => (x.characters.fox.fallback = true));
    expect(characterFor(m, "gemini", "gemini-4")).toBe("fox");
    expect(characterFor(m, "claude", "claude-opus-5")).toBe("sage");
  });
});

describe("the scene an agent's state asks for", () => {
  it.each<[Partial<Seat>, string]>([
    [{ pose: "asking-user" }, "ask"],
    [{ pose: "failed" }, "fail"],
    [{ pose: "finished" }, "done"],
    [{ pose: "awaiting-accept" }, "wait"],
    [{ pose: "waiting-ci" }, "wait"],
    [{ pose: "offline" }, "sleep"],
    [{ pose: "idle" }, "idle"],
    [{ pose: "reviewing" }, "read"],
    [{ pose: "working" }, "cast"],
    [{ pose: "working", role: "planner" }, "think"],
    [{ pose: "working", role: "researcher" }, "search"],
    [{ pose: "working", role: "reviewer" }, "read"],
    [{ pose: "working", role: "qa" }, "read"],
    [{ pose: "working", role: "tester" }, "cast"],
    [{ pose: "working", role: "executor" }, "write"],
    [{ pose: "working", role: "a-custom-role" }, "cast"],
    [{ pose: "reviewing", area: "meeting" }, "listen"],
    [{ pose: "working", area: "meeting", bubble: {} as Seat["bubble"] }, "speak"],
    // What waits on someone outranks the meeting it sits in.
    [{ pose: "asking-user", area: "meeting" }, "ask"],
    [{ pose: "failed", area: "meeting" }, "fail"],
  ])("%j is %s", (over, wanted) => {
    expect(sceneWanted(seat(over))).toBe(wanted);
  });

  it("sends a run asleep on a usage limit on an outing, the same one every time", () => {
    const outings = new Set(["hike", "stream", "fish", "camp", "bbq", "ride"]);
    const seen = new Set<string>();
    for (let i = 0; i < 60; i++) {
      const wanted = sceneWanted(seat({ pose: "limit", id: `run:${i}` }));
      expect(outings.has(wanted)).toBe(true);
      expect(sceneWanted(seat({ pose: "limit", id: `run:${i}` }))).toBe(wanted);
      seen.add(wanted);
    }
    expect(seen.size).toBe(outings.size);
  });
});

describe("the picture an agent gets", () => {
  it("is its character's scene, with her sign", () => {
    const figure = figureFor(manifest(), { agent: "claude", model: "claude-opus-5" }, "read");
    expect(figure).toEqual({
      key: "sage/read",
      character: "sage",
      sceneName: "read",
      scene: scene("sage/read.webp"),
      sign: "signs/sage.png",
    });
  });

  it("is her idle when she has no such scene, and nothing with no idle either", () => {
    expect(figureFor(manifest(), { agent: "claude", model: "" }, "think")?.sceneName).toBe("idle");
    expect(figureFor(manifest(), { agent: "codex", model: "" }, "think")).toBeUndefined();
    expect(figureFor(manifest(), { agent: "gemini", model: "" }, "read")).toBeUndefined();
  });

  it("is her sleep for an outing she does not go on", () => {
    expect(figureFor(manifest(), { agent: "claude", model: "" }, "hike")?.sceneName).toBe("sleep");
    expect(figureFor(manifest(), { agent: "x", model: "deepseek-v4" }, "hike")?.sceneName).toBe("hike");
    expect(figureFor(manifest(), { agent: "x", model: "deepseek-v4" }, "fish")?.sceneName).toBe("idle");
  });

  it("never takes a scene name for an inherited one", () => {
    expect(figureFor(manifest(), { agent: "codex", model: "" }, "constructor")).toBeUndefined();
  });
});
