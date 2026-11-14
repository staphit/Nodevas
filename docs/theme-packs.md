# Theme packs

A theme pack replaces Nodevas's built-in pixel people with your own art. It is a zip of images, perhaps some short clips, and one JSON file: no code runs, and importing needs no restart and no startup config.

> **Current state:** with a pack chosen, the Agent activity view is a story. A rail lists six places to go to: `work`, `meeting`, `entrance`, `lounge`, `away` and `runners`. A stage shows the pack's pictures of whoever is in the chosen place, one picture per character and state; agents sharing a picture stand as one with a ×N count. In a dialogue box, the agent you turn to says what it is doing, and you answer with choices: the same actions the pixel room's menus have. An agent no character matches stands as an "unnamed" silhouette, unless the pack has a `fallback` character. Without a pack, the pixel room is drawn as before. The zip format below is checked on import.

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

A person can also be one finished picture with her surroundings painted in, and either kind of image can move, or be a clip. Both are in "Whole pictures, movement and signs" below.

### 3. Lay out the folder

```text
my-pack/
  theme.json
  rooms/
    lounge.webp
  figures/
    claude/
      read.webp
  pictures/
    claude/
      idle.webp
      write.webp
      write.webm
      write-click.webm
  signs/
    mage.webp
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
      "sign": "signs/mage.webp",
      "title": {"en": "The Sage", "zh-TW": "賢者"},
      "lines": {
        "idle": {"en": "The hearth is warm.", "zh-TW": "爐火正暖。"}
      },
      "scenes": {
        "read": {
          "room": "lounge",
          "image": "figures/claude/read.webp",
          "x": 217, "y": 147, "w": 408, "h": 699,
          "box": [243, 170, 598, 810]
        },
        "idle": {
          "image": "pictures/claude/idle.webp",
          "x": 0, "y": 0, "w": 1536, "h": 864,
          "box": [520, 180, 1010, 864],
          "frames": {"count": 4, "blink": 1, "click": [2, 3]},
          "sign": [765, 120, 96]
        },
        "write": {
          "image": "pictures/claude/write.webp",
          "video": "pictures/claude/write.webm",
          "clickVideo": "pictures/claude/write-click.webm",
          "x": 0, "y": 0, "w": 1536, "h": 864,
          "box": [520, 180, 1010, 864],
          "sign": [765, 120, 96]
        }
      }
    }
  },
  "skin": {
    "scheme": "dark",
    "font": "serif",
    "colors": {"bg": "#0d0e1c", "surface": "#15142b", "text": "#efe6d0", "accent": "#d9b86c"}
  },
  "places": {
    "work": {"en": "Study", "zh-TW": "書房"},
    "lounge": {"en": "Hearth", "zh-TW": "爐邊"}
  },
  "words": {
    "narrator": {"en": "Chronicler", "zh-TW": "史官"},
    "task": {"en": "a quest", "zh-TW": "一項委託"}
  },
  "lines": {
    "working": {"en": "I am casting on {node}.", "zh-TW": "我正在對 {node} 施法。"}
  }
}
```

| Field | Meaning | Rule |
| --- | --- | --- |
| `schema` | Format version | Exactly `1` |
| `name` | Name shown in Settings. `en` also identifies the pack: importing the same name again replaces your pack | `en` is required and uses English letters and digits, with at most 40 of them. `zh-TW` is optional. At most 64 characters each. Not `none` or `default` |
| `version` | Your version | 1 to 64 characters |
| `author` | Who made it | At most 64 characters |
| `canvas` | Width and height of every room and whole picture | Two integers from 1 to 8192 |
| `rooms` | Room id → image. Optional when no scene names a room | Ids below |
| `characters` | Character id → its `match` and `scenes` | Ids below |
| `match.agents` | Which agents this character stands for: `claude`, `codex` (GPT), `gemini`, `opencode`, `antigravity` | At most 16 |
| `match.models` | Which models it stands for; `*` is intended as a wildcard | At most 16, at most 64 characters each |
| `fallback` | Optional. `true` makes this character stand in for agents no other character matches | At most one in a pack |
| `sign` (character) | Optional. The image of a mark that floats over her head | Must be in the zip |
| `title` | Optional. What the character is called on the dialogue box's name plate | See "Words and lines" below |
| `lines` (character) | Optional. What this character says in the dialogue box | See "Words and lines" below |
| `scenes` | Scene name → one image of this character | Ids below |
| `room` | Which room the scene happens in. Optional: without it, `image` is the whole picture and nothing is drawn under it | When given, must be in `rooms` |
| `image` | The person's transparent image, or the whole picture | Must be in the zip |
| `x`, `y`, `w`, `h` | The rectangle the image fills, in canvas pixels. Keep `w` and `h` the image's own size | Integers, inside the canvas |
| `box` | Left, top, right, bottom of the person inside the canvas: the area meant for clicking | Inside the canvas, left < right, top < bottom |
| `frames` | Optional. Makes `image` a strip of frames that can blink and react to a click | `count` from 2 to 16. `blink` and each of at most 4 `click` frames from 1 to `count` − 1 |
| `video` | Optional. A clip played silent and looping in place of `image`, which stays the still picture | `.mp4` or `.webm`, in the zip. Not with `frames` |
| `clickVideo` | Optional. A clip played once, silent, when she is clicked, before the loop or the still comes back | `.mp4` or `.webm`, in the zip. Not with `frames`. With or without `video` |
| `sign` (scene) | Optional. `[x, y, width]`: the centre of the character's sign over this picture and how wide to draw it, in canvas pixels | Inside the canvas, width above 0. The character must have a `sign` image |
| `skin` | Optional. Colours for the whole app, not only the Agent activity view | See "Skin" below |
| `places` | Optional. Your names for the places of the Agent activity view | See "Place names" below |
| `words` | Optional. Your wording for the story's interface | See "Words and lines" below |
| `lines` | Optional. What agents say in the dialogue box | See "Words and lines" below |

