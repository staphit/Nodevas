import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { api, type CopilotDraftData, type ThemePack } from "../../api";
import { readPreferences } from "../../preferences";
import { useApp } from "../../store";
import type { ThemeManifest, ThemeScene } from "../../theme-pack/themeManifest";
import { useThemePacks } from "../../theme-pack/themePackStore";
import { entryToItem, useCopilot, type DraftItem } from "../copilotStore";
import { DraftCard } from "./DraftCard";
import { DraftPresenter } from "./DraftPresenter";
import { useDraftUi } from "./draftUi";

vi.mock("../../api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../../api")>();
  return {
    ...actual,
    api: {
      checkSubgraph: vi.fn(),
      projectAccess: vi.fn(),
      listThemePacks: vi.fn(),
      getThemeManifest: vi.fn(),
    },
  };
});

const scene = (image: string, frames?: number): ThemeScene => ({
  image,
  x: 0,
  y: 0,
  w: 100,
  h: 200,
  box: [0, 0, 100, 200],
  ...(frames ? { frames: { count: frames, click: [] } } : {}),
});

const manifest: ThemeManifest = {
  schema: 1,
  id: "pk",
  name: { en: "Pack" },
  version: "1",
  author: "someone",
  canvas: [800, 600],
  rooms: {},
  characters: {
    ann: {
      match: { agents: [], models: ["claude-*"] },
      scenes: { idle: scene("ann/idle.webp") },
      title: { en: "Ann", "zh-TW": "小安" },
      lines: { "asking-user": { en: "Have a look at {node}: {n} of them!", "zh-TW": "看看{node}，共 {n} 步！" } },
    },
    kit: {
      match: { agents: ["codex"], models: [] },
      scenes: { idle: scene("kit/idle.webp"), speak: scene("kit/speak.webp", 4) },
      title: { en: "Kit" },
    },
    bob: {
      match: { agents: [], models: [] },
      fallback: true,
      scenes: { idle: scene("bob/idle.webp") },
    },
  },
  lines: { "asking-user": { en: "A word about {node}.", "zh-TW": "關於{node}，有話跟你說。" } },
};

const data: CopilotDraftData = {
  draft: {
    summary: "Launch",
    nodes: [
      { key: "docs", title: "Write docs", kind: "task" },
      { key: "ship", title: "Ship", kind: "task", after: ["docs"] },
    ],
  },
  issues: [],
  provider: "anthropic",
  model: "claude-x",
  state: "open",
};

function themeOn(value: ThemeManifest | null = manifest) {
  useThemePacks.setState({
    loaded: true,
    packs: [{ id: "pk", name: { en: "Pack" } } as ThemePack],
    defaultId: "pk",
    error: null,
    manifest: { id: "pk", value },
  });
}

function language(code: "en" | "zh-TW") {
  useApp.setState({ preferences: { ...readPreferences(), language: code } });
}

beforeEach(() => {
  useThemePacks.getState().reset();
  language("en");
  useApp.setState({
    activeProject: "main",
    graph: { version: 1, nodes: [], edges: [], ui: {} },
    graphUnreadable: null,
    canvasSelection: { project: "main", ids: [] },
  });
  useCopilot.setState({ items: [], drafts: {}, activeId: "c1", running: false });
  useDraftUi.setState({ ghost: null, checks: {}, busy: {}, canWrite: { project: "main", value: true }, toast: null });
  vi.mocked(api.listThemePacks).mockResolvedValue({ packs: [], default: "" } as never);
  vi.mocked(api.projectAccess).mockResolvedValue({ member: "editor", admin: false, canWrite: true, canManage: false } as never);
  vi.mocked(api.checkSubgraph).mockResolvedValue({ ok: true, ids: {}, created: [], rev: "r", issues: [], dependencies: 1, references: 0 });
});

const presenter = () => document.querySelector<HTMLElement>(".draft-presenter");

