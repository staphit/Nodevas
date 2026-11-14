import { describe, expect, it } from "vitest";
import {
  isSafeThemePath,
  isThemeHexColor,
  isThemePack,
  parseThemeManifest,
  THEME_COLORS,
  THEME_LINE_STATES,
  THEME_PLACES,
  THEME_WORDS,
} from "./themeManifest";

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

  it("keeps a fallback character, and adds nothing when there is none", () => {
    const parsed = parseThemeManifest(broken((raw) => (raw.characters.claude.fallback = true)));
    expect(parsed?.characters.claude.fallback).toBe(true);
    expect(parseThemeManifest(manifest())?.characters.claude).not.toHaveProperty("fallback");
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

  it("takes a whole picture: a scene in no room, in a pack with none", () => {
    const raw = broken((r) => {
      delete r.rooms;
      delete r.characters.claude.scenes.read.room;
    });
    const parsed = parseThemeManifest(raw);
    expect(parsed?.rooms).toEqual({});
    expect(parsed?.characters.claude.scenes.read).toEqual({ ...scene(), room: undefined });
    expect(parsed?.characters.claude.scenes.read).not.toHaveProperty("room");
  });

  it("takes a strip of frames: which one blinks, which answer a click", () => {
    const frames = { count: 4, blink: 1, click: [2, 3] };
    const parsed = parseThemeManifest(broken((r) => (r.characters.claude.scenes.read.frames = frames)));
    expect(parsed?.characters.claude.scenes.read.frames).toEqual(frames);
    const bare = parseThemeManifest(broken((r) => (r.characters.claude.scenes.read.frames = { count: 2 })));
    expect(bare?.characters.claude.scenes.read.frames).toEqual({ count: 2, click: [] });
    expect(parseThemeManifest(manifest())?.characters.claude.scenes.read).not.toHaveProperty("frames");
  });

  it("takes a clip to play in place of a scene's still, as .mp4 or .webm", () => {
    for (const video of ["clips/claude/read.mp4", "clips/claude/read.webm"]) {
      const parsed = parseThemeManifest(broken((r) => (r.characters.claude.scenes.read.video = video)));
      expect(parsed?.characters.claude.scenes.read).toEqual({ ...scene(), video });
    }
    expect(parseThemeManifest(manifest())?.characters.claude.scenes.read).not.toHaveProperty("video");
    // The server reads an empty one as none.
    for (const video of ["", null]) {
      const none = parseThemeManifest(broken((r) => (r.characters.claude.scenes.read.video = video)));
      expect(none?.characters.claude.scenes.read).not.toHaveProperty("video");
    }
  });

  it("takes a clip to play once on a click, with or without a loop", () => {
    for (const clickVideo of ["clips/claude/read-click.mp4", "clips/claude/read-click.webm"]) {
      const parsed = parseThemeManifest(broken((r) => (r.characters.claude.scenes.read.clickVideo = clickVideo)));
      expect(parsed?.characters.claude.scenes.read).toEqual({ ...scene(), clickVideo });
    }
    const both = parseThemeManifest(
      broken((r) => {
        r.characters.claude.scenes.read.video = "clips/claude/read.mp4";
        r.characters.claude.scenes.read.clickVideo = "clips/claude/read-click.mp4";
      }),
    );
    expect(both?.characters.claude.scenes.read).toEqual({
      ...scene(),
      video: "clips/claude/read.mp4",
      clickVideo: "clips/claude/read-click.mp4",
    });
    expect(parseThemeManifest(manifest())?.characters.claude.scenes.read).not.toHaveProperty("clickVideo");
    for (const clickVideo of ["", null]) {
      const none = parseThemeManifest(broken((r) => (r.characters.claude.scenes.read.clickVideo = clickVideo)));
      expect(none?.characters.claude.scenes.read).not.toHaveProperty("clickVideo");
    }
  });

  it("takes a character's sign, and where a scene hangs it", () => {
    const parsed = parseThemeManifest(
      broken((r) => {
        r.characters.claude.sign = "signs/claude.png";
        r.characters.claude.scenes.read.sign = [250, 180.5, 96];
      }),
    );
    expect(parsed?.characters.claude.sign).toBe("signs/claude.png");
    expect(parsed?.characters.claude.scenes.read.sign).toEqual([250, 180.5, 96]);
    expect(parseThemeManifest(manifest())?.characters.claude).not.toHaveProperty("sign");
  });

  it("takes a skin: a scheme, a font, and the colours it names", () => {
    const skin = {
      scheme: "dark",
      font: "serif",
      colors: { bg: "#0d0e1c", "surface-2": "#ABC", "accent-text": "#15142B80" },
    };
    expect(parseThemeManifest(broken((r) => (r.skin = skin)))?.skin).toEqual(skin);
    const all = Object.fromEntries(THEME_COLORS.map((color) => [color, "#123456"]));
    expect(parseThemeManifest(broken((r) => (r.skin = { scheme: "light", colors: all })))?.skin?.colors).toEqual(all);
  });

  it("takes a skin of a scheme alone, with no font and no colours of its own", () => {
    const parsed = parseThemeManifest(broken((r) => (r.skin = { scheme: "light" })));
    expect(parsed?.skin).toEqual({ scheme: "light", colors: {} });
    expect(parsed?.skin).not.toHaveProperty("font");
    // The server reads no font as an empty one.
    const empty = parseThemeManifest(broken((r) => (r.skin = { scheme: "light", font: "", colors: null })));
    expect(empty?.skin).toEqual({ scheme: "light", colors: {} });
    expect(empty?.skin).not.toHaveProperty("font");
  });

  it("keeps only what the contract names in a skin", () => {
    const parsed = parseThemeManifest(broken((r) => (r.skin = { scheme: "dark", css: "body{}" })));
    expect(parsed?.skin).toEqual({ scheme: "dark", colors: {} });
  });

  it("takes names for places: any of the six, in one language or both", () => {
    const places = {
      work: { en: "Study", "zh-TW": "書房" },
      runners: { en: "Stable" },
      away: { en: "x".repeat(40), "zh-TW": "" },
    };
    expect(parseThemeManifest(broken((r) => (r.places = places)))?.places).toEqual(places);
    const all = Object.fromEntries(THEME_PLACES.map((place) => [place, { en: place }]));
    expect(parseThemeManifest(broken((r) => (r.places = all)))?.places).toEqual(all);
    expect(parseThemeManifest(broken((r) => (r.places = {})))?.places).toEqual({});
  });

  it("counts a place name in characters, and keeps only its two languages", () => {
    // Forty of them, each two UTF-16 units.
    const long = "\u{20BB7}".repeat(40);
    const parsed = parseThemeManifest(
      broken((r) => (r.places = { lounge: { en: "Hall", "zh-TW": long, ja: "Hiroma" } })),
    );
    expect(parsed?.places).toEqual({ lounge: { en: "Hall", "zh-TW": long } });
  });

  it("takes the story's words and lines, and a character's title and her own lines", () => {
    const words = { narrator: { en: "Chronicler", "zh-TW": "史官" }, count: { en: "{count} here" } };
    const lines = { working: { en: "Toiling over {node}.", "zh-TW": "正埋首於{node}。" }, idle: { en: "At rest." } };
    const own = { failed: { en: "x".repeat(200) } };
    const parsed = parseThemeManifest(
      broken((r) => {
        Object.assign(r, { words, lines });
        Object.assign(r.characters.claude, { title: { en: "The Sage", "zh-TW": "賢者" }, lines: own });
      }),
    );
    expect(parsed?.words).toEqual(words);
    expect(parsed?.lines).toEqual(lines);
    expect(parsed?.characters.claude.title).toEqual({ en: "The Sage", "zh-TW": "賢者" });
    expect(parsed?.characters.claude.lines).toEqual(own);
    const all = Object.fromEntries(THEME_WORDS.map((word) => [word, { en: "x".repeat(120) }]));
    expect(parseThemeManifest(broken((r) => (r.words = all)))?.words).toEqual(all);
    const each = Object.fromEntries(THEME_LINE_STATES.map((state) => [state, { en: state }]));
    expect(parseThemeManifest(broken((r) => (r.lines = each)))?.lines).toEqual(each);
  });

  it("adds no words, lines or title to a pack that has none", () => {
    const parsed = parseThemeManifest(manifest());
    expect(parsed).not.toHaveProperty("words");
    expect(parsed).not.toHaveProperty("lines");
    expect(parsed?.characters.claude).not.toHaveProperty("title");
    expect(parsed?.characters.claude).not.toHaveProperty("lines");
    const nulls = parseThemeManifest(
      broken((r) => {
        Object.assign(r, { words: null, lines: null });
        Object.assign(r.characters.claude, { title: null, lines: null });
      }),
    );
    expect(nulls).toBeDefined();
    expect(nulls).not.toHaveProperty("words");
    expect(nulls?.characters.claude).not.toHaveProperty("title");
  });

  it("adds no skin and no places to a pack that has neither", () => {
    const parsed = parseThemeManifest(manifest());
    expect(parsed).not.toHaveProperty("skin");
    expect(parsed).not.toHaveProperty("places");
    const nulls = parseThemeManifest(broken((r) => Object.assign(r, { skin: null, places: null })));
    expect(nulls).toBeDefined();
    expect(nulls).not.toHaveProperty("skin");
    expect(nulls).not.toHaveProperty("places");
  });

  it.each<[string, unknown]>([
    ["a skin that is no object", broken((raw) => (raw.skin = "dark"))],
    ["a skin that is a list", broken((raw) => (raw.skin = []))],
    ["a skin with no scheme", broken((raw) => (raw.skin = { colors: { bg: "#000" } }))],
    ["a skin of another scheme", broken((raw) => (raw.skin = { scheme: "sepia" }))],
    ["a skin of another font", broken((raw) => (raw.skin = { scheme: "dark", font: "Comic Sans MS" }))],
    ["a skin font that is no string", broken((raw) => (raw.skin = { scheme: "dark", font: 1 }))],
    ["skin colours as a list", broken((raw) => (raw.skin = { scheme: "dark", colors: ["#000"] }))],
    ["a colour the app has no token for", broken((raw) => (raw.skin = { scheme: "dark", colors: { shadow: "#000" } }))],
    ["a colour written as its token", broken((raw) => (raw.skin = { scheme: "dark", colors: { "--bg": "#000" } }))],
    ["an inherited colour name", broken((raw) => (raw.skin = { scheme: "dark", colors: { constructor: "#000" } }))],
    ["a colour by name", broken((raw) => (raw.skin = { scheme: "dark", colors: { bg: "black" } }))],
    ["a colour by function", broken((raw) => (raw.skin = { scheme: "dark", colors: { bg: "rgb(0, 0, 0)" } }))],
    ["a colour that is a url", broken((raw) => (raw.skin = { scheme: "dark", colors: { bg: "url(//x.test/a)" } }))],
    ["a colour of a variable", broken((raw) => (raw.skin = { scheme: "dark", colors: { bg: "var(--text)" } }))],
    ["a colour that goes on", broken((raw) => (raw.skin = { scheme: "dark", colors: { bg: "#000;color:red" } }))],
    ["a colour and a new line", broken((raw) => (raw.skin = { scheme: "dark", colors: { bg: "#000\n" } }))],
    ["a colour with a space", broken((raw) => (raw.skin = { scheme: "dark", colors: { bg: " #000" } }))],
    ["a colour of four digits", broken((raw) => (raw.skin = { scheme: "dark", colors: { bg: "#0000" } }))],
    ["a colour of five digits", broken((raw) => (raw.skin = { scheme: "dark", colors: { bg: "#00000" } }))],
    ["a colour of no hex digits", broken((raw) => (raw.skin = { scheme: "dark", colors: { bg: "#ggg" } }))],
    ["a colour with no hash", broken((raw) => (raw.skin = { scheme: "dark", colors: { bg: "000000" } }))],
    ["a colour that is a number", broken((raw) => (raw.skin = { scheme: "dark", colors: { bg: 0 } }))],
    ["a colour that is null", broken((raw) => (raw.skin = { scheme: "dark", colors: { bg: null } }))],
    ["places that are no object", broken((raw) => (raw.places = "Study"))],
    ["places as a list", broken((raw) => (raw.places = [{ en: "Study" }]))],
    ["a place the war room has not", broken((raw) => (raw.places = { attic: { en: "Attic" } }))],
    ["an inherited place name", broken((raw) => (raw.places = { constructor: { en: "Attic" } }))],
    ["a place that is a string", broken((raw) => (raw.places = { work: "Study" }))],
    ["a place with no English name", broken((raw) => (raw.places = { work: { "zh-TW": "書房" } }))],
    ["a place with an empty English name", broken((raw) => (raw.places = { work: { en: "" } }))],
    ["a place name that is no string", broken((raw) => (raw.places = { work: { en: "Study", "zh-TW": 1 } }))],
    ["a place name of 41 characters", broken((raw) => (raw.places = { work: { en: "x".repeat(41) } }))],
    [
      "a second place name of 41 characters",
      broken((raw) => (raw.places = { work: { en: "Study", "zh-TW": "書".repeat(41) } })),
    ],
    ["a place name with a new line", broken((raw) => (raw.places = { work: { en: "Stu\ndy" } }))],
    ["a place name with a null in it", broken((raw) => (raw.places = { work: { en: "Stu\u0000dy" } }))],
    [
      "a second place name with an escape in it",
      broken((raw) => (raw.places = { work: { en: "Study", "zh-TW": "書\u001b房" } })),
    ],
    ["a place name with a C1 control", broken((raw) => (raw.places = { work: { en: "Stu\u0085dy" } }))],
    ["words that are no object", broken((raw) => (raw.words = "Chronicler"))],
    ["a word the story has not", broken((raw) => (raw.words = { greeting: { en: "Hail" } }))],
    ["an inherited word", broken((raw) => (raw.words = { constructor: { en: "Hail" } }))],
    ["a word that is a string", broken((raw) => (raw.words = { narrator: "Chronicler" }))],
    ["a word with no English", broken((raw) => (raw.words = { narrator: { "zh-TW": "史官" } }))],
    ["a word of 121 characters", broken((raw) => (raw.words = { narrator: { en: "x".repeat(121) } }))],
    ["a word with a new line", broken((raw) => (raw.words = { narrator: { en: "Chron\nicler" } }))],
    ["lines as a list", broken((raw) => (raw.lines = [{ en: "Toiling." }]))],
    ["a line for a state there is not", broken((raw) => (raw.lines = { dancing: { en: "Dancing." } }))],
    ["a line with no English", broken((raw) => (raw.lines = { working: { "zh-TW": "忙。" } }))],
    ["a line of 201 characters", broken((raw) => (raw.lines = { working: { en: "x".repeat(201) } }))],
    ["a line with an escape in it", broken((raw) => (raw.lines = { working: { en: "Toil\u001bing." } }))],
    ["a title that is a string", broken((raw) => (raw.characters.claude.title = "The Sage"))],
    ["a title with no English", broken((raw) => (raw.characters.claude.title = { "zh-TW": "賢者" }))],
    ["a title of 41 characters", broken((raw) => (raw.characters.claude.title = { en: "x".repeat(41) }))],
    [
      "a character's line for a state there is not",
      broken((raw) => (raw.characters.claude.lines = { dancing: { en: "Dancing." } })),
    ],
    [
      "a character's line of 201 characters",
      broken((raw) => (raw.characters.claude.lines = { working: { en: "x".repeat(201) } })),
    ],
    ["frames that are no object", broken((raw) => (raw.characters.claude.scenes.read.frames = 5))],
    ["frames with no count", broken((raw) => (raw.characters.claude.scenes.read.frames = { blink: 1 }))],
    ["one frame", broken((raw) => (raw.characters.claude.scenes.read.frames = { count: 1 }))],
    ["too many frames", broken((raw) => (raw.characters.claude.scenes.read.frames = { count: 17 }))],
    ["a blink on the still", broken((raw) => (raw.characters.claude.scenes.read.frames = { count: 3, blink: 0 }))],
    ["a blink past the strip", broken((raw) => (raw.characters.claude.scenes.read.frames = { count: 3, blink: 3 }))],
    ["a click past the strip", broken((raw) => (raw.characters.claude.scenes.read.frames = { count: 3, click: [3] }))],
    ["five clicks", broken((raw) => (raw.characters.claude.scenes.read.frames = { count: 3, click: [1, 2, 1, 2, 1] }))],
    ["clicks that are no list", broken((raw) => (raw.characters.claude.scenes.read.frames = { count: 3, click: 1 }))],
    ["a clip that is a picture", broken((raw) => (raw.characters.claude.scenes.read.video = "clips/read.png"))],
    ["a clip of another type", broken((raw) => (raw.characters.claude.scenes.read.video = "clips/read.mov"))],
    ["a clip going up", broken((raw) => (raw.characters.claude.scenes.read.video = "../read.mp4"))],
    ["a clip that is no string", broken((raw) => (raw.characters.claude.scenes.read.video = ["clips/read.mp4"]))],
    [
      "a clip and frames",
      broken((raw) => {
        raw.characters.claude.scenes.read.video = "clips/read.mp4";
        raw.characters.claude.scenes.read.frames = { count: 2 };
      }),
    ],
    ["a click clip that is a picture", broken((raw) => (raw.characters.claude.scenes.read.clickVideo = "clips/read.png"))],
    ["a click clip of another type", broken((raw) => (raw.characters.claude.scenes.read.clickVideo = "clips/read.mov"))],
    ["a click clip going up", broken((raw) => (raw.characters.claude.scenes.read.clickVideo = "../read.mp4"))],
    [
      "a click clip that is no string",
      broken((raw) => (raw.characters.claude.scenes.read.clickVideo = ["clips/read.mp4"])),
    ],
    [
      "a click clip and frames",
      broken((raw) => {
        raw.characters.claude.scenes.read.clickVideo = "clips/read.mp4";
        raw.characters.claude.scenes.read.frames = { count: 2 };
      }),
    ],
    ["a clip as the image", broken((raw) => (raw.characters.claude.scenes.read.image = "clips/read.mp4"))],
    ["a clip as a room", broken((raw) => (raw.rooms.studio_claude = "rooms/studio.webm"))],
    ["a clip as a sign", broken((raw) => (raw.characters.claude.sign = "signs/claude.mp4"))],
    ["a sign of another type", broken((raw) => (raw.characters.claude.sign = "signs/claude.svg"))],
    ["a sign going up", broken((raw) => (raw.characters.claude.sign = "../claude.png"))],
    [
      "a scene's sign with no sign to hang",
      broken((raw) => (raw.characters.claude.scenes.read.sign = [250, 180, 96])),
    ],
    [
      "a scene's sign of two numbers",
      broken((raw) => {
        raw.characters.claude.sign = "signs/claude.png";
        raw.characters.claude.scenes.read.sign = [250, 180];
      }),
    ],
    [
      "a scene's sign off the canvas",
      broken((raw) => {
        raw.characters.claude.sign = "signs/claude.png";
        raw.characters.claude.scenes.read.sign = [1537, 180, 96];
      }),
    ],
    [
      "a scene's sign of no width",
      broken((raw) => {
        raw.characters.claude.sign = "signs/claude.png";
        raw.characters.claude.scenes.read.sign = [250, 180, 0];
      }),
    ],
    ["a room that is no string", broken((raw) => (raw.characters.claude.scenes.read.room = 1))],
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
    ["fallback that is no boolean", broken((raw) => (raw.characters.claude.fallback = "yes"))],
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

describe("a colour in a skin", () => {
  it.each(["#abc", "#ABC", "#0d0e1c", "#0D0E1C", "#0d0e1c80"])("may be %s", (value) => {
    expect(isThemeHexColor(value)).toBe(true);
  });

  it.each(["", "#", "#ab", "#abcd", "#abcde", "#abcdefa", "#abcdefabc", "abc", "red", "#abc ", "#abc\n", "#abc;", 0, null])(
    "may not be %j",
    (value) => {
      expect(isThemeHexColor(value)).toBe(false);
    },
  );
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
