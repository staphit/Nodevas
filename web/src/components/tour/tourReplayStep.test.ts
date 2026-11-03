import { expect, it } from "vitest";
import { translate } from "../../i18n";
import { chapterById } from "./chapters";

// The "watch it again" step pointed people at a theme icon beside Settings
// and at Settings in the top-right corner. The top bar has no theme icon (the
// theme is under Settings › General) and Settings sits with Board and Station
// in the mode switch, left of the search box.
const step = chapterById("shell")!.steps.find((s) => s.id === "shell-replay")!;

it("does not send anyone to a theme icon beside Settings", () => {
  expect(translate(step.bodyKey, "zh-TW")).not.toMatch(/小圖示|圖示切換/);
  expect(translate(step.bodyKey, "en")).not.toMatch(/icon/i);
});

it("does not place Settings in the top-right corner", () => {
  expect(translate(step.bodyKey, "zh-TW")).not.toContain("右上角");
  expect(translate(step.bodyKey, "en")).not.toMatch(/top right/i);
});

it("says where the theme is now", () => {
  expect(translate(step.bodyKey, "zh-TW")).toMatch(/一般.*主題/);
  expect(translate(step.bodyKey, "en")).toMatch(/General.*theme/);
});
