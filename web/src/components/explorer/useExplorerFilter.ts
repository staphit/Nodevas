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
  const activeNodes = useMemo(
    () =>
      [...(graph?.nodes ?? [])].sort((left, right) =>
        (left.title || left.id).localeCompare(right.title || right.id),
      ),
    [graph?.nodes],
  );
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
  // Sorting happens on the tree, not on the flat list, so a child never ends
  // up before its parent; see explorer/sortProjects.
  const sortedProjects = useMemo(
    () => sortProjectTree(projects, projectSort, projectOrder),
    [projects, projectSort, projectOrder],
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
