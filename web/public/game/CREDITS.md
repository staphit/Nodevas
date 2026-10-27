# Game view art credits

## kenney-isometric-dungeon/

Sprites from **Isometric Miniature Dungeon** ("Dungeon Pack" 2.3) by Kenney
(www.kenney.nl), licensed **CC0 1.0** (public domain dedication); the pack's
own `License.txt` is kept next to the sprites.

- Source: https://kenney.nl/assets/isometric-miniature-dungeon
  (zip: `kenney_isometric-miniature-dungeon.zip`), downloaded 2026-10-26.
- Only the sprites the boss map draws are vendored, cropped to their opaque
  box and scaled to 62.5% (twice their on-screen size): `floor-stone`
  (`stone_N`), `floor-uneven` (`stoneUneven_N`), `floor-dirt`
  (`dirtTiles_N`), `chest-open` / `chest-closed` (`chestOpen_S` /
  `chestClosed_S`), `gate-closed` / `gate-open` (`stoneWallGateClosed_S` /
  `stoneWallGateOpen_S`), `column` (`stoneColumn_N`), `barrel` (`barrel_S`),
  `hero-idle` (`Characters/Male/Male_2_Idle0`) and `hero-run`
  (`Male_2_Run0`–`Run9`, stitched into one strip).
- Kenney's "Isometric Dungeon Tiles" pack is only distributed through itch.io,
  whose download flow needs a browser session; this sibling pack from kenney.nl has the same 2:1 isometric look,
  the same licence, and the floors, doors, chests and the character the map
  uses.

The monsters, the boss, the war room's office and its agent characters are
drawn by hand as inline SVG in `web/src/game/` (no third-party art).
