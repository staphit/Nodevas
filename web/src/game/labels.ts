// Words for the world model's entities, shared by the war room's drawing and
// its text list so a sprite's accessible name and its sentence always agree.
// Everything goes through t(); product names (Claude, Codex…) are not
// translated.

import { askQuestionText } from "../agents/AskCard";
import { excerpt } from "../agents/agentStore";
import { formatList } from "../i18n";
import { runnerDisplayName } from "../runners/runnerName";
import type { Actor, Area, ClickTarget, CompactOffice, MeetingItem, OfficeStyle, ProjectOffice, Rack } from "./world";

export type Translate = (key: string, values?: Record<string, string | number>) => string;

const AGENT_NAMES: Record<string, string> = {
  claude: "Claude",
  codex: "Codex",
  gemini: "Gemini",
  opencode: "OpenCode",
  antigravity: "Antigravity",
  copilot: "Copilot",
};

export function agentName(kind: string): string {
  if (!kind) return "Agent";
  return AGENT_NAMES[kind.toLowerCase()] ?? kind.charAt(0).toUpperCase() + kind.slice(1);
}

/** "Claude opus", or just the agent when the model is the default. */
export function agentWho(agent: string, model: string): string {
  const name = agentName(agent);
  return model && model !== "default" ? `${name} ${model}` : name;
}

/** Agent kinds that have a colour of their own; anything else is "other". */
export const AGENT_COLORS: readonly string[] = ["claude", "codex", "gemini", "opencode", "antigravity"];

export function agentColorKey(agent: string): string {
  const kind = agent.toLowerCase();
  return AGENT_COLORS.includes(kind) ? kind : "other";
}

/** Where a character works, for its sentence: its runner, or "Terminals"
 * for an agent someone runs in their own terminal. */
export function placeOf(t: Translate, actor: Pick<Actor, "runnerName">): string {
  return runnerDisplayName(t, actor.runnerName) || t("game.room.terminal");
}

export function areaName(t: Translate, area: Area): string {
  return t(`game.area.${area}`);
}

export function styleName(t: Translate, style: OfficeStyle): string {
  return t(`office.style.${style}`);
}

/** The office's name with its style: "Office app (Engineering)". */
export function officeLabel(t: Translate, office: Pick<ProjectOffice, "label" | "style">): string {
  return t("common.withHint", {
    label: t("game.office.label", { name: office.label }),
    hint: styleName(t, office.style),
  });
}

/** What a glance through another office's door shows, in words. */
export function compactSummary(t: Translate, c: Pick<CompactOffice, "counts">): string {
  const parts: string[] = [];
  if (c.counts.running) parts.push(t("game.compact.working", { count: c.counts.running }));
  if (c.counts.waiting) parts.push(t("game.compact.waiting", { count: c.counts.waiting }));
  if (c.counts.offered) parts.push(t("game.compact.offered", { count: c.counts.offered }));
  if (c.counts.sleeping) parts.push(t("game.compact.sleeping", { count: c.counts.sleeping }));
  if (c.counts.othersActive) parts.push(t("game.compact.others", { count: c.counts.othersActive }));
  return parts.length ? formatList(parts) : t("game.compact.idle");
}

/** "app: 2 working, 1 waiting on you, Engineering. Go to app". */
export function compactLabel(t: Translate, c: Pick<CompactOffice, "counts" | "label" | "style">): string {
  return t("game.compact.label", { name: c.label, summary: compactSummary(t, c), style: styleName(t, c.style) });
}

export function rackLabel(t: Translate, rack: Rack): string {
  const state =
    rack.online === null ? t("game.rack.unknown") : rack.online ? t("game.room.online") : t("game.room.offline");
  const name = runnerDisplayName(t, rack.name) || t("game.room.unnamed");
  return rack.slots
    ? t("game.rack.label", { name, state, busy: rack.busy, slots: rack.slots })
    : t("game.rack.labelNoSlots", { name, state, busy: rack.busy });
}

export function itemLabel(t: Translate, item: Pick<MeetingItem, "kind" | "title">): string {
  return t(`game.item.${item.kind}`, { title: item.title });
}

export function poseLabel(t: Translate, pose: Actor["pose"]): string {
  return t(`game.pose.${pose}`);
}

const BUILTIN_ROLES: ReadonlySet<string> = new Set(["planner", "qa", "tester", "executor", "reviewer", "researcher"]);

/** "Acts as Tester.": the role a run acts in; a session has none. */
function roleSentence(t: Translate, role: string): string {
  return t("game.label.role", { role: BUILTIN_ROLES.has(role) ? t(`roles.name.${role}`) : role });
}

/** One sentence for a character: who, where, doing what, on which node. */
export function actorLabel(t: Translate, actor: Actor, room: string): string {
  const who = agentWho(actor.agent, actor.model);
  const parts = [
    actor.nodeTitle
      ? t("game.actor.label", {
          who,
          room,
          pose: poseLabel(t, actor.pose),
          node: actor.nodeTitle,
        })
      : t("game.actor.labelNoNode", {
          who,
          room,
          pose: poseLabel(t, actor.pose),
        }),
  ];
  if (actor.role) parts.push(roleSentence(t, actor.role));
  if (actor.ask) parts.push(t("game.actor.asks", { question: excerpt(askQuestionText(t, actor.ask, who), 80) }));
  if (actor.heldMail.length) parts.push(t("game.actor.held", { count: actor.heldMail.length }));
  if (actor.bubble) parts.push(t("game.actor.says", { text: excerpt(actor.bubble.body, 80) }));
  if (actor.swappedFrom) {
    parts.push(
      t("game.actor.swapped", {
        from: agentWho(actor.swappedFrom.agent, actor.swappedFrom.model),
        reason: t(`game.failure.${actor.swappedFrom.failureKind}`),
      }),
    );
  }
  if (actor.energy) parts.push(energyLabel(t, actor.energy.used, actor.energy.cap));
  return parts.join(" ");
}

export function formatTokens(value: number): string {
  if (value >= 1_000_000) return `${(value / 1_000_000).toFixed(value >= 10_000_000 ? 0 : 1)}M`;
  if (value >= 1_000) return `${(value / 1_000).toFixed(value >= 10_000 ? 0 : 1)}k`;
  return String(Math.round(value));
}

export function energyLabel(t: Translate, used: number, cap?: number): string {
  return cap
    ? t("game.energy.ofCap", { used: formatTokens(used), cap: formatTokens(cap) })
    : t("game.energy.used", { used: formatTokens(used) });
}

/** Node id → title, "" when unknown. */
export type TitleOf = (nodeId: string) => string;

/** Project name → its label, for "Go to …". */
export type LabelOf = (project: string) => string;

export function actionLabel(t: Translate, target: ClickTarget, titleOf: TitleOf, labelOf: LabelOf = (p) => p): string {
  switch (target.kind) {
    case "node":
      return t("game.action.node", { title: titleOf(target.nodeId) || target.nodeId });
    case "run":
      return t("game.action.run");
    case "run-diff":
      return t("game.action.runDiff");
    case "session-diff":
      return t("game.action.sessionDiff");
    case "session":
      return t("game.action.session");
    case "ask":
      return t("game.action.ask");
    case "held-message":
      return t("game.action.held");
    case "pr":
      return t("game.action.pr");
    case "project":
      return t("game.action.project", { name: labelOf(target.project) });
    case "in-project":
      return t("game.action.inProject", {
        action: actionLabel(t, target.then, () => "", labelOf),
        name: labelOf(target.project),
      });
    case "autopilot":
      return t("game.action.autopilot");
    case "runners":
      return t("game.action.runners");
  }
}
