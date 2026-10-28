import { fireEvent, render, screen } from "@testing-library/react";
import { readdirSync, readFileSync, statSync } from "node:fs";
import { join } from "node:path";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { applyTextScale, configurePreferenceStorage, readPreference, readPreferences } from "../../preferences";
import { useApp } from "../../store";
import { AppearanceEditor } from "./AppearanceEditor";

vi.mock("../SignIn", () => ({ useViewer: () => null }));

beforeEach(() => {
  useApp.setState({ preferences: { ...readPreferences(), language: "en", textScale: 1 } });
});

// The text size is one slider in General, from 90% to 150%, applied to the
// whole interface at once.
describe("text size", () => {
  it("is a slider in Appearance that changes the preference", () => {
    render(<AppearanceEditor />);
    const slider = screen.getByRole("slider", { name: "Text size" });
    expect(slider).toHaveAttribute("min", "0.9");
    expect(slider).toHaveAttribute("max", "1.5");
    expect(slider).toHaveValue("1");
    fireEvent.change(slider, { target: { value: "1.5" } });
    expect(useApp.getState().preferences.textScale).toBe(1.5);
    expect(screen.getByText("150%")).toBeInTheDocument();
  });

  it("resets to 100% in one click", () => {
    useApp.setState({ preferences: { ...useApp.getState().preferences, textScale: 1.3 } });
    render(<AppearanceEditor />);
    fireEvent.click(screen.getByRole("button", { name: "Reset text size" }));
    expect(useApp.getState().preferences.textScale).toBe(1);
  });

  it("is stored clamped to 90%–150%", () => {
    configurePreferenceStorage({ getItem: () => "3", setItem: () => {}, removeItem: () => {} } as unknown as Storage);
    expect(readPreference("textScale")).toBe(1.5);
  });

  it("sets the --font-scale every font size multiplies", () => {
    applyTextScale(1.25);
    expect(document.documentElement.style.getPropertyValue("--font-scale")).toBe("1.25");
  });
});

// Every fixed font size in the stylesheets follows the text size, except
// what the board's own zoom already scales (the canvas) and the pixel-art
// war room, which keeps whole pixels.
describe("stylesheets", () => {
  const SKIP = [/10b-canvas-decorations\.css$/, /10e-canvas-gates\.css$/, /10f-canvas-overrides\.css$/, /warroom\.css$/];
  const files: string[] = [];
  const walk = (dir: string) => {
    for (const name of readdirSync(dir)) {
      const path = join(dir, name);
      if (statSync(path).isDirectory()) walk(path);
      else if (name.endsWith(".css") && !SKIP.some((re) => re.test(path))) files.push(path);
    }
  };
  walk(join(__dirname, "..", ".."));

  it("multiply fixed font sizes by --font-scale", () => {
    const fixed: string[] = [];
    for (const file of files) {
      readFileSync(file, "utf8")
        .split("\n")
        .forEach((line, i) => {
          if (/font-size:\s*[\d.]+px/.test(line)) fixed.push(`${file}:${i + 1}: ${line.trim()}`);
        });
    }
    expect(fixed).toEqual([]);
  });
});
