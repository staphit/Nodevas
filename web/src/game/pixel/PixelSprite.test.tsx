import { render } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import fixture from "../anime/fixture.json";
import { parseManifest, toSheet } from "../anime/manifest";
import { deskScene, doorScene, outdoorScene } from "../warroom/deskArt";
import { PixelSprite } from "./PixelSprite";
import { CELL } from "./sprites";
import { OUTDOOR_H, OUTDOOR_W } from "./outdoorSprites";

const SHEET = toSheet("gpt", parseManifest(fixture)!, "/sheets/fixture.webp");

function draw(parts: Parameters<typeof PixelSprite>[0]["parts"], width = CELL, height = CELL) {
  return render(<PixelSprite parts={parts} width={width} height={height} />).container;
}

describe("PixelSprite", () => {
  it("paints pixel parts as paths on the grid, and no image", () => {
    const svg = draw(doorScene("codex", { character: "gpt" })).querySelector("svg")!;
    expect(svg.getAttribute("shape-rendering")).toBe("crispEdges");
    expect(svg.querySelector('[data-part="head.front"] path')).not.toBeNull();
    expect(svg.querySelector("image")).toBeNull();
  });

  it("shows an anime figure as its action's row of the sheet, through the window of its frame in the scene", () => {
    const svg = draw(doorScene("codex", { character: "gpt", anime: SHEET })).querySelector("svg")!;
    const figure = svg.querySelector('[data-part="anime.door-idle"]')!;
    expect(figure).toHaveClass("px-anime");
    expect(figure.getAttribute("transform")).toBe("translate(0 1)");
    // 32 × 31 art pixels of the scene show 192 × 186 of the frame (its
    // bottom row below the doorway's floor is cut).
    const frame = figure.querySelector("svg")!;
    expect([frame.getAttribute("width"), frame.getAttribute("height"), frame.getAttribute("viewBox")]).toEqual(["32", "31", "0 0 192 186"]);
    const strip = frame.querySelector("image")!;
    expect(strip.getAttribute("href")).toBe("/sheets/fixture.webp");
    expect([strip.getAttribute("x"), strip.getAttribute("y"), strip.getAttribute("width"), strip.getAttribute("height")]).toEqual([
      "0",
      "0",
      "1536",
      "768",
    ]);
    expect(strip.getAttribute("preserveAspectRatio")).toBe("none");
    // Rendered art, not pixels: nothing asks for pixelated scaling.
    expect(strip.getAttribute("image-rendering")).toBeNull();
    expect(strip.getAttribute("shape-rendering")).toBeNull();
  });

  it("loops an action's frames with CSS: eight steps over the strip, each frame a frame's width along", () => {
    const strip = draw(doorScene("codex", { character: "gpt", anime: SHEET })).querySelector<SVGImageElement>("image")!;
    expect(strip).toHaveClass("px-anime-strip");
    expect(strip).toHaveClass("is-looping");
    expect(strip.style.getPropertyValue("--px-anime-loop")).toBe("800ms");
    expect(strip.style.getPropertyValue("--px-anime-steps")).toBe("8");
    expect(strip.style.getPropertyValue("--px-anime-span")).toBe("-1536px");
  });

  it("finds a lower action's row further up the sheet", () => {
    const desk = draw(deskScene("working", "codex", { character: "gpt", anime: SHEET, activity: "shell" }));
    expect(desk.querySelector('[data-part="anime.desk-type"] image')!.getAttribute("y")).toBe("-192");
    const ride = draw(outdoorScene({ kind: "cycle", step: 0 }, "codex", { character: "gpt", anime: SHEET }), OUTDOOR_W, OUTDOOR_H);
    const frame = ride.querySelector('[data-part="anime.cycle-ride"] svg')!;
    // The frame starts 5 art pixels left of the tile: 30 of its pixels are cut.
    expect(frame.getAttribute("viewBox")).toBe("30 0 162 192");
    expect(frame.querySelector("image")!.getAttribute("y")).toBe("-576");
  });

  it("holds a still figure on its first frame", () => {
    const sheet = toSheet(
      "gpt",
      parseManifest({ ...fixture, actions: { ...fixture.actions, "desk-rest": fixture.actions["desk-type"] } })!,
      "/sheets/fixture.webp",
    );
    const strip = draw(deskScene("offline", "codex", { character: "gpt", anime: sheet })).querySelector("image")!;
    expect(strip).toHaveClass("px-anime-strip");
    expect(strip).not.toHaveClass("is-looping");
  });

  it("keeps its image when the figure moves to another action, the strip on the new row", () => {
    const sheet = toSheet(
      "gpt",
      parseManifest({ ...fixture, actions: { ...fixture.actions, "desk-mouse": { ...fixture.actions["desk-type"], row: 3 } } })!,
      "/sheets/fixture.webp",
    );
    const scene = (activity: "edit" | "search") => deskScene("working", "codex", { character: "gpt", anime: sheet, activity });
    const { container, rerender } = render(<PixelSprite parts={scene("edit")} width={CELL} height={CELL} />);
    const strip = container.querySelector("image")!;
    expect(strip.getAttribute("y")).toBe("-192");
    rerender(<PixelSprite parts={scene("search")} width={CELL} height={CELL} />);
    expect(container.querySelector('[data-part="anime.desk-mouse"] image')).toBe(strip);
    expect(strip.getAttribute("y")).toBe("-576");
  });
});