Room ids, character ids and scene names use lowercase letters, digits, `_` and `-`, at most 64 characters. Punctuation and spaces in `name.en` are ignored when comparing names, so `Magic world` and `magic-world` are the same pack. If someone else already imported that name, change yours. A pack can have at most 256 rooms, 64 characters, and 256 scenes per character. An unknown field anywhere refuses the zip, which catches typos.

Scene names, by what the agent is doing:

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
python -m zipfile -c ../my-pack.zip theme.json rooms figures pictures signs
```

Avoid PowerShell's `Compress-Archive`: older versions write backslashes into paths, and a backslash refuses the zip.

### 6. Import it

Open **Settings → General → Theme packs**, choose **Import a zip**, then pick your pack under **Your theme**. If the zip is refused, the message says what to fix, and any pack with that name keeps working.

Other people on the same server can see your pack in their list and choose it, so it is not private. It shows for them only if they choose it, or if an admin makes it the site default. You can delete your own pack. An admin can delete any pack.

## Whole pictures, movement and signs

Rooms with cut-out people are one style. The other is a whole picture: one finished image per state, with the person already painted into her surroundings. A pack can use either, or mix them as the example above does.

### Whole pictures

Leave `room` out of a scene and its `image` is drawn alone, with no room under it. A pack with only whole pictures needs no `rooms` at all. `x`, `y`, `w` and `h` still say where the picture sits on the canvas, which is usually all of it: `0`, `0` and the canvas size. `box` is still where the person is inside it.

### Movement

`frames` turns a scene's `image` into a strip: `count` frames side by side, each `w`×`h` pixels, so the file is `count` × `w` wide and `h` high. In the example, `idle` is a 6144×864 file holding four 1536×864 frames. A figure in a room can have `frames` too.

| Part | Meaning |
| --- | --- |
| Frame 0 | The leftmost frame: the still picture, always drawn |
| Every other frame | Transparent except for what it changes, such as closed eyes or a raised hand. It is drawn over frame 0 |
| `blink` | The frame played now and then on its own |
| `click` | Up to 4 frames played in order when she is clicked |

`blink` and `click` are both optional and count from 1, because frame 0 is the still picture. One frame can serve both. A strip is one image, so the 16 MiB limit covers all its frames. Import does not open the image, so a strip of the wrong width is accepted and then draws wrongly: check that the width is `count` × `w`.

### Clips

`video` names a clip that plays in place of a scene's `image`: silent, looping, and only while the picture is on screen or near it. It fills the scene's `x`, `y`, `w` and `h`, over the room when the scene has one, and is cropped rather than stretched if its shape differs. In the example, `write` is a clip of the whole picture.

`image` is still required: it is the still picture. It shows before the clip starts, while the viewer's system asks for reduced motion or the page is in a background tab, if the clip will not play, and wherever the Agent activity view cuts from the picture, such as her face by the dialogue box. Make it the clip's first frame, so nothing jumps when the clip starts.

A scene with `video` cannot also have `frames`. Use H.264 in `.mp4` or VP9 in `.webm`, so that every browser can play it; any sound track is never heard. A clip is one file, so it must fit in 16 MiB. Import checks only that the file starts as its type does, so a clip that is damaged further in shows the still picture instead.

`clickVideo` names a clip of her reaction to a click: it plays once, silent, from its start, in place of the loop, and then the loop (or the still picture, in a scene without `video`) comes back. A second click while it plays starts it again. It fills the same rectangle as `video`, loads only while the picture is on screen or near it, and plays nothing while the viewer's system asks for reduced motion or the page is in a background tab. Start and end it on the still picture, so nothing jumps either way. In the example, `write` has one. A scene with `clickVideo` cannot also have `frames`, whose own `click` frames answer a click. The same types and 16 MiB limit apply; a click clip that will not play is skipped, and the loop goes on.

### Signs

A `sign` on a character names one more image: a mark that floats over her head, such as a badge of her craft. It is drawn only in the scenes that have a `sign` of their own, which is `[x, y, width]` in canvas pixels: the centre of the mark and how wide to draw it. A scene without `sign` shows no mark. A scene `sign` in a character that has no `sign` image refuses the zip.

## Skin

`skin` colours the whole app while your pack is chosen, not only the Agent activity view. Leave it out and the app keeps its own colours.

| Part | Meaning | Rule |
| --- | --- | --- |
| `scheme` | Which built-in palette your colours start from | Required: `light` or `dark` |
| `font` | The app's typeface | Optional: `sans` or `serif` |
| `colors` | The colours you change. The rest stay the scheme's | Optional. Only the names below, each a hex colour: `#rgb`, `#rrggbb` or `#rrggbbaa` |

