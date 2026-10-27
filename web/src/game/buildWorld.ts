// One call from store snapshots to the war room's world. Pure: same inputs, same
// world, so a hook can memoise it on the slices it selects plus the presence
// tick (presence.ts), and tests need no stores.

import type { AgentAsk, AgentMessage, AgentSession } from "../api/agents";
import type { Runner } from "../api/runners";
import type { PipelineState, Run, RunBudgetStatus } from "../api/runs";
import type { Graph } from "../types";
import { buildOffice } from "./office";
import type { World } from "./world";

export interface WorldInputs {
  graph: Graph | null | undefined;
  /** Runs from every source in view (the active list, the latest per node,
   * a node's full list); duplicates allowed, the last copy wins. */
  runs: Run[];
  runners: Runner[];
  sessions: AgentSession[];
  asks: AgentAsk[];
  held: AgentMessage[];
  messages: AgentMessage[];
  pipelines?: Record<string, PipelineState | undefined>;
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
  };
}
