import { fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { api, type RunnerModel, type ThemePack } from "../../api";
import { readPreferences, writePreference } from "../../preferences";
import { useApp } from "../../store";
import { parseThemeManifest } from "../../theme-pack/themeManifest";
import { useThemePacks } from "../../theme-pack/themePackStore";
import { ModelCards } from "./ModelCards";

const PACK: ThemePack = {
  id: "studio",
  name: { en: "Studio" },
  version: "1.0.0",
  author: "tester",
  owner: "u1",
  sizeBytes: 1,
  createdAt: "2026-11-18T00:00:00Z",
  updatedAt: "2026-11-18T00:00:00Z",
  mine: true,
  canDelete: true,
};

const MANIFEST = {
  schema: 1,
  id: "studio",
  name: { en: "Studio" },
  version: "1.0.0",
  author: "tester",
  canvas: [200, 240],
  rooms: {},
  characters: {
    sage: {
      match: { agents: ["opencode", "claude"], models: ["claude*"] },
      title: { en: "Sage" },
      scenes: {
        idle: {
          image: "sage/idle.webp",
          x: 0,
          y: 0,
          w: 160,
          h: 200,
          box: [20, 20, 140, 180],
        },
      },
    },
    whale: {
      match: { agents: [], models: ["*deepseek*"] },
      title: { en: "DeepSeek" },
      scenes: {
        idle: {
          image: "whale/idle.webp",
          x: 0,
          y: 0,
          w: 100,
          h: 120,
          box: [10, 12, 90, 112],
        },
      },
    },
  },
};

function withPack() {
  const value = parseThemeManifest(MANIFEST);
  if (!value) throw new Error("test manifest failed to parse");
  useThemePacks.setState({ loaded: true, packs: [PACK], defaultId: PACK.id, manifest: { id: PACK.id, value } });
}

beforeEach(() => {
  vi.spyOn(api, "listThemePacks").mockResolvedValue({ packs: [], default: "" });
  useThemePacks.getState().reset();
  useThemePacks.setState({ loaded: true, packs: [], defaultId: "", manifest: null });
  writePreference("language", "en");
  writePreference("themePack", "");
  useApp.setState({ preferences: readPreferences() });
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe("ModelCards", () => {
  const models: RunnerModel[] = [
    { id: "deepseek-r1", label: "DeepSeek R1" },
    { id: "default", label: "CLI default", default: true },
    { id: "claude-sonnet-4", label: "Claude Sonnet 4" },
  ];

  it("places default first showing the agent's character, and matches deepseek to the DeepSeek card", () => {
    withPack();
    render(<ModelCards agent="opencode" models={models} value="default" onChange={vi.fn()} />);

    const group = screen.getByRole("radiogroup", { name: "Which model" });
    const radios = within(group).getAllByRole("radio");
    expect(radios).toHaveLength(3);

    // default comes first with the agent's character (sage).
    expect(radios[0]).toHaveTextContent("CLI default");
    expect(radios[0].querySelector("[data-character='sage']")).not.toBeNull();
    expect(radios[0].querySelector("img")?.getAttribute("src")).toBe("/api/themes/studio/files/sage/idle.webp");
    expect(radios[0].querySelector("img")).toHaveAttribute("alt", "");

    // deepseek-r1 shows the DeepSeek character (whale) and visible model name.
    const deepseekCard = radios.find((r) => r.textContent?.includes("DeepSeek R1"))!;
    expect(deepseekCard).toBeDefined();
    expect(deepseekCard.querySelector("[data-character='whale']")).not.toBeNull();
    expect(deepseekCard.querySelector("img")?.getAttribute("src")).toBe("/api/themes/studio/files/whale/idle.webp");
  });

  it("uses simple label for default model in simple mode", () => {
    render(<ModelCards agent="claude" models={models} value="default" onChange={vi.fn()} simple />);
    const radios = screen.getAllByRole("radio");
    expect(radios[0]).toHaveTextContent("Its usual model");
  });

  it("renders plain initials without img when no pack is active, and falls back on img error", () => {
    const { container, rerender } = render(
      <ModelCards agent="claude" models={models} value="default" onChange={vi.fn()} />,
    );
    expect(container.querySelector("img")).toBeNull();
    expect(container.querySelector("svg")).toBeNull();
    expect(container.querySelector(".station-avatar.agent-claude")).toHaveTextContent("Cl");

    withPack();
    rerender(<ModelCards agent="claude" models={models} value="default" onChange={vi.fn()} />);
    const radios = screen.getAllByRole("radio");
    const img = radios[0].querySelector("img")!;
    expect(img).not.toBeNull();

    fireEvent.error(img);
    expect(radios[0].querySelector("img")).toBeNull();
    expect(radios[0].querySelector(".station-avatar.agent-claude")).toHaveTextContent("Cl");
  });

  it("shows filter box, character grouping, and picker.more when there are more than 8 models", () => {
    withPack();
    const many: RunnerModel[] = [
      { id: "default", default: true },
      { id: "claude-sonnet-4", label: "Claude Sonnet 4" },
      { id: "claude-opus-4", label: "Claude Opus 4" },
      { id: "claude-haiku-4", label: "Claude Haiku 4" },
      { id: "deepseek-r1", label: "DeepSeek R1" },
      { id: "deepseek-v3", label: "DeepSeek V3" },
      { id: "deepseek-coder", label: "DeepSeek Coder" },
      { id: "claude-3-7", label: "Claude 3.7" },
      { id: "deepseek-chat", label: "DeepSeek Chat" },
      { id: "deepseek-reasoner", label: "DeepSeek Reasoner" },
      { id: "claude-3-5", label: "Claude 3.5" },
    ];
    const onChange = vi.fn();
    render(<ModelCards agent="opencode" models={many} value="default" onChange={onChange} />);

    // Filter box and "3 more models" button appear.
    const filter = screen.getByRole("searchbox", { name: "Find a model" });
    expect(filter).toBeInTheDocument();
    expect(screen.getAllByRole("radio")).toHaveLength(8);
    const moreBtn = screen.getByRole("button", { name: "3 more models" });
    expect(moreBtn).toBeInTheDocument();

    // Filtering searches across all 11 models (including those hidden beyond 8).
    fireEvent.change(filter, { target: { value: "reasoner" } });
    const filtered = screen.getAllByRole("radio");
    expect(filtered).toHaveLength(1);
    expect(filtered[0]).toHaveTextContent("DeepSeek Reasoner");

    // Clearing filter and clicking "3 more models" reveals all 11 models, grouped by character.
    fireEvent.change(filter, { target: { value: "" } });
    fireEvent.click(screen.getByRole("button", { name: "3 more models" }));
    expect(screen.getAllByRole("radio")).toHaveLength(11);
    expect(screen.queryByRole("button", { name: /more models/i })).toBeNull();
    expect(screen.getByText("Sage")).toBeInTheDocument();
    expect(screen.getByText("DeepSeek")).toBeInTheDocument();
  });

  it("does not show filter or picker.more for 8 or fewer models", () => {
    const eight: RunnerModel[] = Array.from({ length: 8 }, (_, i) => ({
      id: i === 0 ? "default" : `m-${i}`,
      label: i === 0 ? undefined : `Model ${i}`,
    }));
    render(<ModelCards agent="claude" models={eight} value="default" onChange={vi.fn()} />);
    expect(screen.queryByRole("searchbox")).toBeNull();
    expect(screen.queryByRole("button", { name: /more models/i })).toBeNull();
    expect(screen.getAllByRole("radio")).toHaveLength(8);
  });

  it("forwards aria-invalid and aria-describedby on the radiogroup", () => {
    render(
      <ModelCards
        agent="claude"
        models={models}
        value="default"
        onChange={vi.fn()}
        invalid
        describedBy="model-err"
      />,
    );
    const group = screen.getByRole("radiogroup", { name: "Which model" });
    expect(group).toHaveAttribute("aria-invalid", "true");
    expect(group).toHaveAttribute("aria-describedby", "model-err");
  });
});