| Colour | What it paints |
| --- | --- |
| `bg` | The page behind everything |
| `surface`, `surface-2` | Panels and cards, and the areas set into them |
| `border`, `border-strong` | Lines between things, faint and firm |
| `hover` | What the pointer is over |
| `text`, `text-2` | Text, and quieter text |
| `accent`, `accent-text` | Buttons and highlights, and the text on them |

A colour name outside this list, or a value that is not hex (`red`, `rgb(0,0,0)`, `var(--x)`), refuses the zip.

## Place names

`places` gives the places of the Agent activity view your pack's names. Name any of the six; the rest keep their own.

| Place | What it is |
| --- | --- |
| `work` | Where agents work |
| `meeting` | The meeting room |
| `entrance` | The door, where an offered run waits |
| `lounge` | Where agents rest |
| `away` | Where agents waiting out a usage limit have gone |
| `runners` | The runners: the pixel room's server room |

Each name is written like the pack's `name`: `en` is required, `zh-TW` is optional. At most 40 characters each, with no control characters such as a line break. A place outside this list refuses the zip.

## Words and lines

A pack can word the story its own way, as the example above does. Each text is written like the pack's `name`: `en` is required, `zh-TW` is optional, with no control characters such as a line break.

| Field | Meaning | Rule |
| --- | --- | --- |
| `words` | Your wording for the story's interface, by word | Only the words below. At most 120 characters each |
| `lines` | What an agent says in the dialogue box, by its state | Only the states below. At most 200 characters each |
| `lines` (character) | The same, for the agents this character stands for | As `lines` |
| `title` (character) | What the character is called on the dialogue box's name plate, such as "The Sage". It is shown before the agent's own name | At most 40 characters each |

| Words | What they word |
| --- | --- |
| `narrator`, `unnamed` | The name plate where nobody is, and an agent no character matches |
| `task` | What `{node}` becomes in a line when the agent is on no node |
| `people`, `notices`, `others` | The names of three lists: who is here, what waits for you, and the other offices |
| `count`, `nobody`, `runnerCount`, `failed` | Counts beside a place: how many are there, nobody, how many runners, how many failed. All but `nobody` take `{count}` |
| `empty`, `noticesLine`, `runnersLine` | What the narrator says: nobody is in `{place}`, `{count}` things wait for you, `{count}` runners have `{busy}` runs on them |
| `heldLine` | What an agent says when `{count}` messages to it are held for your approval |
| `more` | The choice that opens the rest of the choices |
| `status`, `role`, `node`, `runner`, `tokens`, `project`, `held`, `tests` | Labels of what is known about the agent you turned to |

The states a line can be for are `offline`, `asking-user`, `failed`, `limit`, `awaiting-accept`, `waiting-ci`, `reviewing`, `working`, `finished` and `idle`.

A character's line wins over the pack's, and the pack's over the app's built-in line. A line may contain `{node}`: it becomes the quoted title of the node the agent is on, or your word `task` (else the app's own) when it is on none. A word or a state outside these lists refuses the zip.

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

These are the rules the Agent activity view follows when it draws a pack.

| Situation | What it shows |
| --- | --- |
| Which character is this agent? | First a character whose `match.models` fits the running model, then one whose `match.agents` fits the agent. The model comes first because it says who is really working: opencode running a GPT model shows GPT |
| No character matches | The pack's `fallback` character; with none, an "unnamed" silhouette |
| The character has no image for this state | Its `idle` scene; with no `idle`, an "unnamed" silhouette |
| Several agents of one character in the same state | They stand as one, with a ×N count |

## Limits

| Limit | Value |
| --- | --- |
| Image types | `.webp`, `.png`, `.jpg`, `.jpeg`, with a lowercase extension. No SVG |
| Clip types | `.mp4`, `.webm`, with a lowercase extension, and only as a scene's `video` or `clickVideo` |
| The zip | 200 MiB |
| One image or clip | 16 MiB |
| `theme.json` | 4 MiB |
| Files in a pack | 2000 |
| All files, unpacked | 500 MiB |
| Packs per person | 5 |
| All packs on a server | 2 GiB |
| Paths | Forward slashes, relative, at most 200 bytes. No `..`, no `\ : * ? " < > |`, and no name ending in a dot or space |

Each image and clip must really be the type its extension says. Keep images small with `.webp`: a pack of 12 rooms and 76 people can fit in about 6 MB.

## Rights

Only import images you have the right to use. That includes the licence of the model that made them, and any logo or character that belongs to someone else. Everyone on the server can read a pack you import.
