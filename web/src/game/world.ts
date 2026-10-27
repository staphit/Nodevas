// The war room's world: what it draws, in terms of no particular drawing.
// The workspace is a building: an office per project (the current one in
// full, the others as a glance through the door), a lounge everyone shares
// and a server room with a rack per runner. Everything here is plain data built by pure functions (buildWorld.ts) from
// store snapshots, so the drawing only decides how an entity looks, never
// what it means, and the whole model is testable without React. Labels are
// not here either: the drawing puts them through t().

import type { AgentAsk, AgentMessage } from "../api/agents";
import type { OfficeStyle } from "../types";

export type { OfficeStyle };

/** The parts of a project's office (the lounge is shared by the building). */
export type Area = "work" | "meeting" | "entrance" | "lounge";

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
  | { kind: "pr"; runId: string; url: string }
  /** Another project's office: switch to it. */
  | { kind: "project"; project: string }
  /** Something in another project: switch to it, then do `then` there. */
  | { kind: "in-project"; project: string; then: ClickTarget }
  /** An autopilot's goal, in Station. */
  | { kind: "autopilot"; goalNodeId: string }
  /** The server room: Settings › Agents, the runners. */
  | { kind: "runners" };

/** What an agent character is doing, most urgent first (see pose.ts for the
 * precedence). Every pose needs a non-colour cue in the drawing. */
export type Pose =
  /** Its session timed out, or the runner it sits at is offline. */
  | "offline"
  /** It asked the user something (ask_user) and waits for the answer. */
  | "asking-user"
  /** The run failed, or its PR's CI went red. */
  | "failed"
  /** Queued again after its agent hit a usage limit: asleep until the
   * limit resets (Run.retryAt). */
  | "limit"
  /** Waiting for someone at the runner to accept the offer (queued or
   * offered), or for an approved merge to land. */
  | "awaiting-accept"
  /** Its PR is open and CI is running. */
  | "waiting-ci"
  /** A review run at work. */
  | "reviewing"
  | "working"
  /** The run succeeded a moment ago (office.FINISHED_SHOW_MS). */
  | "finished"
  | "idle";

export const POSES: readonly Pose[] = [
  "offline",
  "asking-user",
  "failed",
  "limit",
  "awaiting-accept",
  "waiting-ci",
  "reviewing",
  "working",
  "finished",
  "idle",
];

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
  /** The project whose office it is in. */
  project: string;
  /** Where in the office it is (game/areas.ts). */
  area: Area;
  runnerId?: string;
  /** The runner it works at; absent for a session (a terminal of its own). */
  runnerName?: string;
  /** null when the runner is not one the caller can list, or there is none. */
  runnerOnline: boolean | null;
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
  /** Agent roles: a run's role (its record's, or its kind's for a legacy
   * run); a session never has one. */
  role?: string;
  /** The latest verify of its pipeline, for a Tester or Executor. */
  testState?: "red" | "green";
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
  /** The project whose meeting room it is in. */
  project: string;
  round: number;
  /** The pipeline's status when it is loaded, "" otherwise. */
  status: string;
  /** It waits for a person's review. */
  waitingHuman: boolean;
  actorIds: string[];
  votes: MeetingVote[];
  target: ClickTarget;
}

/** Something on the meeting-room table that waits on the viewer and has no
 * agent at it: a PR ready to merge, an autopilot review waiting for a
 * person, a held message to an agent not in the room. */
export interface MeetingItem {
  /** "merge:<runId>" | "review:<autopilotId>:<nodeId>" | "held:<msgId>". */
  id: string;
  kind: "merge" | "review" | "held";
  nodeId?: string;
  title: string;
  target: ClickTarget;
  actions: ClickTarget[];
}

/** The current project's office, in full. */
export interface ProjectOffice {
  project: string;
  label: string;
  layout: string;
  style: OfficeStyle;
  /** The project chose its style (otherwise it is the layout's default). */
  styleSet: boolean;
  /** Actor ids at desks, in seating order. */
  work: string[];
  /** Free desks shown next to them: min(3, online runners' free slots). */
  freeDesks: number;
  /** Actor ids in the meeting room. */
  meeting: string[];
  items: MeetingItem[];
  /** Actor ids waiting at the door to be let in. */
  entrance: string[];
  /** Review pipelines. */
  meetings: Meeting[];
}

/** A desk in another project's office, drawn small. */
export interface MiniDesk {
  agent: string;
  pose: Pose;
  title: string;
}

/** Another project's office: its door sign and a glance inside. */
export interface CompactOffice {
  project: string;
  label: string;
  style: OfficeStyle;
  /** At most three: running, then planning, then offered. */
  desks: MiniDesk[];
  /** Seated beyond the three drawn. */
  more: number;
  counts: { running: number; waiting: number; offered: number; sleeping: number; othersActive: number };
  busy: boolean;
  /** {kind: "project"}. */
  target: ClickTarget;
  /** Go there first, then its runs and what waits on the viewer there. */
  actions: ClickTarget[];
}

/** A seat in the shared lounge. */
export interface LoungeSpot {
  id: string;
  /** The current project's actor (its full menu). */
  actorId?: string;
  /** Another project's agent. */
  visitor?: {
    project: string;
    label: string;
    agent: string;
    model: string;
    pose: "idle" | "limit" | "offline";
    title: string;
    target: ClickTarget;
    actions: ClickTarget[];
  };
}

/** A runner, as a rack in the server room. */
export interface Rack {
  runnerId: string;
  name: string;
  ownerName?: string;
  hostname?: string;
  /** null when the caller cannot list the runner. */
  online: boolean | null;
  slots?: number;
  /** Runs offered to or running on it, in every project in view. */
  busy: number;
  agentKinds: string[];
  target: { kind: "runners" };
}

/** The workspace as a building: an office per project. */
export interface Building {
  current: ProjectOffice | null;
  /** Other busy projects, busiest first, at most MAX_COMPACT. */
  others: CompactOffice[];
  /** Idle projects, and busy ones past MAX_COMPACT: door signs only. */
  doorSigns: CompactOffice[];
  /** The current project's actors first, then visitors; at most 10. */
  lounge: LoungeSpot[];
  loungeMore: number;
  racks: Rack[];
  /** The current project's actors. */
  actors: Actor[];
  /** The caller's tokens today against their daily budget, when known. */
  energy?: Energy;
  /** The hub left projects out. */
  truncated: boolean;
  /** Projects in the workspace the viewer may read. */
  total: number;
}

export interface World {
  /** The clock the world was built for. */
  now: number;
  building: Building;
}
