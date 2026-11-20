import { fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { api, type RunnerAgent, type ThemePack } from "../../api";
import { readPreferences, writePreference } from "../../preferences";
import { useApp } from "../../store";
import { parseThemeManifest } from "../../theme-pack/themeManifest";
import { useThemePacks } from "../../theme-pack/themePackStore";
import { AgentCards } from "./AgentCards";

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
  },
};

function withPack() {
  const value = parseThemeManifest(MANIFEST);
  if (!value) throw new Error("test manifest failed to parse");
  useThemePacks.setState({ loaded: true, packs: [PACK], defaultId: PACK.id, manifest: { id: PACK.id, value } });
}

const AGENTS: RunnerAgent[] = [
  { kind: "claude", installed: true, version: "2.1.327", models: [{ id: "default" }] },
  { kind: "codex", installed: true, version: "0.149.1", models: [{ id: "default" }] },
];

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

describe("AgentCards", () => {
  it("renders plain initials on station-avatar with no img or logos when no pack is active", () => {
    const onChange = vi.fn();
    const { container } = render(
      <AgentCards agents={AGENTS} value="claude" onChange={onChange} />,
    );

    const group = screen.getByRole("radiogroup", { name: "Who works" });
    const radios = within(group).getAllByRole("radio");
    expect(radios).toHaveLength(2);
    expect(radios[0]).toHaveTextContent("Claude");
    expect(radios[0]).toHaveTextContent("2.1.327");
    expect(radios[1]).toHaveTextContent("Codex");
    expect(radios[1]).toHaveTextContent("0.149.1");

    expect(container.querySelector("img")).toBeNull();
    expect(container.querySelector("svg")).toBeNull();
    const claudeAvatar = radios[0].querySelector(".station-avatar.agent-claude");
    expect(claudeAvatar).toHaveTextContent("Cl");
    const codexAvatar = radios[1].querySelector(".station-avatar.agent-codex");
    expect(codexAvatar).toHaveTextContent("Co");
    expect(container.textContent).not.toContain("?");

    fireEvent.click(radios[1]);
    expect(onChange).toHaveBeenCalledWith("codex");
  });

  it("renders pack art img with alt='' and falls back to initials on img error", () => {
    withPack();
    const { container } = render(
      <AgentCards agents={AGENTS} value="claude" onChange={vi.fn()} />,
    );

    const radios = screen.getAllByRole("radio");
    const img = radios[0].querySelector("img")!;
    expect(img).not.toBeNull();
    expect(img).toHaveAttribute("alt", "");
    expect(img.getAttribute("src")).toBe("/api/themes/studio/files/sage/idle.webp");

    // Codex has no match in MANIFEST (no fallback character), so it stays plain initials.
    expect(radios[1].querySelector("img")).toBeNull();
    expect(radios[1].querySelector(".station-avatar.agent-codex")).toHaveTextContent("Co");

    // Broken image triggers onError and falls back to plain initials.
    fireEvent.error(img);
    expect(container.querySelector("img")).toBeNull();
    expect(radios[0].querySelector(".station-avatar.agent-claude")).toHaveTextContent("Cl");
  });

  it("gives a signed-out agent no card and shows a muted picker.signedOut line below the cards", () => {
    const mixed: RunnerAgent[] = [
      { kind: "claude", installed: false, signedOut: true, models: [] },
      { kind: "codex", installed: true, version: "0.149.1", models: [{ id: "default" }] },
    ];
    render(<AgentCards agents={mixed} value="codex" onChange={vi.fn()} />);

    const radios = screen.getAllByRole("radio");
    expect(radios).toHaveLength(1);
    expect(radios[0]).toHaveTextContent("Codex");
    expect(screen.queryByRole("radio", { name: /Claude/i })).toBeNull();

    expect(
      screen.getByText("Claude is not signed in on that computer; sign in there to use it"),
    ).toBeInTheDocument();
  });

  it("omits cards when only a signed-out agent exists and still shows picker.signedOut", () => {
    const onlySignedOut: RunnerAgent[] = [
      { kind: "claude", installed: false, signedOut: true, models: [] },
    ];
    render(<AgentCards agents={onlySignedOut} value="" onChange={vi.fn()} />);

    expect(screen.queryAllByRole("radio")).toHaveLength(0);
    expect(
      screen.getByText("Claude is not signed in on that computer; sign in there to use it"),
    ).toBeInTheDocument();
  });

  it("forwards aria-invalid and aria-describedby onto the radiogroup", () => {
    render(
      <AgentCards
        agents={AGENTS}
        value="claude"
        onChange={vi.fn()}
        invalid
        describedBy="agent-err"
      />,
    );
    const group = screen.getByRole("radiogroup", { name: "Who works" });
    expect(group).toHaveAttribute("aria-invalid", "true");
    expect(group).toHaveAttribute("aria-describedby", "agent-err");
  });
});
