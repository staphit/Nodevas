import { describe, expect, it } from "vitest";
import { hasMessage } from "./i18n";

const each = (prefix: string, names: string[]) => names.map((name) => `${prefix}.${name}`);

/** Every string the analytics, Explorer and war-room roles work adds. */
const KEYS = [
  ...each("explorer.tab", ["nodes", "files", "git", "agent"]),
  "explorer.tabs",
  ...each("explorer.files", [
    "workspace", "hub", "repo", "offline", "offlineNote", "readOnlyNote", "readOnlyNoteNone", "search",
    "browseOff", "noRepos", "truncated",
  ]),
  ...each("explorer.file", ["readOnly", "openNode", "binary", "truncatedFile", "hidden"]),
  ...each("explorer.git", ["workspace", "pendingNone", "branches", "commits", "filesBackend", "uncommitted", "whichAgent", "byPerson", "statOne", "statMany", "markSeparator"]),
  ...each("explorer.git.mark", ["M", "A", "D", "R"]),
  ...each("explorer.commit", ["group", "separator", "kindSeparator", "graph", "more", "update"]),
  ...each("explorer.commit.kind", [
    "create", "delete", "edit", "move", "restore", "pages", "addPage", "editPage", "deletePage", "convertPage", "outsideEdit",
  ]),
  ...each("explorer.git.ci", ["pass", "fail", "pending"]),
  ...each("explorer.agent", [
    "live", "external", "recent", "tokens", "cache", "cost", "estimate", "time", "verdict", "filesTouched", "events",
    "openNode", "read", "none", "repoOffline",
  ]),
  ...each("explorer.agent.state", ["running", "succeeded", "failed", "cancelled", "online", "offline"]),
  ...each("explorer.empty", ["title", "hint"]),
  "explorer.close",
  ...each("explorer.action", ["joined", "left", "doing", "message", "ask", "changesOne", "changesMany", "usage"]),
  "verdict.pass",
  "verdict.fail",
  ...each("verdict.severity", ["none", "low", "medium", "high", "critical"]),
  ...each("verdict.reason", [
    "tests_missing", "tests_weak", "criteria_unmet", "regression", "style", "security", "scope_creep", "flaky", "other",
  ]),
  "verdict.contradicted",
  ...each("warroom.roleBadge", ["planner", "qa", "tester", "executor", "reviewer", "researcher", "custom"]),
  "warroom.test.red",
  "warroom.test.green",
  "game.label.role",
  "settingsPage.part.analytics",
  "analytics.value",
  "analytics.valueNone",
  ...each("analytics.tab", ["insights", "scorecards", "tiers"]),
  ...each("analytics", [
    "window", "costPerDelivered", "firstPass", "rework", "waste", "hotspots", "estimateNote", "empty", "suggestions",
    "apply", "applyConfirmTitle", "applyConfirm", "dismiss", "open", "applied", "stale", "adjusted", "tiersHint",
    "autoRouting", "autoRoutingHint", "requiredTier", "exploration", "explorationHint", "picks", "retention",
    "retentionDays", "retentionForever", "retentionHint",
  ]),
  ...each("analytics.waste", ["failed", "retries", "abandoned"]),
  ...each("analytics.by", ["role", "agent", "model", "nodeType", "project"]),
  ...each("analytics.suggest", ["cheaper_equal_tier", "demote_unreliable", "enable_auto_routing", "role_budget", "tester_first"]),
  ...each("analytics.change", ["routing_rule_target", "auto_routing", "role_budget"]),
  ...each("analytics.col", [
    "role", "agent", "model", "samples", "delivery", "firstPass", "rework", "costEff", "speed", "reliability", "tdd",
    "accuracy", "score", "tier",
  ]),
  ...each("analytics.track", ["all", "code", "story"]),
  ...each("tier", ["senior", "mid", "probation"]),
  ...each("analytics.pick.reason", ["manual", "cheapest_meeting_tier", "explore", "none_meets_tier", "budget_exceeded", "no_runner"]),
  "usage.byRole",
  "runs.route.auto",
];

// W-23: every new string exists in English and Traditional Chinese.
describe("analytics, Explorer and war-room strings", () => {
  it.each(KEYS)("%s is in both catalogs", (key) => {
    expect(hasMessage("en", key)).toBe(true);
    expect(hasMessage("zh-TW", key)).toBe(true);
  });
});
