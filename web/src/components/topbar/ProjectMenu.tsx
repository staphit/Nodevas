/**
 * The project menu: one button, named for the project the board shows, in
 * place of the old Workspace / Project breadcrumb, its <select> and the
 * WORKFLOW / STORY chip. Inside: the projects (the current one checked), the
 * project's layout, its settings, and the workspaces to switch to.
 */

import { IconChevronDown, IconGear } from "../../icons";
import { useMemo } from "react";
import { useI18n } from "../../i18n";
import { openSettings } from "../../settings/openSettings";
import { withLiveNodeCount } from "../../state/liveProjects";
import { reportError, useApp } from "../../store";
import { TopbarMenu, type TopbarMenuEntry, type TopbarMenuItem } from "./TopbarMenu";

export function ProjectMenu() {
  const { t } = useI18n();
  const listed = useApp((s) => s.projects);
  const activeProject = useApp((s) => s.activeProject);
  const graphProject = useApp((s) => s.graphProject);
  const nodeCount = useApp((s) => s.graph?.nodes?.length);
  const refreshProjects = useApp((s) => s.refreshProjects);
  // The open project's count follows graph edits, as the explorer's does.
  const projects = useMemo(
    () => withLiveNodeCount(listed, activeProject, graphProject, nodeCount),
    [listed, activeProject, graphProject, nodeCount],
  );
  const switchProject = useApp((s) => s.switchProject);
  const workspaces = useApp((s) => s.workspaces);
  const workspace = useApp((s) => s.workspace);
  const switchWorkspace = useApp((s) => s.switchWorkspace);
  const graphType = useApp((s) => s.graph?.type ?? "");

  const current = projects.find((p) => p.name === activeProject);
  const name = current?.label || activeProject || t("topbar.noProject");

  // The projects, and further down the workspaces, are each one group named
  // by its heading.
  const entries: TopbarMenuEntry[] = [
    {
      kind: "group",
      id: "projects",
      label: t("topbar.projects"),
      items: projects
        .filter((p) => !p.isFolder)
        .map<TopbarMenuItem>((p) => ({
          id: `project-${p.name}`,
          label: p.label || p.name,
          detail: String(p.nodes),
          title: t("topbar.nodeCount", { count: p.nodes }),
          depth: p.depth,
          checked: p.name === activeProject,
          onSelect: () => {
            if (p.name !== activeProject) void switchProject(p.name).catch(reportError);
          },
        })),
    },
  ];

  // The layout is fixed when a project is created; nothing on the hub changes
  // it afterwards, so it is said here rather than offered as a switch.
  if (graphType) {
    entries.push({
      id: "layout",
      label: t("topbar.layout", { type: layoutName(graphType, t) }),
      title: t("topbar.layoutTitle"),
      disabled: true,
      separatorBefore: true,
      onSelect: () => undefined,
    });
  }
  entries.push({
    id: "project-settings",
    label: t("topbar.projectSettings"),
    title: t("topbar.projectSettingsTitle"),
    icon: <IconGear size={14} />,
    separatorBefore: !graphType,
    onSelect: () => openSettings("project"),
  });

  // What the breadcrumb named. More than one workspace is a choice; one is
  // only where we are, and adding another is the explorer's job.
  const roots = workspaces.length > 0
    ? workspaces
    : workspace
      ? [{ path: workspace, label: "", active: true, projects: projects.length }]
      : [];
  if (roots.length > 0) {
    entries.push({
      kind: "group",
      id: "workspaces",
      label: t("topbar.workspace"),
      separatorBefore: true,
      items: roots.map((root) => ({
        id: `workspace-${root.path}`,
        label: root.label || root.path,
        title: root.path,
        checked: root.active,
        disabled: roots.length === 1,
        onSelect: () => {
          if (!root.active) void switchWorkspace(root.path).catch(reportError);
        },
      })),
    });
  }

  return (
    <TopbarMenu
      className="topbar-project"
      triggerClassName="topbar-project-trigger"
      label={t("topbar.projectMenu", { name })}
      title={activeProject ? t("topbar.switchProject") : undefined}
      trigger={
        <>
          <span className="topbar-project-name">{name}</span>
          <IconChevronDown size={14} />
        </>
      }
      entries={entries}
      // Projects added on disk, and other projects' counts, are only in a
      // fresh list; one coalesced read per open keeps them current. A failed
      // read leaves the list as it was rather than toasting over the menu.
      onOpen={() => void refreshProjects().catch(() => undefined)}
    />
  );
}

function layoutName(type: string, t: (key: string) => string): string {
  return type === "workflow" || type === "story" ? t(`topbar.layout.${type}`) : type;
}
