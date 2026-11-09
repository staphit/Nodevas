# Theme packs

A theme pack replaces Nodevas's built-in pixel people with your own art. It is a zip of images and one JSON file: no code runs, and importing needs no restart and no startup config.

> **Current state:** you can import, list and choose a pack, but the war room does not draw packs yet, so choosing one changes nothing on screen. The zip format below is checked on import. The scene names and how characters are matched are not final until the war room reads them.

## Make a pack from your own images

Any tool can make the images: a local model, an online generator, or a drawing program.

### 1. Draw the rooms

Draw each room at the same size, with nobody in it. That size is your `canvas`, for example 1536×864. A later scene can then put several people in one room.

### 2. Draw the people

Make one image per person per state: someone reading, someone asleep, and so on. Each image needs a transparent background, in `.png` or `.webp`, and should hold only the person. People look most at home in a room when you do this:

1. Generate the person alone on a plain background, full body, then cut them out.
2. Paste them into the empty room at the spot and size you want.
3. Repaint only that area at about 45% strength, so they take on the room's light.
4. Keep what changed, with a soft edge, as the transparent image. Note where it sits in the room: that is its `x`, `y`, `w` and `h`.

Choose spots so that people do not cover each other, and give each person their own pose. Otherwise a shared room looks like copies of one figure.

### 3. Lay out the folder

```text
my-pack/
  theme.json
  rooms/
    lounge.webp
  figures/
    claude/
      read.webp
```

Folder names are up to you. The only fixed name is `theme.json`, which must sit at the top of the zip. Every other file must be named in `theme.json`, so remove `Thumbs.db`, `.DS_Store` and anything else left behind: an extra file refuses the whole zip.

### 4. Write theme.json

```json
{
  "schema": 1,
  "name": {"en": "My pack", "zh-TW": "我的主題"},
  "version": "1.0.0",
  "author": "your name",
  "canvas": [1536, 864],
  "rooms": {
    "lounge": "rooms/lounge.webp"
  },
  "characters": {
    "claude": {
      "match": {"agents": ["claude"], "models": ["claude*"]},
      "scenes": {
        "read": {
          "room": "lounge",
          "image": "figures/claude/read.webp",
          "x": 217, "y": 147, "w": 408, "h": 699,
          "box": [243, 170, 598, 810]
        }
      }
    }
  }
}
```

| Field | Meaning | Rule |
| --- | --- | --- |
| `schema` | Format version | Exactly `1` |
| `name` | Name shown in Settings. `en` also identifies the pack: importing the same name again replaces your pack | `en` is required and uses English letters and digits, with at most 40 of them. `zh-TW` is optional. At most 64 characters each. Not `none` or `default` |
| `version` | Your version | 1 to 64 characters |
| `author` | Who made it | At most 64 characters |
| `canvas` | Width and height of every room | Two integers from 1 to 8192 |
| `rooms` | Room id → image | Ids below |
| `characters` | Character id → its `match` and `scenes` | Ids below |
| `match.agents` | Which agents this character stands for: `claude`, `codex` (GPT), `gemini`, `opencode`, `antigravity` | At most 16 |
| `match.models` | Which models it stands for; `*` is intended as a wildcard | At most 16, at most 64 characters each |
| `fallback` | Optional. `true` makes this character stand in for agents no other character matches | At most one in a pack |
| `scenes` | Scene name → one image of this character | Ids below |
| `room` | Which room the scene happens in | Must be in `rooms` |
| `image` | The person's transparent image | Must be in the zip |
| `x`, `y`, `w`, `h` | The rectangle the image fills, in canvas pixels. Keep `w` and `h` the image's own size | Integers, inside the canvas |
| `box` | Left, top, right, bottom of the person inside the canvas: the area meant for clicking | Inside the canvas, left < right, top < bottom |

