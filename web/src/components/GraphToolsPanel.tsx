/** Graph filter and saved-view panel [B-06]. */

import { statusTheme } from "../statusTheme";
import type { Graph, SavedView, Status, StatusDefinition } from "../types";

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
  return (
  <section className="graph-tools-panel" aria-label="篩選與儲存檢視">
    <input
      value={viewFilter.query}
      onChange={(event) =>
        setViewFilter((current) => ({ ...current, query: event.target.value }))
      }
      placeholder="篩選 ID、標題、標籤、負責人"
    />
    <select
      value={viewFilter.status}
      onChange={(event) =>
        setViewFilter((current) => ({ ...current, status: event.target.value }))
      }
      aria-label="狀態篩選"
    >
      <option value="">全部狀態</option>
      {selectableStatuses.map((status) => (
        <option key={status} value={status}>
          {statusTheme(status, customStatuses).label}
        </option>
      ))}
    </select>
    <select
      value={viewFilter.assignee}
      onChange={(event) =>
        setViewFilter((current) => ({ ...current, assignee: event.target.value }))
      }
      aria-label="負責人篩選"
    >
      <option value="">全部負責人</option>
      <option value="__unassigned__">尚未指派</option>
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
      aria-label="標籤篩選"
    >
      <option value="">全部標籤</option>
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
      aria-label="優先度篩選"
    >
      <option value="">全部優先度</option>
      <option value="urgent">緊急</option>
      <option value="high">高</option>
      <option value="medium">中</option>
      <option value="low">低</option>
    </select>
    <select
      value={viewFilter.sort}
      onChange={(event) =>
        setViewFilter((current) => ({
          ...current,
          sort: event.target.value as typeof current.sort,
        }))
      }
      aria-label="排序方式"
    >
      <option value="manual">手動順序</option>
      <option value="title">標題</option>
      <option value="priority">優先度</option>
      <option value="status">狀態</option>
      <option value="assignee">負責人</option>
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
      清除
    </button>
    <span className="saved-view-create">
      <input
        value={savedViewName}
        onChange={(event) => setSavedViewName(event.target.value)}
        placeholder="檢視名稱"
        maxLength={80}
      />
      <button type="button" disabled={!savedViewName.trim()} onClick={saveCurrentView}>
        儲存檢視
      </button>
    </span>
    {(graph?.ui?.savedViews ?? []).map((view) => (
      <span className="saved-view-chip" key={view.id}>
        <button type="button" onClick={() => applySavedView(view)}>
          {view.name}
        </button>
        <button type="button" aria-label={`刪除檢視 ${view.name}`} onClick={() => removeSavedView(view.id)}>
          ×
        </button>
      </span>
    ))}
  </section>
  );
}
