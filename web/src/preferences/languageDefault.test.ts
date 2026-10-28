import { afterEach, describe, expect, it, vi } from "vitest";
import { configurePreferenceStorage, readPreference } from ".";

const empty = () => ({ getItem: () => null, setItem: () => {}, removeItem: () => {} }) as unknown as Storage;

afterEach(() => vi.restoreAllMocks());

// Until the viewer picks one, the interface speaks the browser's language.
describe("default interface language", () => {
  it("is Traditional Chinese for a Chinese browser", () => {
    vi.spyOn(navigator, "languages", "get").mockReturnValue(["zh-TW", "en"]);
    configurePreferenceStorage(empty());
    expect(readPreference("language")).toBe("zh-TW");
  });

  it("is English otherwise", () => {
    vi.spyOn(navigator, "languages", "get").mockReturnValue(["fr-FR", "en"]);
    configurePreferenceStorage(empty());
    expect(readPreference("language")).toBe("en");
  });
});
