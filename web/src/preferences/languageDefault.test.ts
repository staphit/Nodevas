import { afterEach, describe, expect, it } from "vitest";
import { configurePreferenceStorage, readPreference } from ".";

const empty = () => ({ getItem: () => null, setItem: () => {}, removeItem: () => {} }) as unknown as Storage;

function browserLanguages(languages: string[]) {
  Object.defineProperty(navigator, "languages", { configurable: true, get: () => languages });
}

afterEach(() => {
  delete (navigator as { languages?: unknown }).languages;
});

// Until the viewer picks one, the interface speaks the browser's language.
describe("default interface language", () => {
  it("is Traditional Chinese for a Chinese browser", () => {
    browserLanguages(["zh-TW", "en"]);
    configurePreferenceStorage(empty());
    expect(readPreference("language")).toBe("zh-TW");
  });

  it("is English otherwise", () => {
    browserLanguages(["fr-FR", "en"]);
    configurePreferenceStorage(empty());
    expect(readPreference("language")).toBe("en");
  });
});
