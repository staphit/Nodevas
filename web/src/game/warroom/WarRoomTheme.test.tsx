import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { api, type ThemePack } from "../../api";
import { readPreferences, writePreference } from "../../preferences";
import { useApp } from "../../store";
import { parseThemeManifest } from "../../theme-pack/themeManifest";
import { useThemePacks } from "../../theme-pack/themePackStore";
import { buildBuilding, type BuildingInputs } from "../building";
import { GameActionsProvider } from "../GameActions";
import { ago, makeRun, makeRunner, NOW } from "../testFixtures";
import type { Building } from "../world";
import { StoryChoices } from "./StoryTalk";
import WarRoomStage from "./WarRoom";

const GRAPH = {
  version: 1,
  type: "workflow",
  nodes: [
    { id: "login", title: "Login form" },
    { id: "schema", title: "DB schema" },
    { id: "docs", title: "API docs" },
  ],
};
const titles = new Map(GRAPH.nodes.map((node) => [node.id, node.title]));
const titleOf = (id: string) => titles.get(id) ?? "";

const PACK: ThemePack = {
  id: "magic",
  name: { en: "Magic" },
  version: "1.0.0",
  author: "someone",
  owner: "u-bea",
  sizeBytes: 1,
  createdAt: ago(1_000),
  updatedAt: ago(1_000),
  mine: true,
  canDelete: true,
};

const whole = (image: string, more: object = {}) => ({ image, x: 0, y: 0, w: 100, h: 120, box: [10, 10, 90, 110], ...more });

/** A sage for Claude, in three states; nobody for any other agent. */
const SAGE = {
  match: { agents: ["claude"], models: ["claude*"] },
  sign: "signs/sage.png",
  scenes: {
    // What a run does as an executor, the role a run has by default.
    write: whole("sage/write.webp", { frames: { count: 3, blink: 1, click: [2] }, sign: [50, 12, 20] }),
    fail: whole("sage/fail.webp"),
    idle: whole("sage/idle.webp"),
  },
};
const MANIFEST = {
  schema: 1,
  id: "magic",
  name: { en: "Magic" },
  version: "1.0.0",
  author: "someone",
  canvas: [100, 120],
  characters: { sage: SAGE },
};

function building(over: Partial<BuildingInputs> = {}): Building {
  const base = makeRunner({ id: "rn1", name: "mac-mini" });
  return buildBuilding({
    graph: GRAPH,
    runs: [],
    runners: [{ ...base, info: { ...base.info!, maxConcurrent: 6 } }],
    sessions: [],
    asks: [],
    held: [],
    messages: [],
    now: NOW,
    activeProject: "main",
    projectLabel: "Main",
    remote: null,
    waitingItems: [],
    ...over,
  });
}

function mount(world: Building) {
  return render(
    <GameActionsProvider>
      <WarRoomStage building={world} titleOf={titleOf} rowOf={() => undefined} />
    </GameActionsProvider>,
  );
}

function withPack(manifest: object = MANIFEST) {
  const value = parseThemeManifest(manifest);
  if (!value) throw new Error("the test's manifest does not parse");
  useThemePacks.setState({ loaded: true, packs: [PACK], defaultId: PACK.id, manifest: { id: PACK.id, value } });
}

beforeEach(() => {
  vi.spyOn(api, "listThemePacks").mockResolvedValue({ packs: [], default: "" });
  useThemePacks.getState().reset();
  // Stored, not only held: a preference a test sets has them all read back.
  writePreference("language", "en");
  writePreference("agentWorkstation", true);
  useApp.setState({ activeProject: "main", graph: GRAPH as never, preferences: readPreferences() });
});

afterEach(() => {
  vi.restoreAllMocks();
});

const stage = (name: string) => screen.getByRole("region", { name });
const places = () => screen.getByRole("navigation", { name: "Places" });
const roster = (name = "Who is here") => screen.getByRole("group", { name });
/** The line in the dialogue box, as a reader hears it. */
const said = (container: HTMLElement) => container.querySelector(".px-story-line .visually-hidden")?.textContent;

