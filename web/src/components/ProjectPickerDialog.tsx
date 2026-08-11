/**
 * Project picker [B-06].
 *
 * Imperative like {@link confirmAction}, because the callers are a toolbar
 * button and a keyboard shortcut — neither of which owns a place to hang
 * dialog state.
 */

import { useEffect, useMemo, useRef, useState } from "react";
import { IconClose } from "../icons";
import { useApp } from "../store";

export interface ProjectPickerOptions {
  title: string;
  description: string;
  confirmLabel?: string;
  /** Projects that must not be offered — usually the one we are copying from. */
  exclude?: string[];
  /**
   * Offer grouping folders as well as projects.
   *
   * Off by default because the original caller is picking somewhere to write
   * nodes, and a folder has no graph.yaml to write them into. A caller picking
   * a *parent* — moving a project — wants the opposite: a folder is the most
   * likely answer.
   */
  includeFolders?: boolean;
  /**
   * When set, the workspace root is offered as a first option under this label,
   * and choosing it resolves ".". The root is not in the project list — it is
   * not a project — so a caller that can accept it has to ask for it.
   */
  rootLabel?: string;
}

type ProjectPickerRequest = ProjectPickerOptions & {
  resolve: (project: string | null) => void;
};

let showPicker: ((request: ProjectPickerRequest) => void) | null = null;

/** Resolves with the chosen project name, or null if the user backed out. */
export function pickProject(options: ProjectPickerOptions): Promise<string | null> {
  if (!showPicker) return Promise.resolve(null);
  return new Promise((resolve) => showPicker?.({ ...options, resolve }));
}

export function ProjectPickerHost() {
  const [request, setRequest] = useState<ProjectPickerRequest | null>(null);
  const [selected, setSelected] = useState("");
  const [filter, setFilter] = useState("");
  const projects = useApp((state) => state.projects);
  const refreshProjects = useApp((state) => state.refreshProjects);
  const dialogRef = useRef<HTMLElement>(null);
  const filterRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    showPicker = (next) => {
      setSelected("");
      setFilter("");
      setRequest(next);
    };
    return () => {
      showPicker = null;
    };
  }, []);

  // A project may have been created in another window since the last refresh.
  useEffect(() => {
    if (request) void refreshProjects().catch(() => undefined);
  }, [request, refreshProjects]);

  useEffect(() => {
    if (!request) return;
    filterRef.current?.focus();
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        event.preventDefault();
        request.resolve(null);
        setRequest(null);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [request]);

  const excluded = useMemo(
    () => new Set(request?.exclude ?? []),
    [request?.exclude],
  );
  // Grouping folders have no graph.yaml, so nothing can be written into them —
  // which is why they are left out unless the caller says it is picking a
  // parent rather than a destination for content.
  const includeFolders = request?.includeFolders ?? false;
  const options = useMemo(() => {
    const needle = filter.trim().toLowerCase();
    return projects.filter(
      (project) =>
        (includeFolders || !project.isFolder) &&
        !excluded.has(project.name) &&
        (needle === "" ||
          project.label.toLowerCase().includes(needle) ||
          project.name.toLowerCase().includes(needle)),
    );
  }, [projects, excluded, includeFolders, filter]);

  if (!request) return null;

  const close = (project: string | null) => {
    request.resolve(project);
    setRequest(null);
  };

  return (
    <div
      className="confirm-backdrop"
      onMouseDown={(event) => {
        if (event.target === event.currentTarget) close(null);
      }}
    >
      <section
        ref={dialogRef}
        className="confirm-dialog project-picker-dialog"
        role="dialog"
        aria-modal="true"
        aria-labelledby="project-picker-title"
      >
        <header>
          <div>
            <h2 id="project-picker-title">{request.title}</h2>
            <p>{request.description}</p>
          </div>
          <button
            type="button"
            className="confirm-dialog-close"
            onClick={() => close(null)}
            aria-label="關閉"
          >
            <IconClose size={15} />
          </button>
        </header>
        <div className="project-picker-body">
          <input
            ref={filterRef}
            type="search"
            placeholder="搜尋專案…"
            value={filter}
            onChange={(event) => setFilter(event.target.value)}
          />
          <ul className="project-picker-list">
            {options.length === 0 && !request.rootLabel && (
              <li className="project-picker-empty">沒有可用的專案</li>
            )}
            {/* Pinned above the filtered list rather than filtered with it: it
                is the answer often enough that hunting for it, or losing it to
                a search term that does not match its label, would be the
                annoying part of the dialog. */}
            {request.rootLabel && (
              <li>
                <button
                  type="button"
                  className={selected === "." ? "selected" : ""}
                  onClick={() => setSelected(".")}
                  onDoubleClick={() => close(".")}
                >
                  <span>{request.rootLabel}</span>
                  <small>最上層</small>
                </button>
              </li>
            )}
            {options.map((project) => (
              <li key={project.name}>
                <button
                  type="button"
                  className={project.name === selected ? "selected" : ""}
                  onClick={() => setSelected(project.name)}
                  onDoubleClick={() => close(project.name)}
                >
                  <span style={{ paddingLeft: `${project.depth * 12}px` }}>
                    {project.label}
                  </span>
                  <small>{project.isFolder ? "目錄" : `${project.nodes} 個節點`}</small>
                </button>
              </li>
            ))}
          </ul>
        </div>
        <footer>
          <button type="button" onClick={() => close(null)}>
            取消
          </button>
          <button
            type="button"
            className="primary"
            disabled={!selected}
            onClick={() => close(selected)}
          >
            {request.confirmLabel ?? "確認"}
          </button>
        </footer>
      </section>
    </div>
  );
}
