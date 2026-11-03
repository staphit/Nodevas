/**
 * The project list with the open project's node count read off the graph.
 *
 * The list is fetched on open and switch, not on every node create or delete
 * (nor on a peer's), so the open project's count is read off the graph on
 * screen — once that graph is the active project's — which is exactly what the
 * server counts (len(graph.nodes)). The explorer tree and the top bar's project
 * menu both draw counts, and both go through this so they never disagree.
 */

import type { ProjectEntry } from "./types";

export function withLiveNodeCount(
  projects: ProjectEntry[],
  activeProject: string,
  graphProject: string,
  nodeCount: number | undefined,
): ProjectEntry[] {
  if (nodeCount === undefined || !activeProject || graphProject !== activeProject) {
    return projects;
  }
  return projects.some((project) => project.name === activeProject && project.nodes !== nodeCount)
    ? projects.map((project) =>
        project.name === activeProject ? { ...project, nodes: nodeCount } : project,
      )
    : projects;
}
