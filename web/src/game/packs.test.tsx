import { render } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import type { ViewPack } from "../api";
import { KENNEY, KenneyImg } from "./dungeon/DungeonArt";
import { PackArt, PackContext, packSprite } from "./packs";
import { packName, safePack } from "./packStore";

const url = (path: string) => `/api/packs/neon/files/${path}?v=1`;

function pack(over: Partial<ViewPack> = {}): ViewPack {
  return {
    id: "neon",
    name: { en: "Neon", "zh-TW": "霓虹" },
    layout: "rooms",
    palette: { ground: "#0e1726", text: "#e6eef7" },
    sprites: { agent: url("images/agent.svg"), "agent.asking-user": url("images/hand.svg") },
    installedAt: 1,
    ...over,
  };
}

describe("safePack", () => {
  it("keeps only this server's pack files and real colours", () => {
    const safe = safePack(
      pack({
        sprites: {
          agent: url("images/agent.svg"),
          a: "https://evil.example/x.png",
          b: "/api/packs/neon/files/../../../api/state",
          c: '/api/packs/neon/files/x".png',
          d: "/api/packs/other/files/x.png",
        },
        palette: { ground: "red; background: url(x)", text: "#fff" },
      }),
    )!;
    expect(Object.keys(safe.sprites)).toEqual(["agent", "d"]);
    expect(safe.palette).toEqual({ ground: undefined, text: "#fff" });
  });

  it("refuses an unknown layout or a bad id", () => {
    expect(safePack(pack({ layout: "grid" as never }))).toBeNull();
    expect(safePack(pack({ id: "Bad ID" }))).toBeNull();
  });

  it("names a pack in the reader's language, else English, else its id", () => {
    expect(packName(pack(), "zh-TW")).toBe("霓虹");
    expect(packName(pack(), "fr")).toBe("Neon");
    expect(packName(pack({ name: {} }), "en")).toBe("neon");
  });
});

describe("drawing with a pack", () => {
  it("takes the first key the pack has a picture for", () => {
    expect(packSprite(pack(), ["agent.asking-user", "agent"])).toBe(url("images/hand.svg"));
    expect(packSprite(pack(), ["agent.working", "agent"])).toBe(url("images/agent.svg"));
    expect(packSprite(pack(), ["table"])).toBeUndefined();
    expect(packSprite(null, ["agent"])).toBeUndefined();
  });

  it("draws the pack's picture as an image, or the built-in art", () => {
    const { container, rerender } = render(
      <PackContext.Provider value={pack()}>
        <PackArt keys={["agent.working", "agent"]} className="game-desk" width={80} height={96}>
          <svg data-testid="built-in" />
        </PackArt>
      </PackContext.Provider>,
    );
    const img = container.querySelector("img")!;
    expect(img.getAttribute("src")).toBe(url("images/agent.svg"));
    expect(img.className).toBe("game-desk pack-sprite");
    expect(img.getAttribute("width")).toBe("80");

    rerender(
      <PackContext.Provider value={pack()}>
        <PackArt keys={["table"]} className="game-table" width={96} height={56}>
          <svg data-testid="built-in" />
        </PackArt>
      </PackContext.Provider>,
    );
    expect(container.querySelector("img")).toBeNull();
    expect(container.querySelector("[data-testid=built-in]")).not.toBeNull();
  });

  it("replaces a Kenney sprite by its pack key, keeping its box", () => {
    const { container } = render(
      <PackContext.Provider value={pack({ layout: "path", sprites: { "gate.closed": url("images/gate.svg") } })}>
        <KenneyImg sprite={KENNEY.gateClosed} x={0} y={0} />
        <KenneyImg sprite={KENNEY.barrel} x={0} y={0} />
      </PackContext.Provider>,
    );
    const [gate, barrel] = [...container.querySelectorAll("img")];
    expect(gate.getAttribute("src")).toBe(url("images/gate.svg"));
    expect(gate.getAttribute("width")).toBe(String(KENNEY.gateClosed.w));
    expect(barrel.getAttribute("src")).toContain("/game/kenney-isometric-dungeon/");
  });
});
