import { fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { api, type RunnerRepo, type ThemePack } from "../../api";
import { readPreferences, writePreference } from "../../preferences";
import { useApp } from "../../store";
import { parseThemeManifest } from "../../theme-pack/themeManifest";
import { useThemePacks } from "../../theme-pack/themePackStore";
import { RepoCards } from "./RepoCards";

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

const MANIFEST_WITH_ROOMS = {
  schema: 1,
  id: "studio",
  name: { en: "Studio" },
  version: "1.0.0",
  author: "tester",
  canvas: [200, 240],
  rooms: {
    zebra: "rooms/zebra.webp",
    alpha: "rooms/alpha.webp",
  },
  characters: {
    sage: {
      match: { agents: ["claude"], models: [] },
      scenes: {
        idle: { image: "sage/idle.webp", x: 0, y: 0, w: 160, h: 200, box: [0, 0, 160, 200] },
      },
    },
  },
};

function withPack(manifest: object = MANIFEST_WITH_ROOMS) {
  const value = parseThemeManifest(manifest);
  if (!value) throw new Error("test manifest failed to parse");
  useThemePacks.setState({ loaded: true, packs: [PACK], defaultId: PACK.id, manifest: { id: PACK.id, value } });
}

const REPOS: RunnerRepo[] = [
  { id: "repo-api", name: "api", path: "/Users/dev/work/services/api" },
  { id: "repo-web", name: "web", path: "/Users/dev/work/clients/web" },
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

describe("RepoCards", () => {
  it("shows room art stably by repo-id hash with lazy images, and keeps repo name & path tail visible", () => {
    withPack();
    const { unmount } = render(<RepoCards repos={REPOS} value="repo-api" onChange={vi.fn()} />);

    const group = screen.getByRole("radiogroup", { name: "Which folders" });
    const radios = within(group).getAllByRole("radio");
    expect(radios).toHaveLength(2);

    // Repo name and path tail are always visible.
    expect(radios[0]).toHaveTextContent("api");
    expect(radios[0]).toHaveTextContent("services/api");
    expect(radios[1]).toHaveTextContent("web");
    expect(radios[1]).toHaveTextContent("clients/web");

    // Room images are lazy and decorative (alt="").
    const img0 = radios[0].querySelector("img")!;
    const img1 = radios[1].querySelector("img")!;
    expect(img0).not.toBeNull();
    expect(img0).toHaveAttribute("loading", "lazy");
    expect(img0).toHaveAttribute("alt", "");
    expect(img1).toHaveAttribute("loading", "lazy");
    const firstRoomSrc = img0.getAttribute("src");
    expect(firstRoomSrc).toMatch(/^\/api\/themes\/studio\/files\/rooms\/(alpha|zebra)\.webp$/);

    // Broken room image falls back to plain folder SVG without img.
    fireEvent.error(img0);
    expect(radios[0].querySelector("img")).toBeNull();
    expect(radios[0].querySelector("svg")).not.toBeNull();
    unmount();

    // Reordering keys in manifest.rooms picks the exact same room image for repo-api.
    withPack({
      ...MANIFEST_WITH_ROOMS,
      rooms: { alpha: "rooms/alpha.webp", zebra: "rooms/zebra.webp" },
    });
    render(<RepoCards repos={REPOS} value="repo-api" onChange={vi.fn()} />);
    expect(screen.getAllByRole("radio")[0].querySelector("img")?.getAttribute("src")).toBe(firstRoomSrc);
  });

  it("renders plain folder cards (neutral SVG, no img) when no pack or pack has no rooms", () => {
    const { container, rerender } = render(<RepoCards repos={REPOS} value="repo-api" onChange={vi.fn()} />);
    expect(container.querySelector("img")).toBeNull();
    expect(container.querySelectorAll("svg").length).toBe(2);
    expect(screen.getByText("services/api")).toBeInTheDocument();
    expect(screen.getByText("clients/web")).toBeInTheDocument();

    withPack({ ...MANIFEST_WITH_ROOMS, rooms: {} });
    rerender(<RepoCards repos={REPOS} value="repo-api" onChange={vi.fn()} />);
    expect(container.querySelector("img")).toBeNull();
    expect(container.querySelectorAll("svg").length).toBe(2);
  });

  it("supports multi-select mode with native checkboxes, All/None, count, and aria-invalid", () => {
    const onChange = vi.fn();
    render(
      <RepoCards
        mode="many"
        repos={REPOS}
        values={["repo-api"]}
        onChange={onChange}
        invalid
        describedBy="repo-help"
      />,
    );

    const group = screen.getByRole("group", { name: "Which folders" });
    expect(group).toHaveAttribute("aria-invalid", "true");
    expect(group).toHaveAttribute("aria-describedby", "repo-help");
    const boxes = within(group).getAllByRole("checkbox");
    expect(boxes).toHaveLength(2);
    expect(boxes[0]).toBeChecked();
    expect(boxes[1]).not.toBeChecked();
    expect(screen.getByText("1 picked")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "All" }));
    expect(onChange).toHaveBeenLastCalledWith(["repo-api", "repo-web"]);

    fireEvent.click(screen.getByRole("button", { name: "None" }));
    expect(onChange).toHaveBeenLastCalledWith([]);
  });

  it("uses simple label in simple mode", () => {
    render(<RepoCards repos={REPOS} value="repo-api" onChange={vi.fn()} simple />);
    expect(screen.getByRole("radiogroup", { name: "Which folder" })).toBeInTheDocument();
  });
});
