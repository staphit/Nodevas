/**
 * Explorer search [B-06].
 *
 * One query box filters both halves of the tree — the open project's nodes and
 * the project list — so the two filters have to agree about what a match is and
 * are derived together here.
 */

import { useMemo, useState } from "react";
import type { ProjectEntry } from "../../state/types";
import type { Graph } from "../../types";
import { useApp, usePreference } from "../../store";
import { sortProjectTree } from "./sortProjects";

export function useExplorerFilter(
  graph: Graph | null,
  projects: ProjectEntry[],
  activeProject: string,
  projectByName: Map<string, ProjectEntry>,
  expandedProjects: Set<string>,
) {
  const [query, setQuery] = useState("");

  const userNames = useMemo(
    () => new Map((graph?.users ?? []).map((user) => [user.id, user.name])),
    [graph?.users],
  );

  const normalizedQuery = query.trim().toLocaleLowerCase();
  // Natural order, like the project list (sortProjects): "Card 2" before
  // "Card 10". One collator for the whole sort, not one per comparison.
  const activeNodes = useMemo(() => {
    const collator = new Intl.Collator("zh-Hant", { numeric: true, sensitivity: "base" });
    return [...(graph?.nodes ?? [])].sort((left, right) =>
      collator.compare(left.title || left.id, right.title || right.id),
    );
  }, [graph?.nodes]);
  const visibleNodes = normalizedQuery
    ? activeNodes.filter((node) =>
        `${node.title ?? ""} ${node.id} ${
          (node.assignee && userNames.get(node.assignee)) ?? ""
        }`
          .toLocaleLowerCase()
          .includes(normalizedQuery),
      )
    : activeNodes;
  const projectSort = usePreference("explorerProjectSort");
  const projectOrder = useApp((state) => state.projectOrder);
  const graphProject = useApp((state) => state.graphProject);
  // The project list is fetched on open and switch, not on every node create
  // or delete (nor on a peer's), so the open project's count is read off the
  // graph on screen — once that graph is the active project's — which is
  // exactly what the server counts (len(graph.nodes)).
  const nodeCount = graph?.nodes?.length;
  const liveProjects = useMemo(() => {
    if (nodeCount === undefined || !activeProject || graphProject !== activeProject) {
      return projects;
    }
    return projects.map((project) =>
      project.name === activeProject && project.nodes !== nodeCount
        ? { ...project, nodes: nodeCount }
        : project,
    );
  }, [activeProject, graphProject, nodeCount, projects]);
  // Sorting happens on the tree, not on the flat list, so a child never ends
  // up before its parent; see explorer/sortProjects.
  const sortedProjects = useMemo(
    () => sortProjectTree(liveProjects, projectSort, projectOrder),
    [liveProjects, projectSort, projectOrder],
  );
  const visibleProjects = useMemo(() => {
    if (normalizedQuery) {
      const matches = new Set<string>();
      for (const project of projects) {
        if (
          `${project.label} ${project.name}`
            .toLocaleLowerCase()
            .includes(normalizedQuery) ||
          (project.name === activeProject && visibleNodes.length > 0)
        ) {
          let current: typeof project | undefined = project;
          while (current && !matches.has(current.name)) {
            matches.add(current.name);
            current = current.parent ? projectByName.get(current.parent) : undefined;
          }
        }
      }
      return sortedProjects.filter((project) => matches.has(project.name));
    }
    return sortedProjects.filter((project) => {
      let parent = project.parent;
      while (parent) {
        if (!expandedProjects.has(parent)) return false;
        parent = projectByName.get(parent)?.parent ?? "";
      }
      return true;
    });
  }, [
    activeProject,
    expandedProjects,
    normalizedQuery,
    projectByName,
    projects,
    sortedProjects,
    visibleNodes.length,
  ]);

  return {
    query,
    setQuery,
    normalizedQuery,
    userNames,
    activeNodes,
    visibleNodes,
    projectSort,
    projectOrder,
    visibleProjects,
  };
}
