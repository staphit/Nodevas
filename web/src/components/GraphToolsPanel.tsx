/**
 * Graph filter and saved-view panel [B-06].
 *
 * Lives in the board header's Filter popover. Status is what a board is most
 * often narrowed by, so its checkboxes and "Clear filters" come first; search,
 * assignee, tag, priority, sort and the saved views wait under "More filters",
 * which opens by itself when one of them is already on. The trigger shows how
 * many filters are on, so a dimmed board always says why.
 */

import { useId, useState } from "react";

import type { Graph, SavedView, Status, StatusDefinition } from "../types";
import { localizedStatusLabel, useI18n } from "../i18n";

export interface ViewFilter {
  query: string;
  /** Statuses shown; empty shows every status. */
  statuses: string[];
  assignee: string;
  tag: string;
  priority: string;
  sort: "manual" | "title" | "priority" | "status" | "assignee";
}

export const EMPTY_VIEW_FILTER: ViewFilter = {
  query: "",
  statuses: [],
  assignee: "",
  tag: "",
  priority: "",
  sort: "manual",
};

/** Filters that hide or dim nodes. Sorting reorders and is not counted. */
export function activeFilterCount(filter: ViewFilter): number {
  return [
    filter.query.trim(),
    filter.statuses.length > 0,
    filter.assignee,
    filter.tag,
    filter.priority,
  ].filter(Boolean).length;
}

/** Whether anything under "More filters" is set, which opens it. */
function moreFiltersActive(filter: ViewFilter): boolean {
  return Boolean(
    filter.query.trim() ||
      filter.assignee ||
      filter.tag ||
      filter.priority ||
      filter.sort !== "manual",
  );
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
  const moreId = useId();
  // Decided when the popover opens: a filter already on under the fold is
  // shown, otherwise the fold stays shut until asked for.
  const [moreOpen, setMoreOpen] = useState(() => moreFiltersActive(viewFilter));

  const toggleStatus = (status: string, on: boolean) =>
    setViewFilter((current) => ({
      ...current,
      statuses: on
        ? [...current.statuses.filter((value) => value !== status), status]
        : current.statuses.filter((value) => value !== status),
    }));

  return (
  <section className="graph-tools-panel" aria-label={t("graphTools.aria")}>
    <fieldset className="graph-tools-statuses">
      <legend>{t("graphTools.status")}</legend>
      {selectableStatuses.map((status) => (
        <label key={status} className="graph-tools-status">
          <input
            type="checkbox"
            checked={viewFilter.statuses.includes(status)}
            onChange={(event) => toggleStatus(status, event.target.checked)}
          />
          <span>{localizedStatusLabel(status, customStatuses)}</span>
        </label>
      ))}
    </fieldset>
    <button type="button" onClick={() => setViewFilter(EMPTY_VIEW_FILTER)}>
      {t("graphTools.clearFilters")}
    </button>
    <button
      type="button"
      className="graph-tools-more"
      aria-expanded={moreOpen}
      aria-controls={moreId}
      onClick={() => setMoreOpen((open) => !open)}
    >
      <span aria-hidden>{moreOpen ? "▾" : "▸"}</span> {t("graphTools.moreFilters")}
    </button>
    <div id={moreId} className="graph-tools-more-fields" hidden={!moreOpen}>
      <input
        value={viewFilter.query}
        onChange={(event) =>
          setViewFilter((current) => ({ ...current, query: event.target.value }))
        }
        placeholder={t("graphTools.query")}
        aria-label={t("graphTools.query")}
      />
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
      <span className="saved-view-create">
        <input
          value={savedViewName}
          onChange={(event) => setSavedViewName(event.target.value)}
          placeholder={t("graphTools.viewName")}
          aria-label={t("graphTools.viewName")}
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
    </div>
  </section>
  );
}
