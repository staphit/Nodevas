import { afterEach, describe, expect, it, vi } from "vitest";
import { api } from "../api";
import { readPreferences } from "../preferences";
import { useApp } from "../store";

afterEach(() => vi.restoreAllMocks());

// A template writes titles into the new project (the Eisenhower quadrants), so
// it is asked for in the interface's language, as an example is. Left out, the
// hub falls back to the browser's Accept-Language: an English interface in a
// Chinese browser got a board of Chinese quadrant titles.
describe("creating a project from a template", () => {
  it("asks for the template in the interface language", async () => {
    useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
    const open = vi.spyOn(api, "openProject").mockResolvedValue({ root: "/tmp/m", active: "m" });
    vi.spyOn(api, "getGraph").mockRejectedValue(new Error("not needed"));
    await useApp
      .getState()
      .switchProject("matrix", true, "eisenhower")
      .catch(() => {});
    expect(open).toHaveBeenCalledWith("matrix", true, "eisenhower", "en");
  });

  it("sends the language with the request", async () => {
    const fetch = vi
      .spyOn(globalThis, "fetch")
      .mockResolvedValue(new Response(JSON.stringify({ root: "/tmp/m", active: "m" }), { status: 200 }));
    await api.openProject("matrix", true, "eisenhower", "en");
    const init = fetch.mock.calls[0][1] as RequestInit;
    expect(JSON.parse(String(init.body))).toEqual({
      name: "matrix",
      create: true,
      template: "eisenhower",
      language: "en",
    });
  });
});
