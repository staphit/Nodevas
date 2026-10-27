// The war room's world: what it draws, in terms of no particular drawing.
// Everything here is plain data built by pure functions (buildWorld.ts) from
// store snapshots, so the drawing only decides how an entity looks, never
// what it means, and the whole model is testable without React. Labels are
// not here either: the drawing puts them through t().

import type { AgentAsk, AgentMessage } from "../api/agents";

/** What clicking an entity opens. The drawing maps each kind to an existing
 * UI action (open the node, open the run in the drawer, show a diff); the
 * model only says which. */
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
  | { kind: "pr"; runId: string; url: string };

/** What an agent character is doing, most urgent first (see pose.ts for the
 * precedence). Every pose needs a non-colour cue in the drawing. */
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
  /** The runner's name; "" for the terminal room (the drawing names it). */
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
  /** The daily token budget, when one is set; the drawing shows a bar only
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

export interface World {
  /** The clock the world was built for. */
  now: number;
  office: Office;
}
