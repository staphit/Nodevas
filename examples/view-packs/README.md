# Example view packs

Two view packs to copy from, one for each layout. A view pack restyles Station's war room or boss map with your own pictures. The format and its rules are in [docs/view-packs.md](../../docs/view-packs.md).

| Pack | Layout | Look |
|---|---|---|
| [`neon-station`](./neon-station/) | `rooms` (war room) | A space station on a dark ground. Each agent is an astronaut at a console, with a different pose per state: typing, holding a tablet, a hand raised, sparks on the screen, leaning back, an empty seat, standing by the console, or holding a mug. |
| [`candy-dungeon`](./candy-dungeon/) | `path` (boss map) | Candy land on a light ground. Nodes are gumdrops: open to work, wrapped up when locked, and an empty wrapper with crumbs when settled. The boss is a giant cake, which ends as a single slice. The party leader is a gingerbread knight, on wafer and frosted tiles. |

Each pack is a `pack.json` and an `images/` folder of hand-written SVG files.

## Install one

Zip the pack's folder:

```bash
cd examples/view-packs && zip -r neon-station.zip neon-station
```

On macOS, add `-x '*.DS_Store'` to leave Finder's files out of the zip.

Then an admin installs the zip in **Project settings → Appearance → View packs → Install a pack (.zip)**. Pick the pack from Station's view switch.

## Check a pack

```bash
python3 -I examples/view-packs/check.py
```

It checks every pack here against the rules in [docs/view-packs.md](../../docs/view-packs.md):
- the keys in `pack.json`, and the sprite keys allowed for its layout;
- that every sprite file exists, is an SVG, and is under the size limits;
- that each SVG's `viewBox` has its box's proportions;
- that no SVG has a script, a `foreignObject`, or a URL, and that `pack.json` has no URLs.

It uses only the Python standard library. Copy a pack, change it, and run the check before you zip it.

## 繁體中文

這裡有兩個範例視圖包，每種版面各一個，可以照著改。視圖包用你自己的圖片重繪 Station 的戰情室或頭目地圖。格式與規則見 [docs/view-packs.md](../../docs/view-packs.md)。

- **`neon-station`（霓虹太空站）**，版面 `rooms`（戰情室）：深色底的太空站。每個代理是一位坐在控制台前的太空人，每種狀態有不同姿勢：打字、拿著平板、舉手、螢幕冒火花、往後靠、空座位、站在控制台旁、拿著馬克杯。
- **`candy-dungeon`（糖果地城）**，版面 `path`（頭目地圖）：淺色底的糖果世界。節點是軟糖怪：可以處理時是軟糖，被擋住時包著糖果紙，完成後只剩糖果紙和碎屑。頭目是一個大蛋糕，打完只剩一片。隊長是薑餅騎士，走在威化餅和糖霜地磚上。

每個視圖包是一個 `pack.json` 加上一個放手寫 SVG 的 `images/` 資料夾。

**安裝：** 先把資料夾壓縮成 zip：

```bash
cd examples/view-packs && zip -r neon-station.zip neon-station
```

在 macOS 上可以加上 `-x '*.DS_Store'`，把 Finder 的檔案排除在外。接著由管理員在 **專案設定 → 外觀 → 視圖包 → 安裝視圖包（.zip）** 安裝，再從 Station 的視圖切換選擇它。

**檢查：** 壓縮前執行 `python3 -I examples/view-packs/check.py`。它只用 Python 標準函式庫，會檢查 `pack.json` 的欄位與該版面允許的圖片鍵、每個圖片檔是否存在、是否為 SVG、是否超過大小上限、`viewBox` 比例是否符合，以及 SVG 裡沒有腳本、`foreignObject` 或網址，`pack.json` 裡也沒有網址。