describe("DraftPresenter", () => {
  it("shows nothing without a theme pack", () => {
    render(<DraftPresenter data={data} count={2} model="claude-x" />);
    expect(presenter()).toBeNull();
  });

  it("has the character matching the drafting model present the draft, in her own line", () => {
    themeOn();
    render(<DraftPresenter data={data} count={2} model="claude-x" />);
    const box = presenter()!;
    expect(box).toHaveAttribute("data-character", "ann");
    expect(screen.getByRole("region", { name: "Ann presents the draft" })).toBe(box);
    expect(box.querySelector(".draft-presenter-plate")).toHaveTextContent("Ann");
    expect(box.querySelector(".draft-presenter-plate")).toHaveTextContent("claude-x");
    expect(box.querySelector(".draft-presenter-line")).toHaveTextContent("Have a look at “a draft of 2 steps”: 2 of them!");
    expect(box.querySelector("img")).toHaveAttribute("src", "/api/themes/pk/files/ann/idle.webp");
  });

  it("speaks Chinese where the interface does", () => {
    themeOn();
    language("zh-TW");
    render(<DraftPresenter data={data} count={8} model="claude-x" />);
    const box = presenter()!;
    expect(box.querySelector(".draft-presenter-plate")).toHaveTextContent("小安");
    expect(box.querySelector(".draft-presenter-line")).toHaveTextContent("看看「8 步的草稿」，共 8 步！");
  });

  it("falls back to the pack's fallback character, in the pack's line", () => {
    themeOn();
    render(<DraftPresenter data={{ ...data, provider: "ollama", model: "qwen3:8b" }} count={3} model="Ollama · qwen3:8b" />);
    const box = presenter()!;
    expect(box).toHaveAttribute("data-character", "bob");
    // She has no name of her own: the model names her.
    expect(box.querySelector(".draft-presenter-plate")).toHaveTextContent("Ollama · qwen3:8b");
    expect(box.querySelector(".draft-presenter-line")).toHaveTextContent("A word about “a draft of 3 steps”.");
  });

  it("casts a coding agent's plan by its agent, showing the first frame of her speaking scene", () => {
    themeOn();
    const plan: CopilotDraftData = {
      ...data,
      provider: "",
      model: "",
      origin: { runId: "r1", goalId: "g1", agent: "codex", model: "gpt-9" },
    };
    render(<DraftPresenter data={plan} count={5} model="codex · gpt-9" />);
    const box = presenter()!;
    expect(box).toHaveAttribute("data-character", "kit");
    const img = box.querySelector("img")!;
    expect(img).toHaveAttribute("src", "/api/themes/pk/files/kit/speak.webp");
    expect(img.style.width).toBe("400%");
  });

  it("says the app's own line when the pack has none", () => {
    const { lines: _ignored, ...bare } = manifest;
    themeOn({ ...bare, characters: { bob: manifest.characters.bob } });
    render(<DraftPresenter data={data} count={8} model="claude-x" />);
    expect(presenter()!.querySelector(".draft-presenter-line")).toHaveTextContent("I've laid out 8 steps. Take a look?");
  });

  it("shows nothing when the pack has nobody for the model and no fallback", () => {
    themeOn({ ...manifest, characters: { kit: manifest.characters.kit } });
    render(<DraftPresenter data={data} count={2} model="claude-x" />);
    expect(presenter()).toBeNull();
  });
});

describe("DraftCard with a theme pack", () => {
  function item(): DraftItem {
    return entryToItem({ id: "d1", seq: 1, kind: "draft", text: "Launch", data: JSON.stringify(data), createdAt: 1 }) as DraftItem;
  }

  it("has the theme character present an open draft", () => {
    themeOn();
    const draftItem = item();
    useCopilot.setState({ items: [draftItem] });
    render(<DraftCard item={draftItem} active />);
    expect(presenter()).not.toBeNull();
    expect(presenter()!.closest(".draft-card")).not.toBeNull();
  });

  it("has no presenter without a theme", () => {
    const draftItem = item();
    useCopilot.setState({ items: [draftItem] });
    render(<DraftCard item={draftItem} active />);
    expect(presenter()).toBeNull();
  });
});
