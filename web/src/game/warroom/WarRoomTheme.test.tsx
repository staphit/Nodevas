import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { api, type Run, type RunEvent, type ThemePack } from "../../api";
import { readPreferences, writePreference } from "../../preferences";
import { EditionProvider } from "../../runners/edition";
import { useCoordStore } from "../../runs/coord/coordStore";
import { useRuns } from "../../runs/runStore";
import type { ControlRow } from "../../station/control";
import { useApp } from "../../store";
import { parseThemeManifest } from "../../theme-pack/themeManifest";
import { useThemePacks } from "../../theme-pack/themePackStore";
import { buildBuilding, type BuildingInputs } from "../building";
import { GameActionsProvider } from "../GameActions";
import { ago, makeBudget, makeMessage, makePipeline, makeRun, makeRunner, makeSession, NOW } from "../testFixtures";
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

function mount(world: Building, rowOf: (runId: string) => ControlRow | undefined = () => undefined) {
  return render(
    <GameActionsProvider>
      <WarRoomStage building={world} titleOf={titleOf} rowOf={rowOf} />
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

describe("story room dialog, pictures, group view and coordination (W5)", () => {
  function seedRuns(runs: Run[], events: Record<string, RunEvent[]> = {}) {
    useRuns.getState().reset();
    useRuns.setState({ globalById: {}, threadsByRoot: {}, watched: {}, eventsByRun: {} });
    useCoordStore.getState().reset();
    for (const run of runs) useRuns.getState().putRun(run);
    for (const [id, list] of Object.entries(events)) {
      useRuns.getState().appendEvents(id, list);
    }
    const byId = new Map(runs.map((run) => [run.id, { run } as ControlRow]));
    return (runId: string) => byId.get(runId);
  }

  beforeEach(() => {
    vi.spyOn(api, "getRunEvents").mockResolvedValue({ events: [], more: false });
    vi.spyOn(api, "getRunAsks").mockResolvedValue({ asks: [] });
    vi.spyOn(api, "getGlobalRunThread").mockResolvedValue({ runs: [] });
    vi.spyOn(api, "getRunOverlaps").mockResolvedValue({ overlaps: [] });
  });

  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
    useRuns.getState().reset();
    useRuns.setState({ globalById: {}, threadsByRoot: {}, watched: {}, eventsByRun: {} });
    useCoordStore.getState().reset();
  });

  it("renders RunDialog variant='story' for a run actor with canned line in header and real output in body", () => {
    withPack();
    const run = makeRun({ id: "r-out", nodeId: "login", status: "running" });
    const rowOf = seedRuns([run], {
      "r-out": [
        { seq: 1, type: "text", at: ago(1_000), text: "Updating the login handler now." },
        {
          seq: 2,
          type: "tool_call",
          at: ago(500),
          tool: "edit",
          toolId: "t1",
          input: JSON.stringify({ file_path: "src/login.ts" }),
        },
        { seq: 3, type: "tool_result", at: ago(200), toolId: "t1", text: "ok" },
      ],
    });
    const session = makeSession({ id: "s-out", runId: "r-out", nodeId: "login", state: "busy" });
    const msg = makeMessage({ id: "m-out", senderSession: session.name, body: "Agent mail bubble text" });

    const { container } = mount(building({ runs: [run], sessions: [session], messages: [msg] }), rowOf);

    const talk = container.querySelector(".px-story-talk")!;
    expect(talk).toHaveClass("has-run");
    const dlg = talk.querySelector(".run-dialog.run-dialog-story")!;
    expect(dlg).not.toBeNull();
    // Canned flavour line stays in header (and agent mail bubble does not override it for a run actor).
    const headerLine = dlg.querySelector(".run-dlg-head .px-story-line .visually-hidden");
    expect(headerLine).toHaveTextContent("I'm working on “Login form”.");
    expect(headerLine).not.toHaveTextContent("Agent mail bubble text");
    // Body shows real transcript output, not the canned line, and no typewriter span.
    const body = dlg.querySelector(".run-dlg-body")!;
    expect(body).toHaveTextContent("Updating the login handler now.");
    expect(body).toHaveTextContent("Edit src/login.ts");
    expect(body.querySelector(".px-story-untyped")).toBeNull();
  });

  it("shows the canned line in the body empty state when a run has no events yet", () => {
    withPack();
    const run = makeRun({ id: "r-empty", nodeId: "login", status: "queued" });
    const rowOf = seedRuns([run]);

    const { container } = mount(building({ runs: [run] }), rowOf);
    const dlg = container.querySelector(".run-dialog.run-dialog-story")!;
    expect(dlg.querySelector(".run-dlg-head .px-story-line .visually-hidden")).toHaveTextContent(
      "I'm waiting to be let in for “Login form”.",
    );
    const empty = dlg.querySelector(".run-dlg-empty")!;
    expect(empty).toHaveTextContent("I'm waiting to be let in for “Login form”.");
    expect(empty).toHaveTextContent("Nothing said yet");
  });

  it("keeps the flavour line in header and puts a failed run error in the body as alert", () => {
    withPack();
    const run = makeRun({
      id: "r-fail",
      nodeId: "docs",
      status: "failed",
      error: "Build failed with exit code 2",
      finishedAt: ago(1_000),
    });
    const rowOf = seedRuns([run]);

    const { container } = mount(building({ runs: [run] }), rowOf);
    const dlg = container.querySelector(".run-dialog.run-dialog-story")!;
    expect(dlg.querySelector(".run-dlg-head .px-story-line .visually-hidden")?.textContent).toBe(
      "I could not finish “API docs”.",
    );
    expect(within(dlg as HTMLElement).getByRole("alert")).toHaveTextContent("Build failed with exit code 2");
  });

  it("honours cast.still on the header flavour line and never typewrites transcript lines", () => {
    vi.stubGlobal("matchMedia", (query: string) => ({
      matches: query.includes("reduce"),
      addEventListener: () => {},
      removeEventListener: () => {},
    }));
    withPack();
    const run = makeRun({ id: "r-still", nodeId: "login", status: "running" });
    const rowOf = seedRuns([run], {
      "r-still": [{ seq: 1, type: "text", at: ago(500), text: "Immediate output" }],
    });

    const { container } = mount(building({ runs: [run] }), rowOf);
    // Under reduced motion (cast.still === true), the untyped span is empty immediately.
    const untyped = container.querySelector(".run-dlg-head .px-story-line .px-story-untyped");
    expect(untyped?.textContent).toBe("");
    // Transcript never has typewriter spans.
    expect(container.querySelector(".run-dlg-log-list .px-story-untyped")).toBeNull();
  });

  it("does not hijack arrow or digit keys while typing in the dialog textarea", async () => {
    withPack();
    const g1 = makeRun({
      id: "g-type-1",
      project: "",
      nodeId: "",
      global: true,
      title: "Upgrade deps",
      status: "running",
    });
    const g2 = makeRun({
      id: "g-type-2",
      project: "",
      nodeId: "",
      global: true,
      agent: "codex",
      model: "gpt-5",
      title: "Fix CI",
      status: "running",
    });
    const rowOf = seedRuns([g1, g2]);

    const { container } = render(
      <EditionProvider edition="single">
        <GameActionsProvider>
          <WarRoomStage building={building({ activeProject: "", globalRuns: [g1, g2] })} titleOf={titleOf} rowOf={rowOf} />
        </GameActionsProvider>
      </EditionProvider>,
    );
    const textarea = container.querySelector<HTMLTextAreaElement>(".run-dlg-textarea")!;
    expect(textarea).not.toBeNull();
    expect(textarea).toBeEnabled();

    textarea.focus();
    await userEvent.keyboard("123{ArrowRight}{ArrowLeft}6");
    // Place must still be Work area (digit 6 did not jump to Server room).
    expect(within(places()).getByRole("button", { name: /^Work area/ })).toHaveAttribute("aria-pressed", "true");
    // Focused figure must still be the first group (ArrowRight did not turn the stage).
    expect(within(roster()).getByRole("button", { name: /Claude opus/ })).toHaveAttribute("aria-pressed", "true");
    // ArrowLeft moved the caret left one character inside the textarea before "6" was typed.
    expect(textarea.value).toBe("1263");
  });

  it("uses compact sizing inside the stage and opens full size via Open large", async () => {
    withPack();
    const run = makeRun({ id: "r-expand", nodeId: "login", status: "running" });
    const rowOf = seedRuns([run], {
      "r-expand": [{ seq: 1, type: "text", at: ago(500), text: "Compact output" }],
    });

    const { container } = mount(building({ runs: [run] }), rowOf);
    const inlineDialog = container.querySelector(".px-story-talk.has-run .run-dialog")!;
    expect(inlineDialog).not.toBeNull();
    expect(inlineDialog).not.toHaveClass("is-expanded");

    await userEvent.click(within(inlineDialog as HTMLElement).getByRole("button", { name: "Open large" }));
    const expandedModal = document.body.querySelector(".run-dialog-portal-theme .run-dialog.is-expanded");
    expect(expandedModal).not.toBeNull();
    expect(expandedModal).toHaveTextContent("Compact output");
  });

  it("subscribes at most 6 in-view boxes in list layout and shows static last text for the rest", () => {
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

    // 8 distinct agents so list layout has 8 rows (each its own group).
    withPack();
    const runs = Array.from({ length: 8 }, (_, i) =>
      makeRun({
        id: `list-run-${i + 1}`,
        project: "",
        nodeId: "",
        global: true,
        agent: `agent${i + 1}`,
        model: `m${i + 1}`,
        title: `Task ${i + 1}`,
        status: "running",
      }),
    );
    const events: Record<string, RunEvent[]> = {};
    for (const run of runs) {
      events[run.id] = [{ seq: 1, type: "text", at: ago(500), text: `Output of ${run.title}` }];
    }
    const rowOf = seedRuns(runs, events);
    const watchSpy = vi.spyOn(useRuns.getState(), "watchRun");

    useApp.getState().updateUIPreference("storyLayout", "list");
    const { container } = mount(building({ activeProject: "", globalRuns: runs }), rowOf);

    const listRows = [...container.querySelectorAll(".px-story-row")];
    expect(listRows).toHaveLength(8);
    // Before anything intersects, nothing is subscribed.
    expect(watchSpy).not.toHaveBeenCalled();

    // Report all 8 rows intersecting at once: at most 6 should subscribe.
    act(() => {
      for (const entry of watched) {
        if (entry.target.classList.contains("px-story-row")) entry.report(true);
      }
    });

    const subscribedCount = Object.keys(useRuns.getState().watched).length;
    expect(subscribedCount).toBeLessThanOrEqual(6);
    expect(subscribedCount).toBe(6);

    // Unsubscribed rows (the remaining 2) still display the flavour line + last loaded text event.
    const previews = container.querySelectorAll(".px-story-preview");
    expect(previews).toHaveLength(2);
    expect(previews[0].textContent).toContain("Output of Task");
  });

  it("turns pack facts into pictures while keeping visually-hidden text and using the fixed runner icon", () => {
    withPack();
    const verifyRun = makeRun({
      id: "r-facts-verify",
      nodeId: "login",
      kind: "verify",
      pipelineId: "p1",
      status: "succeeded",
      finishedAt: ago(60 * 60 * 1000),
      verify: { phase: "green", passed: true, exitCode: 0, durationMs: 1200, noCommand: false },
    });
    const run = makeRun({
      id: "r-facts",
      nodeId: "login",
      role: "tester",
      pipelineId: "p1",
      status: "running",
      usage: { inputTokens: 3000, outputTokens: 2000 },
      overlapCount: 2,
    });
    const session = makeSession({ id: "s-facts", runId: "r-facts", nodeId: "login", state: "busy" });
    const held = [makeMessage({ id: "h1", to: session.name, status: "held" })];
    const rowOf = seedRuns([run]);

    const { container } = mount(
      building({
        runs: [verifyRun, run],
        sessions: [session],
        held,
        pipelines: { p1: makePipeline({ id: "p1", nodeId: "login" }) },
        budget: makeBudget(5000, 10000),
      }),
      rowOf,
    );

    const meta = container.querySelector(".px-story-meta")!;
    // Status is visually hidden (text still accessible).
    const statusLi = meta.querySelector("li.is-status")!;
    expect(statusLi).toHaveClass("visually-hidden");
    expect(statusLi).toHaveTextContent("Status working");

    // Role and node stay visible text.
    const roleLi = meta.querySelector("li.is-role")!;
    expect(roleLi).not.toHaveClass("visually-hidden");
    expect(roleLi).toHaveTextContent("Tester");
    const nodeLi = meta.querySelector("li.is-node")!;
    expect(nodeLi).not.toHaveClass("visually-hidden");
    expect(nodeLi).toHaveTextContent("Login form");

    // Runner uses the fixed runner icon with tooltip, no pack image, visually-hidden text kept.
    const runnerLi = meta.querySelector("li.is-runner")!;
    expect(runnerLi.querySelector(".picker-runner-badge")).toHaveAttribute("title", "mac-mini");
    expect(runnerLi.querySelector("svg")).not.toBeNull();
    expect(runnerLi.querySelector("img")).toBeNull();
    expect(runnerLi.querySelector(".visually-hidden")).toHaveTextContent("Runner mac-mini");

    // Tokens uses EnergyHud-style meter + visually-hidden text.
    const tokensLi = meta.querySelector("li.is-tokens")!;
    expect(within(tokensLi as HTMLElement).getByRole("meter")).toBeInTheDocument();
    expect(tokensLi.querySelector(".visually-hidden")).toHaveTextContent(/Tokens/);

    // Held mail shows mail icon + count + visually-hidden text.
    const heldLi = meta.querySelector("li.is-held")!;
    expect(heldLi.querySelector("svg")).not.toBeNull();
    expect(heldLi).toHaveTextContent("1");
    expect(heldLi.querySelector(".visually-hidden")).toHaveTextContent("Held messages 1");

    // Tests shows a test lamp + visually-hidden text.
    const testsLi = meta.querySelector("li.is-tests")!;
    expect(testsLi.querySelector(".px-story-test-lamp.is-green")).not.toBeNull();
    expect(testsLi.querySelector(".visually-hidden")).toHaveTextContent(/Tests/);

    // Overlap shows a small same-files mark with tooltip + visually-hidden text.
    const overlapLi = meta.querySelector("li.is-overlap")!;
    expect(overlapLi.querySelector(".px-story-fact-overlap")).toHaveAttribute("title", "2 overlap");
    expect(overlapLi.querySelector(".visually-hidden")).toHaveTextContent("2 overlap");
  });

  it("stands group members as one figure (group:<id>) with repo tabs, pose faces, and status pips without merging unrelated runs", async () => {
    withPack();
    const gWorking = makeRun({
      id: "grp-a",
      project: "",
      nodeId: "",
      global: true,
      groupId: "grp1",
      repoId: "repo-b",
      repoName: "billing-svc",
      title: "Upgrade Go",
      status: "running",
    });
    const gFailed = makeRun({
      id: "grp-b",
      project: "",
      nodeId: "",
      global: true,
      groupId: "grp1",
      repoId: "repo-a",
      repoName: "auth-svc",
      title: "Upgrade Go",
      status: "failed",
      error: "test failed",
      finishedAt: ago(1_000),
    });
    const gQueued = makeRun({
      id: "grp-c",
      project: "",
      nodeId: "",
      global: true,
      groupId: "grp1",
      repoId: "repo-c",
      repoName: "web-app",
      title: "Upgrade Go",
      status: "queued",
    });
    // Unrelated global run in the same scene (Claude opus working).
    const unrelated = makeRun({
      id: "other-1",
      project: "",
      nodeId: "",
      global: true,
      repoId: "repo-x",
      repoName: "solo-repo",
      title: "Unrelated fix",
      status: "running",
    });
    const rowOf = seedRuns([gWorking, gFailed, gQueued, unrelated], {
      "grp-a": [{ seq: 1, type: "text", at: ago(500), text: "Working on billing-svc" }],
      "grp-b": [{ seq: 1, type: "text", at: ago(500), text: "Failed on auth-svc" }],
    });

    const { container } = mount(
      building({ activeProject: "", globalRuns: [gWorking, gFailed, gQueued, unrelated] }),
      rowOf,
    );

    // Two figures in Work area: one for group:grp1 (3 members across running/failed/queued), one for unrelated.
    expect(container.querySelectorAll(".px-story-figure")).toHaveLength(2);
    // Nothing split off into Entrance!
    expect(within(places()).getByRole("button", { name: /^Entrance/ })).toHaveTextContent("Nobody");

    // Group is focused first (because auth-svc failed) and shows ×3.
    expect(container.querySelector(".px-story-figure:not(.is-side) .px-story-count")).toHaveTextContent("×3");

    // Pip strip shows 3 status pips in repo-name order: auth-svc (failed), billing-svc (working), web-app (awaiting-accept).
    const pips = [...container.querySelectorAll(".px-story-pips .px-story-pip")];
    expect(pips).toHaveLength(3);
    expect(pips[0]).toHaveClass("is-failed");
    expect(pips[1]).toHaveClass("is-working");
    expect(pips[2]).toHaveClass("is-awaiting-accept");

    // Member tabs ordered by repo name, each showing repo name + pose face.
    const tabs = [...container.querySelectorAll(".px-story-members .px-story-member")];
    expect(tabs).toHaveLength(3);
    expect(tabs[0]).toHaveTextContent("auth-svc");
    expect(tabs[0].querySelector(".px-story-face")).not.toBeNull();
    expect(tabs[1]).toHaveTextContent("billing-svc");
    expect(tabs[1].querySelector(".px-story-face")).not.toBeNull();
    expect(tabs[2]).toHaveTextContent("web-app");
    expect(tabs[2].querySelector(".px-story-face")).not.toBeNull();

    // Initially focused member is auth-svc (failed); clicking billing-svc switches RunDialog.
    expect(container.querySelector(".run-dlg-body")).toHaveTextContent("Failed on auth-svc");
    await userEvent.click(tabs[1]);
    expect(tabs[1]).toHaveAttribute("aria-pressed", "true");
    expect(container.querySelector(".run-dlg-body")).toHaveTextContent("Working on billing-svc");
  });

  it("renders OverlapNotice in the RunDialog header and pre-fills the composer on Tell it", async () => {
    withPack();
    const run = makeRun({
      id: "r-ov",
      project: "",
      nodeId: "",
      global: true,
      title: "Refactor auth",
      status: "running",
      overlapCount: 1,
    });
    const rowOf = seedRuns([run]);
    useCoordStore.setState({
      overlapsByRun: {
        "r-ov": [
          {
            runId: "r-other",
            title: "Login tweak",
            mine: true,
            hidden: false,
            paths: ["src/auth.ts"],
            pathCount: 1,
            strongCount: 1,
            since: ago(1_000),
          },
        ],
      },
    });

    const { container } = mount(building({ activeProject: "", globalRuns: [run] }), rowOf);
    const head = container.querySelector(".run-dlg-head")!;
    expect(head.querySelector(".coord-overlap")).not.toBeNull();
    expect(head).toHaveTextContent("Also being changed by another run");

    await userEvent.click(within(head as HTMLElement).getByRole("button", { name: "Tell it" }));
    const textarea = container.querySelector<HTMLTextAreaElement>(".run-dlg-textarea")!;
    expect(textarea.value).toContain("src/auth.ts");
  });

  it("stands contest entries together as a pair with per-entry figures and tabs", async () => {
    withPack();
    const entryA = makeRun({
      id: "ct-a",
      nodeId: "login",
      contestId: "contest-1",
      contestLabel: "A",
      agent: "claude",
      model: "opus",
      status: "running",
    });
    const entryB = makeRun({
      id: "ct-b",
      nodeId: "login",
      contestId: "contest-1",
      contestLabel: "B",
      agent: "codex",
      model: "gpt-5",
      status: "queued",
    });
    const rowOf = seedRuns([entryA, entryB], {
      "ct-a": [{ seq: 1, type: "text", at: ago(500), text: "Entry A approach" }],
      "ct-b": [{ seq: 1, type: "text", at: ago(500), text: "Entry B approach" }],
    });

    const { container } = mount(building({ runs: [entryA, entryB] }), rowOf);
    // Both entries stand together as ONE contest figure pair in Work area.
    expect(container.querySelectorAll(".px-story-figure")).toHaveLength(1);
    const pair = container.querySelector(".px-story-pair")!;
    expect(pair).not.toBeNull();
    expect(pair.querySelectorAll(".px-story-pair-entry")).toHaveLength(2);
    expect(pair).toHaveTextContent("A");
    expect(pair).toHaveTextContent("B");

    // Member tabs switch between Entry A and Entry B.
    const tabs = [...container.querySelectorAll(".px-story-members .px-story-member")];
    expect(tabs).toHaveLength(2);
    expect(tabs[0]).toHaveTextContent("Entry A");
    expect(tabs[1]).toHaveTextContent("Entry B");
    expect(container.querySelector(".run-dlg-body")).toHaveTextContent("Entry A approach");

    await userEvent.click(tabs[1]);
    expect(container.querySelector(".run-dlg-body")).toHaveTextContent("Entry B approach");

    // The pair is where a node contest is decided: Compare and pick opens it.
    const getContest = vi.spyOn(api, "getContest").mockResolvedValue({
      contest: {
        id: "contest-1",
        title: "Login",
        global: false,
        state: "running",
        entryCount: 2,
        createdBy: "u1",
        createdAt: "2026-11-20T08:00:00Z",
      },
      runs: [entryA, entryB],
      compare: { entries: [], scores: null },
    });
    await userEvent.click(within(container.querySelector(".px-story-contest") as HTMLElement).getByRole("button", { name: "Compare and pick" }));
    expect(await screen.findByRole("dialog")).toBeInTheDocument();
    await waitFor(() => expect(getContest).toHaveBeenCalledWith("contest-1"));
  });
});
