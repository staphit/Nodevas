// The game views' world: what the war room and the boss map draw, in terms
// of no particular skin. Everything here is plain data built by pure
// functions (buildWorld.ts) from store snapshots, so a skin only decides how
// an entity looks, never what it means, and the whole model is testable
// without React. Labels are not here either: skins put them through t().

import type { AgentAsk, AgentMessage } from "../api/agents";
import type { LogicGateOperator, NodeKind } from "../types";

/** What clicking an entity opens. A skin maps each kind to an existing UI
 * action (open the node, open the run in the drawer, show a diff, open the
 * autopilot panel); the model only says which. */
export type ClickTarget =
  | { kind: "node"; nodeId: string }
  | { kind: "run"; runId: string; nodeId: string }
  | { kind: "run-diff"; runId: string; nodeId: string }
  /** An external session's changes on a node (SessionChange.sessionId is
   * AgentSession.id). */
  | { kind: "session-diff"; sessionId: string; nodeId: string }
  /** A session that holds no node: there is only the session to show. */
  | { kind: "session"; sessionId: string }
  | { kind: "ask"; askId: string; sessionId: string }
  | { kind: "held-message"; messageId: string }
  | { kind: "autopilot"; autopilotId: string; goalNodeId: string }
  | { kind: "pr"; runId: string; url: string };

// ---- War room -------------------------------------------------------------

/** What an agent character is doing, most urgent first (see pose.ts for the
 * precedence). Every pose needs a non-colour cue in the skin. */
export type Pose =
  /** Its session timed out, or the runner it sits at is offline. */
  | "offline"
  /** It asked the user something (ask_user) and waits for the answer. */
  | "asking-user"
  /** The run failed, or its PR's CI went red. */
  | "failed"
  /** Waiting for someone at the runner to accept the offer (queued or
   * offered), or for an approved merge to land. */
  | "awaiting-accept"
  /** Its PR is open and CI is running. */
  | "waiting-ci"
  /** A review run at work: a seat at the meeting table. */
  | "reviewing"
  | "working"
  | "idle";

export const POSES: readonly Pose[] = [
  "offline",
  "asking-user",
  "failed",
  "awaiting-accept",
  "waiting-ci",
  "reviewing",
  "working",
  "idle",
];

export type RoomKind = "runner" | "terminal";

/** The id of the room for agents with no runner: sessions people run in
 * their own terminals, and run sessions whose run is not in view. */
export const TERMINAL_ROOM_ID = "terminal";

export interface Room {
  id: string;
  kind: RoomKind;
  /** The runner's name; "" for the terminal room (the skin names it). */
  name: string;
  runnerId?: string;
  ownerName?: string;
  hostname?: string;
  /** null when the runner is not one the caller can list (an
   * administrator's view of someone else's runner): nothing is known. */
  online: boolean | null;
  /** Agent kinds installed on the runner, for the desks it has. */
  agentKinds: string[];
  /** Actor ids in this room, in display order. */
  actorIds: string[];
}

export interface Energy {
  /** Input + output tokens. */
  used: number;
  /** The daily token budget, when one is set; the skin draws a bar only
   * then, a counter otherwise. */
  cap?: number;
}

export interface SwappedFrom {
  runId: string;
  agent: string;
  model: string;
  failureKind: string;
}

export interface Actor {
  /** "run:<runId>" or "session:<sessionId>". */
  id: string;
  kind: "run" | "session";
  /** claude / codex / gemini / opencode / antigravity / … */
  agent: string;
  model: string;
  roomId: string;
  pose: Pose;
  nodeId?: string;
  /** The node's title from the graph; "" when the node is not in it. */
  nodeTitle: string;
  runId?: string;
  /** The agent session behind the character, when there is one in view. */
  sessionId?: string;
  sessionName?: string;
  /** Who the character works for: the run's dispatcher or the session's
   * account. */
  ownerName: string;
  /** The pending ask_user, drawn as a raised hand. */
  ask?: AgentAsk;
  /** The newest message it sent within the bubble window. */
  bubble?: AgentMessage;
  /** Messages to it a person still has to approve: sealed envelopes. */
  heldMail: AgentMessage[];
  /** A fallback run's predecessor: the seat swap animation. */
  swappedFrom?: SwappedFrom;
  energy?: Energy;
  /** The review pipeline it sits in. */
  pipelineId?: string;
  /** What a click opens. */
  target: ClickTarget;
  /** Everything the character's popover offers, target first. */
  actions: ClickTarget[];
}

export interface MeetingVote {
  agent: string;
  model: string;
  verdict: string;
  runId?: string;
}

/** A review pipeline's reviewers around one table. */
export interface Meeting {
  pipelineId: string;
  nodeId: string;
  nodeTitle: string;
  roomId: string;
  round: number;
  /** The pipeline's status when it is loaded, "" otherwise. */
  status: string;
  /** It waits for a person's review. */
  waitingHuman: boolean;
  actorIds: string[];
  votes: MeetingVote[];
  target: ClickTarget;
}

export interface Office {
  rooms: Room[];
  actors: Actor[];
  meetings: Meeting[];
  /** The caller's tokens today against their daily budget, when known. */
  energy?: Energy;
}

// ---- Boss map ---------------------------------------------------------------

/** attackable: open to work (not settled, nothing blocks it; the server's
 * "ready" alone does not mean that). locked: something blocks it.
 * defeated: settled (done, skipped, deprecated or a settled custom state). */
export type MonsterState = "attackable" | "locked" | "defeated";

export interface Monster {
  nodeId: string;
  title: string;
  kind: NodeKind | string;
  /** The node's priority, as difficulty. */
  priority: string;
  status: string;
  state: MonsterState;
  /** What blocks it, by node id, while locked. */
  blockers: string[];
  /** An entrance: nothing points at it. */
  entry: boolean;
  /** On the critical path: the corridor to the boss. */
  critical: boolean;
  /** Only reachable through deprecated wires: drawn faded. */
  faded: boolean;
  /** A run or an autopilot task is at work on it. */
  engaged: boolean;
  /** A run's PR on it was merged. */
  merged: boolean;
  target: ClickTarget;
}

/** A boolean logic gate in front of its output node. */
export interface Door {
  gateId: string;
  operator: LogicGateOperator;
  inputs: string[];
  output: string;
  open: boolean;
  /** Why it is shut, by node id. */
  blockers: string[];
  /** The first blocker while shut, the output once open. */
  target: ClickTarget;
}

export interface Boss {
  /** The project's name, as the caller knows it. */
  name: string;
  /** Unsettled nodes, start/end/event excluded. */
  hp: number;
  maxHp: number;
  /** hp / maxHp, 0 for an empty project. */
  ratio: number;
  defeated: boolean;
}

/** A live autopilot, moving through its goal's tasks. */
export interface Party {
  autopilotId: string;
  goalNodeId: string;
  status: string;
  /** Tasks it is on now (running or waiting on a review or a person), in
   * the autopilot's topological order. */
  positions: string[];
  /** Tasks waiting for a person. */
  waitingHuman: string[];
  total: number;
  done: number;
  failed: number;
  target: ClickTarget;
}

export interface Dungeon {
  boss: Boss;
  monsters: Monster[];
  doors: Door[];
  parties: Party[];
  /** Completions: lifecycle history events that reached done. */
  xp: number;
  /** Nodes with a merged run PR, in graph order. */
  mergedNodeIds: string[];
}

export interface World {
  /** The clock the world was built for. */
  now: number;
  office: Office;
  dungeon: Dungeon;
}