describe("the war room with a theme pack on", () => {
  it("is a story: a stage of the pack's pictures, with the agent's own words under them", () => {
    withPack();
    const { container } = mount(building({ runs: [makeRun({ id: "w1", nodeId: "login" })] }));
    // It opens where the people are.
    expect(within(places()).getByRole("button", { name: /^Work area/ })).toHaveAttribute("aria-pressed", "true");
    const who = within(roster()).getByRole("button", { name: /Claude opus in mac-mini: working, on “Login form”/ });
    expect(who).toHaveAttribute("aria-pressed", "true");
    expect(stage("Work area").querySelector(".px-story-figure .px-figure-art canvas")).not.toBeNull();
    expect(container.querySelector(".px-story-plate")).toHaveTextContent("Claude");
    expect(said(container)).toBe("I'm working on “Login form”.");
    expect(container.querySelectorAll(".px-story-choices button").length).toBeGreaterThan(0);
    // No pixel room under it: no furniture, no pixel person, no wall.
    expect(container.querySelector(".px-cell")).toBeNull();
    expect(container.querySelector(".px-sprite")).toBeNull();
    expect(container.querySelector(".px-wall")).toBeNull();
  });

  it("seats the global agents in the story too, with no project open", () => {
    withPack();
    const run = makeRun({
      id: "g1",
      project: "",
      nodeId: "",
      global: true,
      title: "Bump the deps",
      branch: "agent/bump-the-deps-f0000000",
    });
    const { container } = mount(building({ activeProject: "", globalRuns: [run] }));
    expect(container.querySelector(".px-story-project")).toHaveTextContent("Global agents");
    expect(within(places()).getByRole("button", { name: /^Work area/ })).toHaveAttribute("aria-pressed", "true");
    expect(within(roster()).getByRole("button", { name: /Claude opus/ })).toHaveAttribute("aria-pressed", "true");
    expect(stage("Work area").querySelector(".px-story-figure .px-figure-art canvas")).not.toBeNull();
  });

  it("floats her sign where the scene puts it", () => {
    withPack();
    const { container } = mount(building({ runs: [makeRun({ id: "w1", nodeId: "login" })] }));
    const sign = container.querySelector<HTMLImageElement>(".px-figure-sign")!;
    expect(sign.getAttribute("src")).toBe("/api/themes/magic/files/signs/sage.png");
    expect(sign.style.left).toBe("50%");
    expect(sign.style.top).toBe("10%");
    expect(sign.style.width).toBe("20%");
  });

  describe("the name plate", () => {
    const plate = (container: HTMLElement) => container.querySelector<HTMLElement>(".px-story-plate")!;

    it("has her sign before her name, still and unread, and goes on without it when it will not load", async () => {
      withPack();
      const { container } = mount(
        building({
          runs: [
            makeRun({ id: "w1", nodeId: "login" }),
            makeRun({ id: "w2", nodeId: "schema", agent: "codex", model: "gpt-5" }),
          ],
        }),
      );
      await userEvent.click(within(roster()).getByRole("button", { name: /Claude opus in mac-mini: working/ }));
      const sign = plate(container).querySelector("img")!;
      expect(sign).toHaveClass("px-story-plate-sign");
      expect(sign.getAttribute("src")).toBe("/api/themes/magic/files/signs/sage.png");
      expect(sign).toHaveAttribute("alt", "");
      expect(sign).toHaveAttribute("aria-hidden", "true");
      expect(plate(container).firstChild).toBe(sign);
      expect(plate(container)).toHaveTextContent(/^Claude\s*opus$/);

      fireEvent.error(sign);
      expect(plate(container).querySelector("img")).toBeNull();
      expect(plate(container)).toHaveTextContent(/^Claude\s*opus$/);

      // One of the unnamed has no sign to show.
      await userEvent.click(within(roster()).getByRole("button", { name: /Codex gpt-5/ }));
      expect(plate(container).querySelector("img")).toBeNull();
      expect(plate(container)).toHaveTextContent(/^Codex\s*gpt-5$/);
    });

    it("has it for a character with a sign and no place over her head for it", () => {
      const { write, idle } = SAGE.scenes;
      withPack({
        ...MANIFEST,
        characters: { sage: { ...SAGE, scenes: { write: { ...write, sign: undefined }, idle } } },
      });
      const { container } = mount(building({ runs: [makeRun({ id: "w1", nodeId: "login" })] }));
      expect(container.querySelector(".px-figure-sign")).toBeNull();
      expect(plate(container).querySelector("img")?.getAttribute("src")).toBe("/api/themes/magic/files/signs/sage.png");
    });

    it("has none for a character with no sign, nor for the narrator", async () => {
      withPack({
        ...MANIFEST,
        characters: {
          sage: { match: SAGE.match, scenes: { write: whole("sage/write.webp"), idle: whole("sage/idle.webp") } },
        },
      });
      const { container } = mount(building({ runs: [makeRun({ id: "w1", nodeId: "login" })] }));
      expect(container.querySelector(".px-story-figure .px-figure-art")).not.toBeNull();
      expect(plate(container).querySelector("img")).toBeNull();
      expect(plate(container)).toHaveTextContent("Claude");

      await userEvent.click(within(places()).getByRole("button", { name: /^Meeting room/ }));
      expect(plate(container)).toHaveTextContent("Narrator");
      expect(plate(container).querySelector("img")).toBeNull();
    });
  });

  describe("laid out as a list", () => {
    const RUNS = [
      makeRun({ id: "w1", nodeId: "login" }),
      makeRun({ id: "w2", nodeId: "schema", agent: "codex", model: "gpt-5" }),
      makeRun({ id: "w3", nodeId: "docs", status: "failed", finishedAt: ago(1_000) }),
    ];
    const layouts = () => screen.getByRole("group", { name: "Layout" });
    const rows = (container: HTMLElement) => [...container.querySelectorAll<HTMLElement>(".px-story-row")];
    const choices = (box: Element) => [...box.querySelectorAll(".px-story-choices button")].map((b) => b.textContent);

    it("is switched to from the place's header and back, and remembered", async () => {
      withPack();
      const { container, unmount } = mount(building({ runs: RUNS }));
      const stageButton = within(layouts()).getByRole("button", { name: "Stage" });
      const listButton = within(layouts()).getByRole("button", { name: "List" });
      expect(stageButton).toHaveAttribute("aria-pressed", "true");
      expect(listButton).toHaveAttribute("aria-pressed", "false");
      expect(rows(container)).toHaveLength(0);

      // From the keyboard, as any button.
      listButton.focus();
      await userEvent.keyboard("{Enter}");
      expect(within(layouts()).getByRole("button", { name: "List" })).toHaveAttribute("aria-pressed", "true");
      expect(within(layouts()).getByRole("button", { name: "Stage" })).toHaveAttribute("aria-pressed", "false");
      expect(rows(container)).toHaveLength(3);
      expect(container.querySelector(".px-story-figure")).toBeNull();
      expect(stage("Work area")).toHaveAttribute("data-layout", "list");
      expect(useApp.getState().preferences.storyLayout).toBe("list");
      expect(readPreferences().storyLayout).toBe("list");

      // Remembered: the room opens as a list again.
      unmount();
      const again = mount(building({ runs: RUNS }));
      expect(rows(again.container)).toHaveLength(3);

      await userEvent.click(within(layouts()).getByRole("button", { name: "Stage" }));
      expect(rows(again.container)).toHaveLength(0);
      expect(again.container.querySelector(".px-story-figure")).not.toBeNull();
      expect(readPreferences().storyLayout).toBe("stage");
    });

    it("gives each of them a card and a dialogue box with all the bottom box had", async () => {
      withPack();
      const { container } = mount(building({ runs: RUNS }));
      // The stage opens on whoever something went wrong for: what her box offers.
      const lead = container.querySelector(".px-story-talk")!;
      const leadChoices = choices(lead);
      const leadMeta = lead.querySelector(".px-story-meta")!.textContent;
      expect(leadChoices.length).toBeGreaterThan(0);

      await userEvent.click(within(layouts()).getByRole("button", { name: "List" }));
      const all = rows(container);
      expect(all).toHaveLength(3);
      for (const row of all) {
        expect(row.querySelector(".px-story-card .px-story-art")).not.toBeNull();
        expect(row.querySelector(".px-story-talk .px-story-plate")).not.toBeNull();
        expect(row.querySelector(".px-story-talk .px-story-line")).not.toBeNull();
      }
      // A box each and none under them.
      expect(container.querySelectorAll(".px-story-talk")).toHaveLength(3);

      const on = container.querySelector<HTMLElement>(".px-story-row.is-on")!;
      expect(said(on)).toBe("I could not finish “API docs”.");
      expect(on.querySelector(".px-story-meta")!.textContent).toBe(leadMeta);
      expect(choices(on)).toEqual(leadChoices);

      const sage = all.find((row) => said(row) === "I'm working on “Login form”.")!;
      expect(sage.querySelector(".px-story-card .px-figure-art canvas")).not.toBeNull();
      expect(sage.querySelector(".px-story-plate img")?.getAttribute("src")).toBe(
        "/api/themes/magic/files/signs/sage.png",
      );
      expect(sage.querySelector(".px-story-meta")).toHaveTextContent("Status working");
      expect(choices(sage).length).toBeGreaterThan(0);

      const codex = all.find((row) => said(row) === "I'm working on “DB schema”.")!;
      expect(codex.querySelector(".px-story-card .px-story-unnamed")).toHaveTextContent("Unnamed");
      expect(codex.querySelector(".px-story-plate")).toHaveTextContent(/^Codex\s*gpt-5$/);
      expect(codex.querySelector(".px-story-plate img")).toBeNull();

      // A click on a row turns to her, as a click on her card on the stage does.
      await userEvent.click(codex.querySelector(".px-story-card")!);
      expect(codex).toHaveClass("is-on");
      expect(on).not.toHaveClass("is-on");
      expect(within(roster()).getByRole("button", { name: /Codex gpt-5/ })).toHaveAttribute("aria-pressed", "true");
    });

    it("keeps the narrator's box where nobody is", async () => {
      withPack();
      const { container } = mount(building({ runs: RUNS }));
      await userEvent.click(within(layouts()).getByRole("button", { name: "List" }));
      await userEvent.click(within(places()).getByRole("button", { name: /^Meeting room/ }));
      expect(rows(container)).toHaveLength(0);
      expect(container.querySelector(".px-story-plate")).toHaveTextContent("Narrator");
      expect(within(layouts()).getByRole("button", { name: "List" })).toHaveAttribute("aria-pressed", "true");
    });
  });

  describe("with a scene that plays a clip", () => {
    const FILMED = {
      ...MANIFEST,
      characters: {
        sage: {
          ...SAGE,
          scenes: { ...SAGE.scenes, write: whole("sage/write.webp", { video: "sage/write.webm", sign: [50, 12, 20] }) },
        },
      },
    };
    // jsdom plays no media.
    const media = () => ({
      play: vi.spyOn(HTMLMediaElement.prototype, "play").mockResolvedValue(),
      pause: vi.spyOn(HTMLMediaElement.prototype, "pause").mockImplementation(() => {}),
    });
    const art = () => stage("Work area").querySelector(".px-story-figure .px-figure-art")!;

    afterEach(() => {
      // Unmounted while the media is still stood in for: leaving pauses her.
      cleanup();
      vi.unstubAllGlobals();
    });

    it("plays it, silent and looping, over its still, with her sign over it", () => {
      const { play } = media();
      withPack(FILMED);
      mount(building({ runs: [makeRun({ id: "w1", nodeId: "login" })] }));
      const video = art().querySelector("video")!;
      expect(video.getAttribute("src")).toBe("/api/themes/magic/files/sage/write.webm");
      expect(video.getAttribute("poster")).toBe("/api/themes/magic/files/sage/write.webp");
      expect(video.muted).toBe(true);
      expect(video).toHaveAttribute("loop");
      expect(video).toHaveAttribute("playsinline");
      expect(video.style.width).toBe("100%");
      expect(art().querySelector("canvas")).toBeNull();
      expect(art().querySelector(".px-figure-sign")).not.toBeNull();
      // With nothing to say where the screen is, she counts as on it.
      expect(play).toHaveBeenCalled();
      expect(video.preload).toBe("auto");
    });

    it("leaves it at its poster under reduced motion, loading nothing", () => {
      const { play } = media();
      vi.stubGlobal("matchMedia", (query: string) => ({
        matches: query.includes("reduce"),
        addEventListener: () => {},
        removeEventListener: () => {},
      }));
      withPack(FILMED);
      mount(building({ runs: [makeRun({ id: "w1", nodeId: "login" })] }));
      const video = art().querySelector("video")!;
      expect(video.getAttribute("poster")).toBe("/api/themes/magic/files/sage/write.webp");
      expect(video.preload).toBe("none");
      expect(play).not.toHaveBeenCalled();
    });

    it("plays a clip in the list only while its row is in sight", () => {
      const played: string[] = [];
      vi.spyOn(HTMLMediaElement.prototype, "play").mockImplementation(function (this: HTMLMediaElement) {
        played.push(this.getAttribute("src") ?? "");
        return Promise.resolve();
      });
      vi.spyOn(HTMLMediaElement.prototype, "pause").mockImplementation(() => {});
      // Says, when told, whether what it watches is in sight.
      const watched: { target: Element; report: (inSight: boolean) => void }[] = [];
      vi.stubGlobal(
        "IntersectionObserver",
        class {
          constructor(private readonly callback: IntersectionObserverCallback) {}
          observe(target: Element) {
            watched.push({
              target,
              report: (inSight) =>
                this.callback(
                  [{ target, isIntersecting: inSight } as IntersectionObserverEntry],
                  this as unknown as IntersectionObserver,
                ),
            });
          }
          unobserve() {}
          disconnect() {}
          takeRecords() {
            return [];
          }
        },
      );
      const sage = FILMED.characters.sage;
      withPack({
        ...FILMED,
        characters: {
          sage: { ...sage, scenes: { ...sage.scenes, fail: whole("sage/fail.webp", { video: "sage/fail.webm" }) } },
        },
      });
      useApp.getState().updateUIPreference("storyLayout", "list");
      const { container } = mount(
        building({
          runs: [
            makeRun({ id: "w1", nodeId: "login" }),
            makeRun({ id: "w3", nodeId: "docs", status: "failed", finishedAt: ago(1_000) }),
          ],
        }),
      );
      const [first, second] = container.querySelectorAll(".px-story-row");
      const clipOf = (row: Element) => row.querySelector("video")!.getAttribute("src");
      expect(clipOf(first)).not.toBe(clipOf(second));
      expect(watched.filter(({ target }) => target.closest(".px-story-row"))).toHaveLength(2);
      expect(played).toEqual([]);

      // The first row is in the list's sight; the second is scrolled far out of it.
      for (const { target, report } of watched) report(first.contains(target));
      expect(played).toEqual([clipOf(first)]);
    });

    it("draws the still on the canvas when the clip will not play", () => {
      media();
      withPack(FILMED);
      mount(building({ runs: [makeRun({ id: "w1", nodeId: "login" })] }));
      fireEvent.error(art().querySelector("video")!);
      expect(art().querySelector("video")).toBeNull();
      expect(art().querySelector("canvas")).not.toBeNull();
      expect(art().querySelector(".px-figure-sign")).not.toBeNull();
    });
  });

  describe("with a scene that plays a clip on a click", () => {
    const LOOP = "/api/themes/magic/files/sage/write.webm";
    const CLICK = "/api/themes/magic/files/sage/write-click.webm";
    const clicked = (more: object = {}) => ({
      ...MANIFEST,
      characters: {
        sage: {
          ...SAGE,
          scenes: {
            ...SAGE.scenes,
            write: whole("sage/write.webp", { clickVideo: "sage/write-click.webm", sign: [50, 12, 20], ...more }),
          },
        },
      },
    });
    /** What each video was told, by its src. jsdom plays no media. */
    const media = () => {
      const played: string[] = [];
      const paused: string[] = [];
      vi.spyOn(HTMLMediaElement.prototype, "play").mockImplementation(function (this: HTMLMediaElement) {
        played.push(this.getAttribute("src") ?? "");
        return Promise.resolve();
      });
      vi.spyOn(HTMLMediaElement.prototype, "pause").mockImplementation(function (this: HTMLMediaElement) {
        paused.push(this.getAttribute("src") ?? "");
      });
      return { played, paused };
    };
    const art = () => stage("Work area").querySelector(".px-story-figure .px-figure-art")!;
    const reaction = (inside: ParentNode = art()) => inside.querySelector<HTMLVideoElement>("video.px-figure-click")!;
    const loop = () => art().querySelector<HTMLVideoElement>(`video[src="${LOOP}"]`)!;

    afterEach(() => {
      cleanup();
      vi.unstubAllGlobals();
    });

    it("plays it once over the loop, silent, then the loop again from its start", () => {
      const { played, paused } = media();
      withPack(clicked({ video: "sage/write.webm" }));
      mount(building({ runs: [makeRun({ id: "w1", nodeId: "login" })] }));
      const video = reaction();
      expect(video.getAttribute("src")).toBe(CLICK);
      expect(video.muted).toBe(true);
      expect(video).not.toHaveAttribute("loop");
      expect(video).toHaveAttribute("playsinline");
      expect(video.style.width).toBe("100%");
      expect(video).not.toHaveClass("is-playing");
      // Near the screen, so loaded ahead; the loop plays.
      expect(video.preload).toBe("auto");
      expect(played).toEqual([LOOP]);

      // A click on her card, wherever it lands, as the strip's click frames.
      fireEvent.click(art());
      expect(played).toEqual([LOOP, CLICK]);
      expect(video.currentTime).toBe(0);
      // Shown once it plays, and the loop waits under it.
      fireEvent.playing(video);
      expect(video).toHaveClass("is-playing");
      expect(paused).toContain(LOOP);

      loop().currentTime = 1.5;
      fireEvent.ended(video);
      expect(video).not.toHaveClass("is-playing");
      expect(loop().currentTime).toBe(0);
      expect(played).toEqual([LOOP, CLICK, LOOP]);
    });

    it("starts it again on a second click while it plays", () => {
      const { played } = media();
      withPack(clicked({ video: "sage/write.webm" }));
      mount(building({ runs: [makeRun({ id: "w1", nodeId: "login" })] }));
      fireEvent.click(art());
      fireEvent.playing(reaction());
      reaction().currentTime = 2;
      fireEvent.click(art());
      expect(reaction().currentTime).toBe(0);
      expect(played.filter((src) => src === CLICK)).toHaveLength(2);
      expect(reaction()).toHaveClass("is-playing");
    });

    it("plays it over the still where the scene has no loop", () => {
      const { played } = media();
      withPack(clicked());
      mount(building({ runs: [makeRun({ id: "w1", nodeId: "login" })] }));
      expect(art().querySelector("canvas")).not.toBeNull();
      expect(art().querySelectorAll("video")).toHaveLength(1);
      fireEvent.click(art());
      expect(played).toEqual([CLICK]);
      fireEvent.playing(reaction());
      expect(reaction()).toHaveClass("is-playing");
      fireEvent.ended(reaction());
      expect(reaction()).not.toHaveClass("is-playing");
      expect(art().querySelector("canvas")).not.toBeNull();
    });

    it("plays nothing on a click under reduced motion, and loads nothing", () => {
      const { played } = media();
      vi.stubGlobal("matchMedia", (query: string) => ({
        matches: query.includes("reduce"),
        addEventListener: () => {},
        removeEventListener: () => {},
      }));
      withPack(clicked({ video: "sage/write.webm" }));
      mount(building({ runs: [makeRun({ id: "w1", nodeId: "login" })] }));
      fireEvent.click(art());
      expect(played).toEqual([]);
      expect(reaction().preload).toBe("none");
      expect(reaction()).not.toHaveClass("is-playing");
    });

    it("goes back to the loop, saying nothing, when it will not play", () => {
      const { played } = media();
      withPack(clicked({ video: "sage/write.webm" }));
      mount(building({ runs: [makeRun({ id: "w1", nodeId: "login" })] }));
      fireEvent.click(art());
      fireEvent.playing(reaction());
      fireEvent.error(reaction());
      expect(reaction()).toBeNull();
      expect(loop()).not.toBeNull();
      expect(played.at(-1)).toBe(LOOP);
      fireEvent.click(art());
      expect(played.filter((src) => src === CLICK)).toHaveLength(1);
      // Her picture is not lost with it.
      expect(art().querySelector(".px-figure-sign")).not.toBeNull();
    });

    it("plays it from a row of the list, and loads it only for a row in sight", () => {
      const { played } = media();
      const watched: { target: Element; report: (inSight: boolean) => void }[] = [];
      vi.stubGlobal(
        "IntersectionObserver",
        class {
          constructor(private readonly callback: IntersectionObserverCallback) {}
          observe(target: Element) {
            watched.push({
              target,
              report: (inSight) =>
                this.callback(
                  [{ target, isIntersecting: inSight } as IntersectionObserverEntry],
                  this as unknown as IntersectionObserver,
                ),
            });
          }
          unobserve() {}
          disconnect() {}
          takeRecords() {
            return [];
          }
        },
      );
      const manifest = clicked();
      const sage = manifest.characters.sage;
      withPack({
        ...manifest,
        characters: {
          sage: {
            ...sage,
            scenes: { ...sage.scenes, fail: whole("sage/fail.webp", { clickVideo: "sage/fail-click.webm" }) },
          },
        },
      });
      useApp.getState().updateUIPreference("storyLayout", "list");
      const { container } = mount(
        building({
          runs: [
            makeRun({ id: "w1", nodeId: "login" }),
            makeRun({ id: "w3", nodeId: "docs", status: "failed", finishedAt: ago(1_000) }),
          ],
        }),
      );
      const [first, second] = container.querySelectorAll(".px-story-row");
      expect(reaction(first).preload).toBe("none");
      expect(reaction(second).preload).toBe("none");
      for (const { target, report } of watched) report(first.contains(target));
      expect(reaction(first).preload).toBe("auto");
      expect(reaction(second).preload).toBe("none");

      fireEvent.click(first.querySelector(".px-story-card .px-figure-art")!);
      expect(played).toEqual([reaction(first).getAttribute("src")]);
      fireEvent.playing(reaction(first));
      expect(reaction(first)).toHaveClass("is-playing");
    });
  });

  it("stands agents that wear the same picture as one, with a count and a tab each", async () => {
    withPack();
    const { container } = mount(
      building({
        runs: [
          makeRun({ id: "w1", nodeId: "login" }),
          makeRun({ id: "w2", nodeId: "schema" }),
          makeRun({ id: "w3", nodeId: "docs", status: "failed", finishedAt: ago(1_000) }),
        ],
      }),
    );
    // Another state is another picture: two figures for the three of them.
    expect(container.querySelectorAll(".px-story-figure")).toHaveLength(2);
    expect(within(places()).getByRole("button", { name: /^Work area.*1 failed/ })).toBeInTheDocument();
    // The stage turns first to whoever something went wrong for.
    const failed = within(roster()).getByRole("button", { name: /on “API docs”/ });
    expect(failed).toHaveAttribute("aria-pressed", "true");
    expect(failed).toHaveClass("is-fail");
    expect(said(container)).toBe("I could not finish “API docs”.");

    const two = within(roster()).getByRole("button", { name: "Claude opus: 2 agents" });
    expect(two).toHaveTextContent("Claude ×2");
    await userEvent.click(two);
    expect(two).toHaveAttribute("aria-pressed", "true");
    expect(container.querySelector(".px-story-count")).toHaveTextContent("×2");
    expect(said(container)).toBe("I'm working on “Login form”.");
    const tabs = within(screen.getByRole("group", { name: "Claude opus: 2 agents" }));
    expect(tabs.getByRole("button", { name: /on “Login form”/ })).toHaveAttribute("aria-pressed", "true");
    await userEvent.click(tabs.getByRole("button", { name: /on “DB schema”/ }));
    expect(said(container)).toBe("I'm working on “DB schema”.");
  });

  it("turns the stage with the arrow keys", async () => {
    withPack();
    const { container } = mount(
      building({
        runs: [
          makeRun({ id: "w1", nodeId: "login" }),
          makeRun({ id: "w3", nodeId: "docs", status: "failed", finishedAt: ago(1_000) }),
        ],
      }),
    );
    const [first, second] = within(roster()).getAllByRole("button");
    const lit = () => container.querySelector<HTMLElement>(".px-story-who.is-on")!;
    const from = lit();
    from.focus();
    await userEvent.keyboard(from === first ? "{ArrowRight}" : "{ArrowLeft}");
    expect(lit()).toBe(from === first ? second : first);
  });

  it("stands an agent the pack has nobody for as the unnamed", () => {
    withPack();
    const { container } = mount(
      building({ runs: [makeRun({ id: "w1", nodeId: "login", agent: "codex", model: "gpt-5" })] }),
    );
    expect(within(roster()).getByRole("button", { name: /Codex gpt-5/ })).toBeInTheDocument();
    expect(container.querySelector(".px-story-figure .px-story-unnamed")).toHaveTextContent("Unnamed");
    expect(container.querySelector(".px-story-plate")).toHaveTextContent("Codex");
    expect(container.querySelector(".px-sprite")).toBeNull();
    expect(container.querySelector(".px-desk")).toBeNull();
  });

  it("has a run asleep on a usage limit away, not in the lounge", () => {
    withPack();
    mount(building({ runs: [makeRun({ id: "c1", nodeId: "docs", status: "queued", retryAt: ago(-60_000) })] }));
    expect(within(places()).getByRole("button", { name: /^Away on a usage limit/ })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    expect(within(places()).getByRole("button", { name: /^Lounge/ })).toHaveTextContent("Nobody");
    expect(within(roster()).getByRole("button", { name: /asleep until its usage limit resets/ })).toBeInTheDocument();
  });

  it("goes to another place by its button or its digit, and the narrator speaks where nobody is", async () => {
    withPack();
    const { container } = mount(building({ runs: [makeRun({ id: "w1", nodeId: "login" })] }));
    await userEvent.click(within(places()).getByRole("button", { name: /^Meeting room/ }));
    expect(stage("Meeting room")).toBeInTheDocument();
    expect(container.querySelector(".px-story-plate")).toHaveTextContent("Narrator");
    expect(said(container)).toBe("Nobody is in Meeting room.");
    expect(container.querySelector(".px-story-figure")).toBeNull();

    await userEvent.keyboard("6");
    const runners = stage("Server room");
    expect(within(runners).getByRole("button", { name: /Runner mac-mini/ })).toHaveClass("px-story-rack");
    expect(runners.querySelectorAll(".px-story-slots > span")).toHaveLength(6);
    expect(within(runners).getByRole("button", { name: "Manage runners" })).toHaveClass("px-story-choice");

    await userEvent.keyboard("1");
    expect(said(container)).toBe("I'm working on “Login form”.");
  });

  it("calls the places what the pack calls them", () => {
    withPack({ ...MANIFEST, places: { work: { en: "Study" }, runners: { en: "Crystal tower" } } });
    mount(building({ runs: [makeRun({ id: "w1", nodeId: "login" })] }));
    expect(within(places()).getByRole("button", { name: /^Study/ })).toHaveAttribute("aria-pressed", "true");
    expect(within(places()).getByRole("button", { name: /^Crystal tower/ })).toBeInTheDocument();
    // Its own name stays under the pack's, for whoever knows the room by it.
    expect(within(stage("Study")).getByRole("heading", { name: /Study.*Work area/ })).toBeInTheDocument();
    expect(within(places()).getByRole("button", { name: /^Meeting room/ })).toBeInTheDocument();
  });

  it("speaks in the pack's words, each character in her own voice", async () => {
    withPack({
      ...MANIFEST,
      words: {
        narrator: { en: "Chronicler" },
        people: { en: "In this hall" },
        task: { en: "the errand at hand" },
        status: { en: "Bearing" },
        empty: { en: "{place} lies empty." },
      },
      lines: { working: { en: "Someone toils over {node}." }, idle: { en: "Awaiting {node}." } },
      characters: {
        sage: {
          ...SAGE,
          title: { en: "The Sage" },
          lines: { working: { en: "I am transcribing {node}." } },
        },
      },
    });
    const { container } = mount(
      building({
        runs: [
          makeRun({ id: "w1", nodeId: "login" }),
          makeRun({ id: "w2", nodeId: "schema", agent: "codex", model: "gpt-5" }),
        ],
      }),
    );
    // Her title leads the plate, who she is follows; her own line wins.
    const plate = container.querySelector(".px-story-plate")!;
    expect(plate).toHaveTextContent("The Sage");
    expect(plate.querySelector("small")).toHaveTextContent("Claude opus");
    expect(said(container)).toBe("I am transcribing “Login form”.");
    expect(container.querySelector(".px-story-meta")).toHaveTextContent("Bearing working");

    // One of the unnamed says what anyone of the pack says.
    await userEvent.click(within(roster("In this hall")).getByRole("button", { name: /Codex gpt-5/ }));
    expect(said(container)).toBe("Someone toils over “DB schema”.");

    await userEvent.click(within(places()).getByRole("button", { name: /^Meeting room/ }));
    expect(container.querySelector(".px-story-plate")).toHaveTextContent("Chronicler");
    expect(said(container)).toBe("Meeting room lies empty.");
  });

  it("draws the pixel room when no pack is on", () => {
    const { container } = mount(building({ runs: [makeRun({ id: "w1", nodeId: "login" })] }));
    expect(screen.getByRole("button", { name: /Claude opus/ })).toHaveClass("px-desk");
    expect(container.querySelector(".px-wall")).not.toBeNull();
    expect(container.querySelector(".px-story")).toBeNull();
  });
});

describe("the story's choices", () => {
  const items = (count: number, onSelect = vi.fn()) =>
    Array.from({ length: count }, (_, i) => ({ id: `a${i}`, label: `Do ${i}`, onSelect: () => onSelect(i) }));

  it("are a button each while they are few", async () => {
    const onSelect = vi.fn();
    render(<StoryChoices items={items(4, onSelect)} />);
    expect(screen.getAllByRole("button")).toHaveLength(4);
    await userEvent.click(screen.getByRole("button", { name: "Do 3" }));
    expect(onSelect).toHaveBeenCalledWith(3);
  });

  it("keep the rest in a menu when they are many", async () => {
    const onSelect = vi.fn();
    render(<StoryChoices items={items(6, onSelect)} />);
    expect(screen.getAllByRole("button").map((button) => button.textContent)).toEqual(["Do 0", "Do 1", "Do 2", "More…"]);
    await userEvent.click(screen.getByRole("button", { name: "More…" }));
    const menu = screen.getByRole("menu", { name: "More…" });
    expect(within(menu).getAllByRole("menuitem")).toHaveLength(3);
    await userEvent.click(within(menu).getByRole("menuitem", { name: "Do 5" }));
    expect(onSelect).toHaveBeenCalledWith(5);
  });

  it("are nothing when there is nothing to do", () => {
    const { container } = render(<StoryChoices items={[]} />);
    expect(container).toBeEmptyDOMElement();
  });
});
