/**
 * Project tree expansion [B-06].
 *
 * Which projects show their children survives a reload, so the set is mirrored
 * into the UI preferences on every change rather than kept only in React state.
 */

import { useEffect, useRef, useState } from "react";
import type { ProjectEntry } from "../../state/types";
import { useApp } from "../../store";

function storedExpandedProjects(): Set<string> {
  return new Set(useApp.getState().preferences.explorerExpandedProjects);
}

export function useProjectExpansion(
  activeProject: string,
  projectByName: Map<string, ProjectEntry>,
) {
  const updateUIPreference = useApp((state) => state.updateUIPreference);
  const [expandedProjects, setExpandedProjects] = useState(storedExpandedProjects);

  const persistExpandedProjects = (next: Set<string>) => {
    setExpandedProjects(next);
    updateUIPreference("explorerExpandedProjects", [...next]);
  };

  const toggleProject = (name: string) => {
    const next = new Set(expandedProjects);
    if (next.has(name)) next.delete(name);
    else next.add(name);
    persistExpandedProjects(next);
  };

  // Reveal a newly opened project once. Re-running on every project-list
  // refresh would keep re-expanding the ancestors the user just collapsed.
  const revealedProject = useRef<string | null>(null);
  useEffect(() => {
    if (!activeProject) return;
    if (revealedProject.current === activeProject) return;
    let current = projectByName.get(activeProject);
    if (!current) return; // project list has not caught up yet
    revealedProject.current = activeProject;
    const next = new Set(expandedProjects);
    while (current) {
      next.add(current.name);
      current = current.parent ? projectByName.get(current.parent) : undefined;
    }
    if (next.size !== expandedProjects.size) persistExpandedProjects(next);
  }, [activeProject, projectByName]);

  return { expandedProjects, persistExpandedProjects, toggleProject };
}
