/** How a key press or a spoken answer maps to a permission decision. */

import type { CopilotDecision } from "../api";

export function keyToDecision(key: string): CopilotDecision | null {
  switch (key) {
    case "1":
      return "allow";
    case "2":
      return "allow_session";
    case "3":
      return "deny";
    default:
      return null;
  }
}

// Chinese words match anywhere in the phrase (no word breaks to rely on);
// English words must stand alone, so "know" is not "no" and "okay" is "ok".
const DENY_ZH = ["取消", "拒絕", "不要", "不行", "不用", "不好", "不是", "否"];
const ALLOW_ZH = ["確認", "確定", "允許", "可以", "好", "是", "對"];
const DENY_EN = new Set(["no", "nope", "deny", "cancel", "stop", "don't", "dont"]);
const ALLOW_EN = new Set(["yes", "yeah", "yep", "allow", "ok", "okay", "confirm", "sure"]);

/**
 * A spoken answer to a pending permission request. Refusals are checked first:
 * "不好" contains "好" and "不是" contains "是", and a missed yes only costs a
 * repeat while a misheard no would run the tool.
 */
export function parseVoiceDecision(transcript: string): "allow" | "deny" | null {
  const text = transcript.toLowerCase().trim();
  if (!text) return null;
  const words = text.split(/[^a-z']+/).filter(Boolean);
  if (DENY_ZH.some((word) => text.includes(word)) || words.some((word) => DENY_EN.has(word))) {
    return "deny";
  }
  if (ALLOW_ZH.some((word) => text.includes(word)) || words.some((word) => ALLOW_EN.has(word))) {
    return "allow";
  }
  return null;
}
