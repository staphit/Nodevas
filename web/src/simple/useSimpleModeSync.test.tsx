// Simple mode keeps finished work on this computer: while it is on, the
// built-in runner's "open a pull request when done" is turned off once each
// time the runner comes online, and never turned back on.

import { act, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi, type MockInstance } from "vitest";

import { api, type Runner } from "../api";
import { localRunner } from "../local/localFixtures";
import { configurePreferenceStorage, readPreferences } from "../preferences";
import { EditionProvider, type Edition } from "../runners/edition";
import { useRunners } from "../runners/runnerStore";
import { useApp } from "../store";
import { useSimpleModeSync } from "./useSimpleModeSync";

function fakeStorage(initial: Record<string, string> = {}) {
  const map = new Map(Object.entries(initial));
  return {
    getItem: (key: string) => map.get(key) ?? null,
    setItem: (key: string, value: string) => void map.set(key, value),
    removeItem: (key: string) => void map.delete(key),
  };
}

function prefs(experience: "full" | "simple") {
  configurePreferenceStorage(fakeStorage({ "vised-experience": experience }));
  useApp.setState({ preferences: readPreferences() });
}

/** The built-in runner with its "open a pull request" setting. */
function local(autoPR: boolean, overrides: Partial<Runner> = {}): Runner {
  const base = localRunner(overrides);
  return { ...base, info: { ...base.info!, local: { ...base.info!.local!, autoPR } } };
}

function seed(runners: Runner[]) {
  useRunners.setState({ loaded: true, loading: false, runners, error: null, creating: false, revoking: {} });
}

const inEdition =
  (edition: Edition) =>
  ({ children }: { children?: ReactNode }) => <EditionProvider edition={edition}>{children}</EditionProvider>;

function mount(edition: Edition = "single") {
  return renderHook(() => useSimpleModeSync(), { wrapper: inEdition(edition) });
}

/** Lets pending promises (the request, its answer) settle. */
async function settle() {
  await act(async () => {
    await Promise.resolve();
    await Promise.resolve();
  });
}

let settings: MockInstance<typeof api.setRunnerSettings>;

beforeEach(() => {
  prefs("simple");
  seed([]);
  settings = vi.spyOn(api, "setRunnerSettings").mockImplementation(async (id, body) => ({
    runner: local(body.autoPR ?? true, { id }),
    persisted: true,
  }));
  vi.spyOn(api, "listRunners").mockResolvedValue({ runners: [] });
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe("useSimpleModeSync", () => {
  it("turns the built-in runner's pull requests off, exactly once", async () => {
    seed([local(true)]);
    const { rerender } = mount();
    await waitFor(() => expect(settings).toHaveBeenCalledTimes(1));
    expect(settings).toHaveBeenCalledWith("local1", { autoPR: false });
    await settle();
    // The answer replaces the runner in the list: off now, nothing more.
    expect(useRunners.getState().runners[0].info?.local?.autoPR).toBe(false);
    rerender();
    act(() => seed([local(true)]));
    await settle();
    expect(settings).toHaveBeenCalledTimes(1);
  });

  it.each<[string, () => void, Edition]>([
    ["pull requests are already off", () => seed([local(false)]), "single"],
    ["the runner is offline", () => seed([local(true, { online: false })]), "single"],
    ["the runner is not built in", () => seed([local(true, { builtIn: undefined, hubLocal: undefined })]), "single"],
    ["the app is a team hub", () => seed([local(true)]), "team"],
    ["the pref is full", () => {
      prefs("full");
      seed([local(true)]);
    }, "single"],
  ])("sends nothing when %s", async (_why, arrange, edition) => {
    arrange();
    mount(edition);
    await settle();
    expect(settings).not.toHaveBeenCalled();
  });

  it("does not retry a refused change until the runner comes online again", async () => {
    settings.mockRejectedValue(new Error("busy"));
    seed([local(true)]);
    mount();
    await waitFor(() => expect(settings).toHaveBeenCalledTimes(1));
    await settle();
    act(() => seed([local(true)]));
    await settle();
    expect(settings).toHaveBeenCalledTimes(1);

    act(() => seed([local(true, { online: false })]));
    await settle();
    expect(settings).toHaveBeenCalledTimes(1);
    act(() => seed([local(true)]));
    await waitFor(() => expect(settings).toHaveBeenCalledTimes(2));
  });

  it("asks for the runner list once when nothing has loaded it", async () => {
    useRunners.setState({ loaded: false });
    const list = vi.spyOn(api, "listRunners").mockResolvedValue({ runners: [local(true)] });
    mount();
    await waitFor(() => expect(settings).toHaveBeenCalledWith("local1", { autoPR: false }));
    expect(list).toHaveBeenCalledTimes(1);
  });

  it("never loads the list or sends anything with the preset off", async () => {
    prefs("full");
    useRunners.setState({ loaded: false });
    const list = vi.spyOn(api, "listRunners");
    mount();
    await settle();
    expect(list).not.toHaveBeenCalled();
    expect(settings).not.toHaveBeenCalled();
  });

  it("only ever sends autoPR: false", async () => {
    seed([local(true)]);
    mount();
    await waitFor(() => expect(settings).toHaveBeenCalled());
    for (const [, body] of settings.mock.calls) expect(body).toEqual({ autoPR: false });
  });
});
