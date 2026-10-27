import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { Actor } from "../../api";
import { usePacks } from "../../game/packStore";
import { readPreferences } from "../../preferences";
import { useApp } from "../../store";
import { ViewPacksEditor } from "./ViewPacksEditor";

let viewer: Actor | null = null;
vi.mock("../SignIn", () => ({ useViewer: () => viewer }));

const neon = {
  id: "neon",
  name: { en: "Neon station" },
  layout: "rooms" as const,
  sprites: {},
  installedAt: 1,
};

beforeEach(() => {
  useApp.setState({ preferences: { ...readPreferences(), language: "en", projectMode: "info", stationPack: "" } });
  usePacks.setState({ packs: [neon], loaded: true, busy: false, load: async () => {} });
});

const notify = () => ({ onError: vi.fn(), onNotice: vi.fn() });

describe("ViewPacksEditor", () => {
  it("lets an admin install and remove, and anyone open a pack in Station", async () => {
    viewer = { id: "a", name: "ann", role: "admin" };
    const remove = vi.fn(async () => "");
    usePacks.setState({ remove });
    const n = notify();
    render(<ViewPacksEditor notify={n} />);
    expect(screen.getByText("Neon station")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Install a pack (.zip)" })).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: "Remove" }));
    await vi.waitFor(() => expect(n.onNotice).toHaveBeenCalledWith("Removed Neon station."));
    expect(remove).toHaveBeenCalledWith("neon");

    fireEvent.click(screen.getByRole("button", { name: "Open in Station" }));
    const prefs = useApp.getState().preferences;
    expect(prefs.stationPack).toBe("neon");
    expect(prefs.stageSkin).toBe("warroom");
    expect(prefs.projectMode).toBe("station");
  });

  it("shows a member the packs but not install or remove", () => {
    viewer = { id: "b", name: "bea", role: "member" };
    render(<ViewPacksEditor notify={notify()} />);
    expect(screen.getByText("Neon station")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Install a pack (.zip)" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Remove" })).toBeNull();
  });

  it("says why an install was refused", async () => {
    viewer = null;
    usePacks.setState({ install: async () => "pack.json: unknown sprite key \"agent.dancing\"" });
    const n = notify();
    const { container } = render(<ViewPacksEditor notify={n} />);
    const input = container.querySelector('input[type="file"]')!;
    fireEvent.change(input, { target: { files: [new File(["x"], "bad.zip")] } });
    await vi.waitFor(() =>
      expect(n.onError).toHaveBeenLastCalledWith('The pack was not installed: pack.json: unknown sprite key "agent.dancing"'),
    );
  });
});
