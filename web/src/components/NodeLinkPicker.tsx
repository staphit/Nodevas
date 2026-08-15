/**
 * Picks a node to link to [B-04].
 *
 * Searching runs over the whole workspace, because a link is allowed to cross
 * projects — that is the point of it. The result carries the project so the
 * inserted link stays correct no matter which project it is read from.
 */

import { useEffect, useRef, useState } from "react";
import { api } from "../api";
import { useApp } from "../store";
import { useI18n } from "../i18n";

export interface NodeLinkTarget {
  project: string;
  nodeId: string;
  title: string;
}

/**
 * The workspace index barely moves while someone is picking a link, so it is
 * kept for a short while instead of being fetched again every time the picker
 * opens. The in-flight promise is shared as well: several pickers mounting at
 * once ask the server once. A failure is not cached, so the next open retries.
 */
const indexFreshnessMs = 30_000;
let cachedIndex: { at: number; nodes: NodeLinkTarget[] } | null = null;
let indexRequest: Promise<NodeLinkTarget[]> | null = null;

function loadNodeIndex(): Promise<NodeLinkTarget[]> {
  if (cachedIndex && Date.now() - cachedIndex.at < indexFreshnessMs) {
    return Promise.resolve(cachedIndex.nodes);
  }
  if (!indexRequest) {
    indexRequest = api
      .getNodeIndex()
      .then((response) => {
        const nodes = response.nodes.map((node) => ({
          project: node.project,
          nodeId: node.nodeId,
          title: node.title || node.nodeId,
        }));
        cachedIndex = { at: Date.now(), nodes };
        return nodes;
      })
      .finally(() => {
        indexRequest = null;
      });
  }
  return indexRequest;
}

export function NodeLinkPicker({
  onPick,
  onClose,
  excludeNodeId,
}: {
  onPick: (target: NodeLinkTarget) => void;
  onClose: () => void;
  /** Usually the node being edited: a node linking to itself helps nobody. */
  excludeNodeId?: string;
}) {
  const { t } = useI18n();
  const activeProject = useApp((state) => state.activeProject);
  const [query, setQuery] = useState("");
  const [all, setAll] = useState<NodeLinkTarget[] | null>(null);
  const [highlighted, setHighlighted] = useState(0);
  const [failed, setFailed] = useState(false);
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    inputRef.current?.focus();
  }, []);

  // The whole workspace is loaded up front, so the list is complete before a
  // word is typed: a link is allowed to cross projects, and only offering the
  // open project hides most of what someone wants to link to.
  useEffect(() => {
    let cancelled = false;
    void loadNodeIndex()
      .then((nodes) => {
        if (cancelled) return;
        setAll(nodes);
      })
      .catch(() => {
        if (!cancelled) setFailed(true);
      });
    return () => {
      cancelled = true;
    };
  }, []);

  const normalized = query.trim().toLocaleLowerCase();
  const results = (all ?? [])
    .filter(
      (node) =>
        !(node.project === activeProject && node.nodeId === excludeNodeId),
    )
    .filter(
      (node) =>
        !normalized ||
        `${node.title} ${node.nodeId} ${node.project}`
          .toLocaleLowerCase()
          .includes(normalized),
    )
    // The open project first: linking to a sibling is the common case.
    .sort((left, right) => {
      const byProject =
        Number(right.project === activeProject) -
        Number(left.project === activeProject);
      return byProject || left.project.localeCompare(right.project, "zh-Hant");
    })
    .slice(0, 200);

  useEffect(() => setHighlighted(0), [query, all]);

  const choose = (index: number) => {
    const target = results[index];
    if (target) onPick(target);
  };

  return (
    <div className="confirm-backdrop" onClick={onClose}>
      <div
        className="confirm-dialog node-link-picker"
        role="dialog"
        aria-modal="true"
        aria-label={t("nodeLink.title")}
        onClick={(event) => event.stopPropagation()}
      >
        <div className="settings-head">
          <div>
            <b>{t("nodeLink.title")}</b>
            <small>{t("nodeLink.hint")}</small>
          </div>
        </div>
        <input
          ref={inputRef}
          value={query}
          placeholder={t("nodeLink.searchPlaceholder")}
          aria-label={t("nodeLink.searchAria")}
          onChange={(event) => setQuery(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === "Escape") {
              event.preventDefault();
              onClose();
              return;
            }
            if (event.key === "ArrowDown") {
              event.preventDefault();
              setHighlighted((index) => Math.min(index + 1, results.length - 1));
              return;
            }
            if (event.key === "ArrowUp") {
              event.preventDefault();
              setHighlighted((index) => Math.max(index - 1, 0));
              return;
            }
            if (event.key === "Enter") {
              event.preventDefault();
              choose(highlighted);
            }
          }}
        />
        <div className="node-link-results" role="listbox">
          {results.length === 0 && (
            <p className="settings-hint">
              {failed
                ? t("nodeLink.loadFailed")
                : all === null
                  ? t("common.loading")
                  : t("nodeLink.noResults")}
            </p>
          )}
          {results.map((result, index) => (
            <button
              key={`${result.project}/${result.nodeId}`}
              type="button"
              role="option"
              aria-selected={index === highlighted}
              className={index === highlighted ? "selected" : ""}
              onMouseEnter={() => setHighlighted(index)}
              onClick={() => choose(index)}
            >
              <b>{result.title}</b>
              <small>
                {result.project === activeProject
                  ? result.nodeId
                  : `${result.project} · ${result.nodeId}`}
              </small>
            </button>
          ))}
        </div>
      </div>
    </div>
  );
}
