import { describe, expect, it } from "vitest";
import { isSafeThemePath, isThemePack, parseThemeManifest } from "./themeManifest";

const scene = () => ({
  room: "studio_claude",
  image: "figures/claude/read.webp",
  x: 100,
  y: 200,
  w: 300,
  h: 500,
  box: [120, 210, 380, 700],
});

const manifest = () => ({
  schema: 1,
  id: "magic",
  name: { en: "Magic world", "zh-TW": "魔法世界" },
  version: "1.0.0",
  author: "someone",
  canvas: [1536, 864],
  rooms: { studio_claude: "rooms/studio.webp" },
  characters: {
    claude: {
      match: { agents: ["claude"], models: ["claude*"] },
      scenes: { read: scene() },
    },
  },
});

type Raw = Record<string, any>; // eslint-disable-line @typescript-eslint/no-explicit-any

/** A valid manifest with one thing changed. */
function broken(change: (raw: Raw) => void): unknown {
  const raw = manifest();
  change(raw);
  return raw;
}

describe("a theme manifest", () => {
  it("is taken as it reads when it is whole", () => {
    expect(parseThemeManifest(manifest())).toEqual(manifest());
  });

  it("needs no author, rooms, characters or match to be readable", () => {
    const parsed = parseThemeManifest({
      schema: 1,
      id: "bare",
      name: { en: "Bare" },
      version: "1",
      canvas: [10, 10],
    });
    expect(parsed).toEqual({
      schema: 1,
      id: "bare",
      name: { en: "Bare" },
      version: "1",
      author: "",
      canvas: [10, 10],
      rooms: {},
      characters: {},
    });
    const noMatch = parseThemeManifest(broken((raw) => delete raw.characters.claude.match));
    expect(noMatch?.characters.claude.match).toEqual({ agents: [], models: [] });
  });

  it("keeps only what the contract names", () => {
    const parsed = parseThemeManifest(broken((raw) => (raw.script = "alert(1)"))) as unknown as Raw;
    expect(parsed.script).toBeUndefined();
  });

  it.each<[string, unknown]>([
    ["null", null],
    ["a string", "magic"],
    ["an array", []],
    ["another schema", broken((raw) => (raw.schema = 2))],
    ["a bad id", broken((raw) => (raw.id = "../up"))],
    ["no English name", broken((raw) => (raw.name = { "zh-TW": "魔法" }))],
    ["an empty English name", broken((raw) => (raw.name.en = ""))],
    ["a name that is no string", broken((raw) => (raw.name["zh-TW"] = 1))],
    ["no version", broken((raw) => delete raw.version)],
    ["an author that is no string", broken((raw) => (raw.author = 1))],
    ["a canvas of one number", broken((raw) => (raw.canvas = [10]))],
    ["a canvas of fractions", broken((raw) => (raw.canvas = [10.5, 10]))],
    ["a canvas too large", broken((raw) => (raw.canvas = [8193, 10]))],
    ["a canvas of nothing", broken((raw) => (raw.canvas = [0, 10]))],
    ["rooms as a list", broken((raw) => (raw.rooms = ["a.webp"]))],
    ["a bad room id", broken((raw) => (raw.rooms["Up/Up"] = "a.webp"))],
    ["a room path going up", broken((raw) => (raw.rooms.up = "../a.webp"))],
    ["a room image of another type", broken((raw) => (raw.rooms.up = "a.svg"))],
    ["a bad character id", broken((raw) => (raw.characters.__proto_x = { scenes: {} }))],
    ["a character that is no object", broken((raw) => (raw.characters.claude = 1))],
    ["match that is no object", broken((raw) => (raw.characters.claude.match = "claude"))],
    ["agents of numbers", broken((raw) => (raw.characters.claude.match.agents = [1]))],
    ["a bad scene name", broken((raw) => (raw.characters.claude.scenes["Read Up"] = scene()))],
    ["a scene that is no object", broken((raw) => (raw.characters.claude.scenes.read = 1))],
    ["a scene in a room that is not there", broken((raw) => (raw.characters.claude.scenes.read.room = "attic"))],
    ["a scene in an inherited room name", broken((raw) => (raw.characters.claude.scenes.read.room = "constructor"))],
    ["a scene image going up", broken((raw) => (raw.characters.claude.scenes.read.image = "a/../../b.webp"))],
    ["a fractional x", broken((raw) => (raw.characters.claude.scenes.read.x = 1.5))],
    ["a negative x", broken((raw) => (raw.characters.claude.scenes.read.x = -1))],
    ["a width of nothing", broken((raw) => (raw.characters.claude.scenes.read.w = 0))],
    ["a rectangle past the canvas", broken((raw) => (raw.characters.claude.scenes.read.w = 1500))],
    ["a box of three", broken((raw) => (raw.characters.claude.scenes.read.box = [1, 2, 3]))],
    ["a box inside out", broken((raw) => (raw.characters.claude.scenes.read.box = [300, 10, 200, 20]))],
    ["a box past the canvas", broken((raw) => (raw.characters.claude.scenes.read.box = [0, 0, 1537, 20]))],
    ["a box of strings", broken((raw) => (raw.characters.claude.scenes.read.box = ["1", "2", "3", "4"]))],
    ["a box with NaN", broken((raw) => (raw.characters.claude.scenes.read.box = [NaN, 2, 3, 4]))],
  ])("is refused with %s", (_name, raw) => {
    expect(parseThemeManifest(raw)).toBeUndefined();
  });
});

describe("a path in a pack", () => {
  it.each(["a.webp", "rooms/lounge.webp", "figures/claude/read.png", "魔法/圖 1.jpeg", "a..b.jpg"])("may be %s", (path) => {
    expect(isSafeThemePath(path)).toBe(true);
  });

  it.each([
    "",
    ".",
    "..",
    "../a.webp",
    "a/../b.webp",
    "a/./b.webp",
    "a//b.webp",
    "/a.webp",
    "a/",
    "a\\b.webp",
    "C:/a.webp",
    "c:a.webp",
    "a\u0000.webp",
    "x".repeat(201),
  ])("may not be %j", (path) => {
    expect(isSafeThemePath(path)).toBe(false);
  });

  it("is a string", () => {
    expect(isSafeThemePath(undefined)).toBe(false);
    expect(isSafeThemePath(1)).toBe(false);
  });
});

describe("a pack in the list", () => {
  const pack = { id: "magic", name: { en: "Magic world" }, version: "1", author: "", owner: "ann", sizeBytes: 1 };

  it("has the fields the page indexes into", () => {
    expect(isThemePack(pack)).toBe(true);
    expect(isThemePack(null)).toBe(false);
    expect(isThemePack({ ...pack, id: "../up" })).toBe(false);
    expect(isThemePack({ ...pack, name: null })).toBe(false);
    expect(isThemePack({ ...pack, name: { en: 1 } })).toBe(false);
  });
});
