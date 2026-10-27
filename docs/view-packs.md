# View packs

A view pack restyles Station's live view, the war room or the boss map, with your own pictures. It is data, not code: a `pack.json` and some images in a zip. The agents, rooms, nodes and states are the same as in the built-in views; a pack only decides how they look.

## Install and use

- **Install:** an admin installs a pack in **Settings → Appearance and language → View packs → Install a pack (.zip)**. A pack is installed for the whole workspace, so everyone on the server can pick it. Installing a pack with an ID that is already installed replaces it.
- **Use:** pick a pack from Station's view switch, next to War room and Boss map, or from the command palette.
- **Remove:** an admin removes a pack in the same place. Anyone who was using it falls back to the built-in view of the same layout.

On a standalone server (loopback, no accounts) you are the admin.

## The zip

```
pack.json
images/…          PNG, WebP or SVG files
```

`pack.json` sits at the root of the zip, or inside one top-level folder (as a zipped folder has).

```jsonc
{
  "format": 1,
  "id": "neon-station",                      // [a-z0-9-]{1,40}, unique per workspace
  "name": { "en": "Neon station", "zh-TW": "霓虹太空站" },  // "en" required
  "layout": "rooms",                         // "rooms" (war room) or "path" (boss map)
  "palette": {                               // optional, colours as #rgb or #rrggbb
    "ground": "#0e1726",                     // the view's background
    "text": "#e6eef7"                        // labels drawn on it
  },
  "sprites": {                               // optional; each value is a path in the zip
    "agent.working": "images/astro-typing.svg",
    "agent.asking-user": "images/astro-hand.svg",
    "monster": "images/crate.svg"
  }
}
```

Every sprite is optional. A missing one falls back as listed below, and then to the built-in art. A pack with no sprites at all is the built-in view with its palette.

### Sprites for the `rooms` layout (war room)

| Key | Box (px) | Stands for | Falls back to |
|---|---|---|---|
| `agent.working` | 80 × 96 | an agent at its desk, working | `agent` |
| `agent.reviewing` | 80 × 96 | reviewing someone's PR | `agent` |
| `agent.asking-user` | 80 × 96 | waiting for your answer | `agent` |
| `agent.awaiting-accept` | 80 × 96 | waiting to be accepted at the runner | `agent` |
| `agent.waiting-ci` | 80 × 96 | waiting for CI | `agent` |
| `agent.failed` | 80 × 96 | its run failed | `agent` |
| `agent.idle` | 80 × 96 | connected, doing nothing | `agent` |
| `agent.offline` | 80 × 96 | gone or its runner offline | `agent` |
| `agent` | 80 × 96 | any of the above | built-in desk and figure |
| `agent.standing` | 40 × 64 | in the door queue or at a meeting | built-in |
| `desk.empty` | 80 × 96 | a free desk | built-in |
| `table` | 96 × 56 | a review meeting's table | built-in |

An `agent.*` picture replaces the whole workstation, desk and person together. Its bottom centre stands on the desk's floor spot.

### Sprites for the `path` layout (boss map)

| Key | Box (px) | Stands for | Falls back to |
|---|---|---|---|
| `monster.attackable` | 48 × 48 | a node open to work | `monster` |
| `monster.locked` | 48 × 48 | a node something blocks | `monster` |
| `monster.defeated` | 48 × 48 | a settled node | `chest.open` (a settled node is loot, not a monster) |
| `monster` | 48 × 48 | any node | built-in |
| `boss` | 140 × 130 | the project | built-in |
| `boss.defeated` | 140 × 130 | the project, all done | `boss` |
| `hero` | 30 × 42 | the autopilot party leader | built-in |
| `tile.corridor` | 80 × 44 | a path tile | built-in |
| `tile.arena` | 80 × 47 | a tile around the boss | built-in |
| `gate.closed` / `gate.open` | 51 × 74 | a dependency gate | built-in |
| `chest.closed` / `chest.open` | 27 × 30 | loot beside a monster | built-in |
| `column`, `barrel` | 20 × 59, 25 × 26 | the boss arena's props | built-in |

A picture is scaled to fit its box, keeping its proportions, with its bottom centre on the spot. Draw for about twice the box size for sharp results on high-density screens.

### What Nodevas still draws

Nodevas still draws the badges and cues that do not depend on colour: the raised hand, error marks, locks, check marks, speech bubbles, names and HP bars. A pack cannot make "asking you" look the same as "working". Keyboard order, the list view and reduced motion work the same with any pack.

## Rules a pack must follow

These are checked at install. A pack that breaks one is refused with the reason, and nothing is installed.

- The zip has at most 200 files and 20 MB unpacked.
- Each image is at most 2 MB, and is a PNG, WebP or SVG, as its contents (not just its extension) show.
- `pack.json` must:
  - parse;
  - use `format` 1;
  - have a valid `id` and an `en` name;
  - use one of the two layouts;
  - use only the sprite keys listed above;
  - use only colours in `palette`.
- Paths stay inside the zip: no absolute paths, no `..`, no backslashes, no links. A sprite's path uses only letters, digits, `.`, `_`, `-` and `/`.
- There are no URLs anywhere. A pack cannot load anything from another site.
- There is no code. The format has no scripts, CSS or HTML. Pictures load only as images (`<img>`), where an SVG's scripts never run. Pack files are also served with `Content-Security-Policy: sandbox; default-src 'none'` and `X-Content-Type-Options: nosniff`, so even opening one directly in a tab runs nothing.

## Examples

`examples/view-packs/` has two packs to copy from, one for each layout. To install one, zip its folder and install the zip:

```bash
cd examples/view-packs && zip -r neon-station.zip neon-station
```

## API

For scripts. Every call needs a signed-in session on a server with accounts.

| Call | Who | Does |
|---|---|---|
| `GET /api/packs` | anyone signed in | lists the installed packs, with each sprite resolved to a URL |
| `GET /api/packs/<id>/files/<path>` | anyone signed in | one of a pack's images |
| `POST /api/packs` | admins | installs `{"zip": "<base64 of the .zip>"}` |
| `DELETE /api/packs/<id>` | admins | removes a pack |

Installs and removals are written to the audit log (`pack.install`, `pack.remove`).
