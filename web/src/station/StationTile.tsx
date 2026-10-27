// A Station item as a small card, the shape of a node card on the board:
// status dot and title on the first line, the node's type label and one
// short line of what is going on under it, and its actions in the corner.
// Tiles sit in a grid (StationTiles) of as many columns as fit, so nothing
// stretches across a wide window.

import type { ReactNode } from "react";

import { nodeTypeById } from "../domain/graph/nodeTypes";
import { NodeTypeChip } from "../nodeTypeTheme";
import { useApp } from "../store";

export function StationTiles({ label, className, children }: { label: string; className?: string; children: ReactNode }) {
  return (
    <ul className={`station-tiles${className ? ` ${className}` : ""}`} aria-label={label}>
      {children}
    </ul>
  );
}

export function StationTile({
  dot,
  title,
  nodeId,
  meta,
  badge,
  action,
  onOpen,
  className,
  children,
}: {
  /** The dot's status class: a run's status, "running", "paused", "off". */
  dot: string;
  title: string;
  /** The node the tile is about: its type's label chip. */
  nodeId?: string;
  /** One short line: "Waiting on Team sync", "2 of 5 tasks done". */
  meta?: string;
  /** A status word, after the meta line. */
  badge?: ReactNode;
  /** The corner: a ⋯ menu. */
  action?: ReactNode;
  /** The title opens this, when the tile has no menu to say it. */
  onOpen?: () => void;
  /** Extra classes on the tile. */
  className?: string;
  /** Anything else, under the meta line (a progress bar, an error, buttons). */
  children?: ReactNode;
}) {
  const graph = useApp((s) => s.graph);
  const kind = nodeId ? graph?.nodes?.find((node) => node.id === nodeId)?.kind : undefined;
  const type = nodeId ? nodeTypeById(graph, kind) : null;
  return (
    <li className={`station-tile dot-${dot}${className ? ` ${className}` : ""}`}>
      <div className="col-card-head">
        <span className={`station-dot ${dot === "off" ? "is-off" : `status-${dot}`}`} aria-hidden="true" />
        {onOpen ? (
          <button type="button" className="col-card-title station-tile-title is-link" onClick={onOpen} title={title}>
            {title}
          </button>
        ) : (
          <span className="col-card-title station-tile-title" title={title}>
            {title}
          </span>
        )}
        {action}
      </div>
      {(type || meta || badge) && (
        <div className="col-card-meta station-tile-meta">
          {type && <NodeTypeChip type={type} />}
          {meta && (
            <span className="station-tile-line" title={meta}>
              {meta}
            </span>
          )}
          {badge}
        </div>
      )}
      {children}
    </li>
  );
}
