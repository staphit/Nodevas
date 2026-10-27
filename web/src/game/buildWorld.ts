// One call from store snapshots to the whole world. Pure: same inputs, same
// world, so a hook can memoise it on the slices it selects plus the presence
// tick (presence.ts), and tests need no stores.

import type { GraphAnalysis } from "../analysis";
import type { AgentAsk, AgentMessage, AgentSession } from "../api/agents";
import type { AutopilotView } from "../api/autopilot";
import type { Runner } from "../api/runners";
import type { PipelineState, Run, RunBudgetStatus } from "../api/runs";
import type { Graph, HistoryEvent, Status } from "../types";
import { buildDungeon } from "./dungeon";
import { buildOffice } from "./office";
import type { World } from "./world";

export interface WorldInputs {
  graph: Graph | null | undefined;
  statuses: Record<string, Status>;
  /** analyzeGraph(graph, statuses), when the caller already has it. */
  analysis?: GraphAnalysis;
  /** runState.history, for XP. */
  history?: HistoryEvent[];
  projectName: string;
  /** Runs from every source in view (the active list, the latest per node,
   * a node's full list); duplicates allowed, the last copy wins. */
  runs: Run[];
  runners: Runner[];
  sessions: AgentSession[];
  asks: AgentAsk[];
  held: AgentMessage[];
  messages: AgentMessage[];
  pipelines?: Record<string, PipelineState | undefined>;
  autopilots: AutopilotView[];
  budget?: RunBudgetStatus | null;
  now: number;
}

/** Node id → title, for the characters' desk screens. */
export function nodeTitles(graph: Graph | null | undefined): Map<string, string> {
  return new Map((graph?.nodes ?? []).map((node) => [node.id, node.title ?? ""]));
}

export function buildWorld(inputs: WorldInputs): World {
  const titles = nodeTitles(inputs.graph);
  return {
    now: inputs.now,
    office: buildOffice({
      runs: inputs.runs,
      runners: inputs.runners,
      sessions: inputs.sessions,
      asks: inputs.asks,
      held: inputs.held,
      messages: inputs.messages,
      pipelines: inputs.pipelines,
      budget: inputs.budget,
      titles,
      now: inputs.now,
    }),
    dungeon: buildDungeon({
      graph: inputs.graph,
      statuses: inputs.statuses,
      analysis: inputs.analysis,
      runs: inputs.runs,
      autopilots: inputs.autopilots,
      history: inputs.history,
      projectName: inputs.projectName,
      now: inputs.now,
    }),
  };
}
