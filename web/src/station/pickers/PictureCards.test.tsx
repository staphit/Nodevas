import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { api, type Runner, type ThemePack } from "../../api";
import { readPreferences, writePreference } from "../../preferences";
import { useApp } from "../../store";
import { parseThemeManifest } from "../../theme-pack/themeManifest";
import { useThemePacks } from "../../theme-pack/themePackStore";
import { PictureCards, RunnerCards, RunnerIcon, type PictureCardItem } from "./PictureCards";

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
  rooms: {
    alpha: "rooms/alpha.webp",
  },
  characters: {
    sage: {
      match: { agents: ["claude"], models: ["claude*"] },
      fallback: true,
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

const ITEMS: PictureCardItem[] = [
  { id: "a", label: "Alpha", visual: <span data-testid="vis-a">A</span> },
  { id: "b", label: "Beta", visual: <span data-testid="vis-b">B</span> },
  { id: "c", label: "Gamma", visual: <span data-testid="vis-c">G</span>, disabled: true },
  { id: "d", label: "Delta", visual: <span data-testid="vis-d">D</span> },
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

describe("PictureCards (single-select radiogroup)", () => {
  it("renders radiogroup with aria-checked, roving tabindex, and arrow-key navigation skipping disabled items", () => {
    const onChange = vi.fn();
    const { rerender } = render(
      <PictureCards mode="one" label="Pick one" items={ITEMS} value="a" onChange={onChange} />,
    );

    const group = screen.getByRole("radiogroup", { name: "Pick one" });
    const radios = within(group).getAllByRole("radio");
    expect(radios).toHaveLength(4);
    expect(radios[0]).toHaveAttribute("aria-checked", "true");
    expect(radios[0]).toHaveAttribute("tabindex", "0");
    expect(radios[1]).toHaveAttribute("aria-checked", "false");
    expect(radios[1]).toHaveAttribute("tabindex", "-1");
    expect(radios[2]).toBeDisabled();
    expect(radios[3]).toHaveAttribute("tabindex", "-1");

    // ArrowRight moves to Beta and focuses it.
    fireEvent.keyDown(group, { key: "ArrowRight" });
    expect(onChange).toHaveBeenLastCalledWith("b");
    expect(document.activeElement).toBe(radios[1]);

    // Rerender with value="b"; ArrowDown skips disabled Gamma and lands on Delta.
    rerender(<PictureCards mode="one" label="Pick one" items={ITEMS} value="b" onChange={onChange} />);
    fireEvent.keyDown(group, { key: "ArrowDown" });
    expect(onChange).toHaveBeenLastCalledWith("d");
    expect(document.activeElement).toBe(radios[3]);

    // Rerender with value="d"; ArrowRight wraps around to Alpha.
    rerender(<PictureCards mode="one" label="Pick one" items={ITEMS} value="d" onChange={onChange} />);
    fireEvent.keyDown(group, { key: "ArrowRight" });
    expect(onChange).toHaveBeenLastCalledWith("a");
    expect(document.activeElement).toBe(radios[0]);

    // Rerender with value="a"; ArrowLeft wraps backwards to Delta.
    rerender(<PictureCards mode="one" label="Pick one" items={ITEMS} value="a" onChange={onChange} />);
    fireEvent.keyDown(group, { key: "ArrowLeft" });
    expect(onChange).toHaveBeenLastCalledWith("d");
    expect(document.activeElement).toBe(radios[3]);
  });

  it("sets aria-invalid and aria-describedby on the radiogroup", () => {
    render(
      <PictureCards
        mode="one"
        label="Pick one"
        items={ITEMS}
        value="a"
        onChange={vi.fn()}
        invalid
        describedBy="err-msg"
      />,
    );
    const group = screen.getByRole("radiogroup", { name: "Pick one" });
    expect(group).toHaveAttribute("aria-invalid", "true");
    expect(group).toHaveAttribute("aria-describedby", "err-msg");
  });
});

describe("PictureCards (multi-select checkboxes)", () => {
  it("renders native checkboxes inside fieldset/legend with All/None buttons and picked count", () => {
    const onChange = vi.fn();
    const { rerender } = render(
      <PictureCards mode="many" label="Pick folders" items={ITEMS} values={["a"]} onChange={onChange} />,
    );

    const group = screen.getByRole("group", { name: "Pick folders" });
    expect(group.tagName).toBe("FIELDSET");
    const boxes = within(group).getAllByRole("checkbox");
    expect(boxes).toHaveLength(4);
    expect(boxes[0]).toBeChecked();
    expect(boxes[1]).not.toBeChecked();
    expect(boxes[2]).toBeDisabled();
    expect(screen.getByText("1 picked")).toBeInTheDocument();

    // Toggling Beta adds it.
    fireEvent.click(boxes[1]);
    expect(onChange).toHaveBeenLastCalledWith(["a", "b"]);

    // All selects every enabled item (skips disabled Gamma).
    fireEvent.click(screen.getByRole("button", { name: "All" }));
    expect(onChange).toHaveBeenLastCalledWith(["a", "b", "d"]);

    // None clears all selections.
    rerender(
      <PictureCards mode="many" label="Pick folders" items={ITEMS} values={["a", "b", "d"]} onChange={onChange} />,
    );
    expect(screen.getByText("3 picked")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "None" }));
    expect(onChange).toHaveBeenLastCalledWith([]);
  });

  it("sets aria-invalid and aria-describedby on the multi-select fieldset", () => {
    render(
      <PictureCards
        mode="many"
        label="Pick folders"
        items={ITEMS}
        values={[]}
        onChange={vi.fn()}
        invalid
        describedBy="repo-help"
      />,
    );
    const group = screen.getByRole("group", { name: "Pick folders" });
    expect(group).toHaveAttribute("aria-invalid", "true");
    expect(group).toHaveAttribute("aria-describedby", "repo-help");
  });
});

describe("RunnerIcon and RunnerCards", () => {
  const runners: Runner[] = [
    {
      id: "r1",
      name: "work-mac",
      ownerId: "u1",
      ownerName: "Alice",
      createdAt: "2026-11-18T00:00:00Z",
      lastSeenAt: "2026-11-18T00:00:00Z",
      online: true,
    },
    {
      id: "r2",
      name: "lab-linux",
      ownerId: "u1",
      ownerName: "Alice",
      createdAt: "2026-11-18T00:00:00Z",
      lastSeenAt: "2026-11-18T00:00:00Z",
      online: false,
    },
  ];

  it("renders a fixed SVG icon with the runner name and never uses pack art", () => {
    withPack();
    const { container } = render(
      <>
        <RunnerIcon name="work-mac" />
        <RunnerCards
          runners={runners}
          value="r1"
          onChange={vi.fn()}
          disabledReason={(r) => (r.online ? "" : "offline")}
        />
      </>,
    );

    // No pack images rendered anywhere for runners.
    expect(container.querySelector("img")).toBeNull();
    // Fixed inline SVG icon is present.
    expect(container.querySelectorAll("svg").length).toBeGreaterThanOrEqual(3);

    const group = screen.getByRole("radiogroup", { name: "Which computer" });
    const radios = within(group).getAllByRole("radio");
    expect(radios).toHaveLength(2);
    expect(radios[0]).toHaveTextContent("work-mac");
    expect(radios[0]).toHaveAttribute("aria-checked", "true");
    expect(radios[1]).toHaveTextContent("lab-linux");
    expect(radios[1]).toBeDisabled();
  });
});

describe("picker grid and phone layout CSS contract", () => {
  it("applies the picker-grid class and defines 96px auto-fill, 3-row scroll, and <=400px single-column layout", () => {
    const { container } = render(
      <PictureCards mode="one" label="Pick one" items={ITEMS} value="a" onChange={vi.fn()} />,
    );
    const grid = container.querySelector(".picker-grid");
    expect(grid).not.toBeNull();

    const css = readFileSync(resolve(process.cwd(), "src/styles/33-pickers.css"), "utf8").replace(
      /\/\*[\s\S]*?\*\//g,
      "",
    );
    expect(css).toMatch(/\.picker-grid\s*\{[^}]*grid-template-columns:\s*repeat\(auto-fill,\s*minmax\(96px,\s*1fr\)\)/);
    expect(css).toMatch(/\.picker-grid\s*\{[^}]*overflow-y:\s*auto/);
    expect(css).toMatch(/@(?:media|container)\s*\(max-width:\s*400px\)\s*\{[\s\S]*?\.picker-grid\s*\{[^}]*grid-template-columns:\s*1fr/);
  });
});