Room ids, character ids and scene names use lowercase letters, digits, `_` and `-`, at most 64 characters. Punctuation and spaces in `name.en` are ignored when comparing names, so `Magic world` and `magic-world` are the same pack. If someone else already imported that name, change yours. A pack can have at most 256 rooms, 64 characters, and 256 scenes per character. An unknown field anywhere refuses the zip, which catches typos.

Scene names you can use now, which may change before the war room reads them:

| Where | Names |
| --- | --- |
| Working | `read`, `search`, `think`, `write`, `cast` |
| Needs you, or finished | `wait`, `ask`, `done`, `fail` |
| Resting | `idle`, `sleep` |
| Meeting | `listen`, `speak` |
| Outings | `hike`, `stream`, `fish`, `camp`, `bbq`, `ride` |

### 5. Zip it

Zip the folder's contents, not the folder, or `theme.json` will not be at the top. From inside `my-pack`:

```shell
python -m zipfile -c ../my-pack.zip theme.json rooms figures
```

Avoid PowerShell's `Compress-Archive`: older versions write backslashes into paths, and a backslash refuses the zip.

### 6. Import it

Open **Settings → General → Theme packs**, choose **Import a zip**, then pick your pack under **Your theme**. If the zip is refused, the message says what to fix, and any pack with that name keeps working.

Other people on the same server can see your pack in their list and choose it, so it is not private. It shows for them only if they choose it, or if an admin makes it the site default. You can delete your own pack. An admin can delete any pack.

## Install several packs and switch

Import each pack under its own name in `name.en`. They all stay installed, up to 5 per person, and you switch under **Settings → General → Theme packs → Your theme**:

| Your theme | What you see |
| --- | --- |
| Site default | The pack an admin chose for the site, or the built-in people if none |
| Built-in pixel people | No pack |
| A pack's name | That pack |

Your choice is kept in this browser, so another browser or device has its own. It changes only what you see. Importing a pack does not select it.

## Replace one character

A pack does not need every character. To replace only GPT, give one character the agent and models GPT runs under. The character's id is yours to choose, because `match` is what connects it. GPT's agent is `codex`:

```text
"characters": {
  "my-gpt": {
    "match": {"agents": ["codex"], "models": ["gpt*", "o1*", "o3*", "o4*"]},
    "scenes": { ... }
  }
}
```

To change one character in an existing pack, replace its images, update `x`, `y`, `w`, `h` and `box` if the size or place changed, raise `version`, zip it again and import it. The same `name.en` replaces the old pack. Changing `name.en` makes a new pack instead: the old one stays until you delete it.

## How a pack connects to agents

These are the rules the war room will follow. It does not read packs yet, so none of them can be seen working today.

| Situation | What it will show |
| --- | --- |
| Which character is this agent? | First a character whose `match.models` fits the running model, then one whose `match.agents` fits the agent. The model comes first because it says who is really working: opencode running a GPT model shows GPT |
| No character matches | The pack's `fallback` character; with none, the built-in pixel person |
| The character has no image for this state | Its `idle` scene; with no `idle`, the built-in pixel person |
| Several people in one room | Each at their own `x` and `y`. Nothing moves them apart, so keep your spots from overlapping |

## Limits

| Limit | Value |
| --- | --- |
| Image types | `.webp`, `.png`, `.jpg`, `.jpeg`, with a lowercase extension. No SVG |
| The zip | 200 MiB |
| One image | 16 MiB |
| `theme.json` | 4 MiB |
| Files in a pack | 2000 |
| All files, unpacked | 500 MiB |
| Packs per person | 5 |
| All packs on a server | 2 GiB |
| Paths | Forward slashes, relative, at most 200 bytes. No `..`, no `\ : * ? " < > |`, and no name ending in a dot or space |

Each image must really be the type its extension says. Keep images small with `.webp`: a pack of 12 rooms and 76 people can fit in about 6 MB.

## Rights

Only import images you have the right to use. That includes the licence of the model that made them, and any logo or character that belongs to someone else. Everyone on the server can read a pack you import.
