import { afterEach, describe, expect, it, vi } from "vitest";
import { api } from "../api";
import { readPreferences } from "../preferences";
import { useApp } from "../store";

afterEach(() => vi.restoreAllMocks());

// An example opens in the interface's language.
describe("opening an example", () => {
  it("asks for the copy in the interface language", async () => {
    useApp.setState({ preferences: { ...readPreferences(), language: "zh-TW" } });
    const open = vi.spyOn(api, "openExample").mockResolvedValue({ ok: true, name: "novel-writing", active: "novel-writing" });
    useApp.setState({ switchProject: vi.fn(async () => {}) });
    await useApp.getState().openExample("novel-writing");
    expect(open).toHaveBeenCalledWith("novel-writing", "zh-TW");
  });

  it("sends the language with the request", async () => {
    const fetch = vi
      .spyOn(globalThis, "fetch")
      .mockResolvedValue(new Response(JSON.stringify({ ok: true, name: "x", active: "x" }), { status: 200 }));
    await api.openExample("novel-writing", "zh-TW");
    const init = fetch.mock.calls[0][1] as RequestInit;
    expect(JSON.parse(String(init.body))).toEqual({ example: "novel-writing", language: "zh-TW" });
  });
});
