/** Graph filter and saved-view panel [B-06]. */

import type { Graph, SavedView, Status, StatusDefinition } from "../types";
import { localizedStatusLabel, useI18n } from "../i18n";

export interface ViewFilter {
  query: string;
  status: string;
  assignee: string;
  tag: string;
  priority: string;
  sort: "manual" | "title" | "priority" | "status" | "assignee";
}

export interface GraphToolsPanelProps {
  graph: Graph | null;
  customStatuses: StatusDefinition[];
  selectableStatuses: Status[];
  viewFilter: ViewFilter;
  setViewFilter: (
    update: ViewFilter | ((current: ViewFilter) => ViewFilter),
  ) => void;
  savedViewName: string;
  setSavedViewName: (name: string) => void;
  saveCurrentView: () => void;
  applySavedView: (view: SavedView) => void;
  removeSavedView: (id: string) => void;
}

export function GraphToolsPanel({
  graph,
  customStatuses,
  selectableStatuses,
  viewFilter,
  setViewFilter,
  savedViewName,
  setSavedViewName,
  saveCurrentView,
  applySavedView,
  removeSavedView,
}: GraphToolsPanelProps) {
  const { t } = useI18n();
  return (
  <section className="graph-tools-panel" aria-label={t("graphTools.aria")}>
    <input
      value={viewFilter.query}
      onChange={(event) =>
        setViewFilter((current) => ({ ...current, query: event.target.value }))
      }
      placeholder={t("graphTools.query")}
    />
    <select
      value={viewFilter.status}
      onChange={(event) =>
        setViewFilter((current) => ({ ...current, status: event.target.value }))
      }
      aria-label={t("graphTools.status")}
    >
      <option value="">{t("graphTools.allStatuses")}</option>
      {selectableStatuses.map((status) => (
        <option key={status} value={status}>
          {localizedStatusLabel(status, customStatuses)}
        </option>
      ))}
    </select>
    <select
      value={viewFilter.assignee}
      onChange={(event) =>
        setViewFilter((current) => ({ ...current, assignee: event.target.value }))
      }
      aria-label={t("graphTools.assignee")}
    >
      <option value="">{t("graphTools.allAssignees")}</option>
      <option value="__unassigned__">{t("graphTools.unassigned")}</option>
      {(graph?.users ?? []).map((user) => (
        <option key={user.id} value={user.id}>
          {user.name}
        </option>
      ))}
    </select>
    <select
      value={viewFilter.tag}
      onChange={(event) =>
        setViewFilter((current) => ({ ...current, tag: event.target.value }))
      }
      aria-label={t("graphTools.tag")}
    >
      <option value="">{t("graphTools.allTags")}</option>
      {[...new Set((graph?.nodes ?? []).flatMap((node) => node.tags ?? []))]
        .sort()
        .map((tag) => (
          <option key={tag} value={tag}>
            {tag}
          </option>
        ))}
    </select>
    <select
      value={viewFilter.priority}
      onChange={(event) =>
        setViewFilter((current) => ({ ...current, priority: event.target.value }))
      }
      aria-label={t("graphTools.priority")}
    >
      <option value="">{t("graphTools.allPriorities")}</option>
      <option value="urgent">{t("graphTools.urgent")}</option>
      <option value="high">{t("graphTools.high")}</option>
      <option value="medium">{t("graphTools.medium")}</option>
      <option value="low">{t("graphTools.low")}</option>
    </select>
    <select
      value={viewFilter.sort}
      onChange={(event) =>
        setViewFilter((current) => ({
          ...current,
          sort: event.target.value as typeof current.sort,
        }))
      }
      aria-label={t("graphTools.sort")}
    >
      <option value="manual">{t("graphTools.manual")}</option>
      <option value="title">{t("graphTools.title")}</option>
      <option value="priority">{t("graphTools.prioritySort")}</option>
      <option value="status">{t("graphTools.statusSort")}</option>
      <option value="assignee">{t("graphTools.assigneeSort")}</option>
    </select>
    <button
      type="button"
      onClick={() =>
        setViewFilter({
          query: "",
          status: "",
          assignee: "",
          tag: "",
          priority: "",
          sort: "manual",
        })
      }
    >
      {t("graphTools.clear")}
    </button>
    <span className="saved-view-create">
      <input
        value={savedViewName}
        onChange={(event) => setSavedViewName(event.target.value)}
        placeholder={t("graphTools.viewName")}
        maxLength={80}
      />
      <button type="button" disabled={!savedViewName.trim()} onClick={saveCurrentView}>
        {t("graphTools.saveView")}
      </button>
    </span>
    {(graph?.ui?.savedViews ?? []).map((view) => (
      <span className="saved-view-chip" key={view.id}>
        <button type="button" onClick={() => applySavedView(view)}>
          {view.name}
        </button>
        <button type="button" aria-label={t("graphTools.deleteView", { name: view.name })} onClick={() => removeSavedView(view.id)}>
          ×
        </button>
      </span>
    ))}
  </section>
  );
}
