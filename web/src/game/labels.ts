// Words for the world model's entities, shared by the war room's drawing and
// its text list so a sprite's accessible name and its sentence always agree.
// Everything goes through t(); product names (Claude, Codex…) are not
// translated.

import { excerpt } from "../agents/agentStore";
import type { Actor, ClickTarget, Room } from "./world";

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

export function roomName(t: Translate, room: Pick<Room, "kind" | "name">): string {
  if (room.kind === "terminal") return t("game.room.terminal");
  return room.name || t("game.room.unnamed");
}

export function poseLabel(t: Translate, pose: Actor["pose"]): string {
  return t(`game.pose.${pose}`);
}

/** One sentence for a character: who, where, doing what, on which node. */
export function actorLabel(t: Translate, actor: Actor, room: string): string {
  const parts = [
    actor.nodeTitle
      ? t("game.actor.label", {
          who: agentWho(actor.agent, actor.model),
          room,
          pose: poseLabel(t, actor.pose),
          node: actor.nodeTitle,
        })
      : t("game.actor.labelNoNode", {
          who: agentWho(actor.agent, actor.model),
          room,
          pose: poseLabel(t, actor.pose),
        }),
  ];
  if (actor.ask) parts.push(t("game.actor.asks", { question: excerpt(actor.ask.question, 80) }));
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

export function actionLabel(t: Translate, target: ClickTarget, titleOf: TitleOf): string {
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
  }
}
