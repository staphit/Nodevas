import { fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { api, type ThemePack } from "../../api";
import { readPreferences, writePreference } from "../../preferences";
import { useApp } from "../../store";
import { parseThemeManifest } from "../../theme-pack/themeManifest";
import { useThemePacks } from "../../theme-pack/themePackStore";
import { ModeCards } from "./ModeCards";

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
      match: { agents: ["claude"], models: ["claude*"] },
      title: { en: "Sage" },
      scenes: {
        idle: { image: "sage/idle.webp", x: 0, y: 0, w: 160, h: 200, box: [20, 20, 140, 180] },
        speak: { image: "sage/speak.webp", x: 0, y: 0, w: 160, h: 200, box: [20, 20, 140, 180] },
        write: { image: "sage/write.webp", x: 0, y: 0, w: 160, h: 200, box: [20, 20, 140, 180] },
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

describe("ModeCards", () => {
  it("shows speak still for interactive and write still for background when a pack is active", () => {
    withPack();
    const onChange = vi.fn();
    render(<ModeCards agent="claude" model="default" value="interactive" onChange={onChange} />);

    const group = screen.getByRole("radiogroup", { name: "Mode" });
    const radios = within(group).getAllByRole("radio");
    expect(radios).toHaveLength(2);
    expect(radios[0]).toHaveTextContent("Talk with it");
    expect(radios[0]).toHaveAttribute("aria-checked", "true");
    expect(radios[0].querySelector("img")?.getAttribute("src")).toBe("/api/themes/studio/files/sage/speak.webp");
    expect(radios[0].querySelector("img")).toHaveAttribute("alt", "");

    expect(radios[1]).toHaveTextContent("Background");
    expect(radios[1]).toHaveAttribute("aria-checked", "false");
    expect(radios[1].querySelector("img")?.getAttribute("src")).toBe("/api/themes/studio/files/sage/write.webp");

    // Arrow key switches from interactive to background.
    fireEvent.keyDown(group, { key: "ArrowRight" });
    expect(onChange).toHaveBeenCalledWith("headless");
  });

  it("falls back to plain initials without img when no pack is active or on image error", () => {
    const { container, rerender } = render(
      <ModeCards agent="claude" value="headless" onChange={vi.fn()} />,
    );
    expect(container.querySelector("img")).toBeNull();
    expect(container.querySelectorAll(".station-avatar.agent-claude")).toHaveLength(2);

    withPack();
    rerender(<ModeCards agent="claude" value="headless" onChange={vi.fn()} />);
    const radios = screen.getAllByRole("radio");
    const speakImg = radios[0].querySelector("img")!;
    fireEvent.error(speakImg);
    expect(radios[0].querySelector("img")).toBeNull();
    expect(radios[0].querySelector(".station-avatar.agent-claude")).toHaveTextContent("Cl");
  });

  it("disables interactive card when interactiveDisabled is set and renders note and aria-invalid", () => {
    render(
      <ModeCards
        agent="claude"
        value="headless"
        onChange={vi.fn()}
        interactiveDisabled
        note="Several folders run in the background"
        invalid
        describedBy="mode-note"
      />,
    );
    const group = screen.getByRole("radiogroup", { name: "Mode" });
    expect(group).toHaveAttribute("aria-invalid", "true");
    expect(group).toHaveAttribute("aria-describedby", "mode-note");
    const radios = within(group).getAllByRole("radio");
    expect(radios[0]).toBeDisabled();
    expect(radios[1]).not.toBeDisabled();
    expect(screen.getByText("Several folders run in the background")).toBeInTheDocument();
  });
});
