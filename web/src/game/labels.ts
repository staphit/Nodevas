// Words for the world model's entities, shared by the scenes and the list
// view so a sprite's accessible name and its list sentence always agree.
// Everything goes through t(); product names (Claude, Codex…) are not
// translated.

import { excerpt } from "../agents/agentStore";
import type { Actor, ClickTarget, Door, Monster, Party, Room } from "./world";

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
    case "autopilot":
      return t("game.action.autopilot");
    case "pr":
      return t("game.action.pr");
  }
}

export function monsterStateLabel(t: Translate, monster: Pick<Monster, "state">): string {
  return t(`game.monster.${monster.state}`);
}

export function monsterLabel(t: Translate, monster: Monster, titleOf: TitleOf): string {
  const parts = [
    t("game.monster.label", {
      title: monster.title || monster.nodeId,
      state: monsterStateLabel(t, monster),
    }),
  ];
  if (monster.state === "locked" && monster.blockers.length) {
    parts.push(
      t("game.monster.lockedBy", {
        names: monster.blockers.map((id) => titleOf(id) || id).join(", "),
      }),
    );
  }
  if (monster.engaged) parts.push(t("game.monster.engaged"));
  if (monster.merged) parts.push(t("game.monster.merged"));
  if (monster.critical) parts.push(t("game.monster.critical"));
  if (monster.entry) parts.push(t("game.monster.entry"));
  return parts.join(" ");
}

export function doorLabel(t: Translate, door: Door, titleOf: TitleOf): string {
  const output = titleOf(door.output) || door.output;
  return door.open
    ? t("game.door.open", { operator: door.operator.toUpperCase(), output })
    : t("game.door.shut", {
        operator: door.operator.toUpperCase(),
        output,
        names: door.blockers.map((id) => titleOf(id) || id).join(", "),
      });
}

export function partyLabel(t: Translate, party: Party, titleOf: TitleOf): string {
  const at = party.positions.map((id) => titleOf(id) || id).join(", ");
  const parts = [
    t("game.party.label", {
      goal: titleOf(party.goalNodeId) || party.goalNodeId,
      done: party.done,
      total: party.total,
    }),
  ];
  parts.push(t(`game.party.status.${party.status}`));
  if (at) parts.push(t("game.party.at", { names: at }));
  if (party.waitingHuman.length) parts.push(t("game.party.waiting", { count: party.waitingHuman.length }));
  if (party.failed) parts.push(t("game.party.failed", { count: party.failed }));
  return parts.join(" ");
}
