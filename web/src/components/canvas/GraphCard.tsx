/**
 * One node card on the graph board, with its ⋯ button and resize frame.
 *
 * Memoized, and handed only values that are equal from one board render to the
 * next unless something about this card changed: everything is resolved to
 * strings and numbers by GraphCanvas, and the event handlers come in one
 * object that never changes identity. A board render — a selection, a drag
 * lifting, a hover — therefore re-renders the cards it touches and skips the
 * rest, which on a few hundred cards is most of the cost.
 */

import { Fragment, memo } from "react";
import { StatusShape } from "../../statusTheme";
import { useI18n } from "../../i18n";
import type { GraphNode, NodeType, Status, StatusDefinition } from "../../types";
import { RunCardBadge } from "../../runs/RunChanges";
import { AutopilotCardBadge } from "../../autopilot/AutopilotCardBadge";
import { NodeTypeChip } from "../../nodeTypeTheme";
import { CARD_HANDLES, CARD_MAX_H, CARD_MAX_W, CARD_MIN_H, CARD_MIN_W } from "./geometry";

/**
 * Stable entry points into the board's handlers, keyed by node id. GraphCanvas
 * builds this once and forwards each call to whatever the latest render had.
 */
export interface CardEvents {
  pointerDown: (event: React.PointerEvent<HTMLElement>, id: string) => void;
  pointerMove: (event: React.PointerEvent<HTMLElement>) => void;
  pointerUp: (event: React.PointerEvent<HTMLElement>, id: string) => void;
  pointerCancel: (event: React.PointerEvent<HTMLElement>) => void;
  pointerEnter: (id: string) => void;
  pointerLeave: (id: string) => void;
  lostPointerCapture: (id: string) => void;
  focus: (id: string) => void;
  keyDown: (event: React.KeyboardEvent<HTMLElement>, id: string) => void;
  contextMenu: (event: React.MouseEvent<HTMLElement>, id: string) => void;
  moreClick: (event: React.MouseEvent<HTMLElement>, id: string) => void;
  resizePointerDown: (
    event: React.PointerEvent<HTMLElement>,
    id: string,
    dirX: number,
    dirY: number,
    width: number,
    height: number,
  ) => void;
  resizePointerMove: (event: React.PointerEvent<HTMLElement>) => void;
  resizePointerUp: (event: React.PointerEvent<HTMLElement>) => void;
  resizePointerCancel: (event: React.PointerEvent<HTMLElement>) => void;
  resizeKeyDown: (
    event: React.KeyboardEvent<HTMLElement>,
    id: string,
    dirX: number,
    dirY: number,
    width: number,
    height: number,
  ) => void;
}

export interface GraphCardProps {
  node: GraphNode;
  left: number;
  top: number;
  width: number;
  height: number;
  className: string;
  cardBg: string | undefined;
  borderColor: string | undefined;
  typeColor: string | undefined;
  textColor: string | undefined;
  /** Lifted under the local pointer: offset by the board's live drag vars. */
  dragging: boolean;
  hasStatus: boolean;
  status: Status;
  customStatuses: StatusDefinition[];
  nodeType: NodeType;
  assigned: string;
  markerKind: string | undefined;
  markerLabel: string | undefined;
  agentName: string | undefined;
  agentKind: string | undefined;
  ariaLabel: string;
  tooltip: string;
  selected: boolean;
  events: CardEvents;
}

/**
 * Where a lifted card is drawn. The offset changes every frame of a drag and
 * is written to the board as two custom properties, so the card itself only
 * renders when it is picked up and when it is put down.
 */
export const LIFTED_TRANSFORM = "translate(var(--drag-dx, 0px), var(--drag-dy, 0px))";

export const GraphCard = memo(function GraphCard({
  node: n,
  left,
  top,
  width,
  height,
  className,
  cardBg,
  borderColor,
  typeColor,
  textColor,
  dragging,
  hasStatus,
  status,
  customStatuses,
  nodeType,
  assigned,
  markerKind,
  markerLabel,
  agentName,
  agentKind,
  ariaLabel,
  tooltip,
  selected,
  events,
}: GraphCardProps) {
  const { t } = useI18n();
  const title = n.title || n.id;
  return (
    <Fragment>
      <div
        data-node-id={n.id}
        role="button"
        tabIndex={0}
        className={className}
        style={{
          left,
          top,
          width,
          height,
          // Shape insets are computed from these.
          ["--card-w" as string]: `${width}px`,
          ["--card-h" as string]: `${height}px`,
          // Fill and outline travel as variables: a clipped shape
          // draws its own edge and cannot use the box border.
          ["--card-bg" as string]: cardBg,
          ["--card-border-color" as string]: borderColor,
          ["--card-type-color" as string]: typeColor || undefined,
          borderColor,
          color: textColor,
          transform: dragging ? LIFTED_TRANSFORM : undefined,
        }}
        onPointerDown={(event) => events.pointerDown(event, n.id)}
        onPointerMove={events.pointerMove}
        onPointerUp={(event) => events.pointerUp(event, n.id)}
        onPointerCancel={events.pointerCancel}
        onPointerEnter={() => events.pointerEnter(n.id)}
        onPointerLeave={() => events.pointerLeave(n.id)}
        onFocus={() => events.focus(n.id)}
        onKeyDown={(event) => events.keyDown(event, n.id)}
        onContextMenu={(event) => events.contextMenu(event, n.id)}
        onLostPointerCapture={() => events.lostPointerCapture(n.id)}
        aria-selected={selected}
        aria-label={ariaLabel}
        title={tooltip}
      >
        <span className="col-card-head">
          {hasStatus && <StatusShape status={status} definitions={customStatuses} />}
          <span className="col-card-title">{title}</span>
          <RunCardBadge nodeId={n.id} />
          <AutopilotCardBadge nodeId={n.id} />
        </span>
        <span className="col-card-meta">
          <NodeTypeChip type={nodeType} />
          <span
            className={`col-card-assignee${n.assignee ? "" : " unassigned"}`}
            data-assignee-prefix={t("canvas.assigneePrefix")}
          >
            {assigned}
          </span>
          {markerKind && (
            <span className={`col-card-marker ${markerKind}`}>{markerLabel}</span>
          )}
        </span>
        {agentName !== undefined && (
          <span className="col-card-agent" title={`${agentName} (${agentKind})`}>
            ● {agentName}
          </span>
        )}
      </div>
      {/* The menu for a pointer that does not right-click: a sibling laid
          over the card's corner rather than a child, so no control sits
          inside the card's own button. Shown on hover, focus or
          selection (04-board.css), and stopped before the board can read
          its press as the start of a pan or a marquee. */}
      <button
        type="button"
        className="col-card-more"
        style={{ left: left + width, top }}
        aria-label={t("board.nodeMenu.more", { title })}
        title={t("board.nodeMenu.more", { title })}
        aria-haspopup="menu"
        onPointerDown={(event) => event.stopPropagation()}
        onKeyDown={(event) => event.stopPropagation()}
        onClick={(event) => events.moreClick(event, n.id)}
      >
        ⋯
      </button>
      {selected && (
        // Sibling of the card: a clipped shape would otherwise clip
        // its own handles away.
        <div className="card-resize-frame" style={{ left, top, width, height }}>
          {CARD_HANDLES.map((handle) => (
            <span
              key={handle.name}
              className={`col-card-handle ${handle.name}`}
              role="slider"
              tabIndex={0}
              aria-label={t("canvas.resizeCard", {
                handle: t(`canvas.handle.${handle.name}`),
                title,
              })}
              aria-valuemin={handle.dirX ? CARD_MIN_W : CARD_MIN_H}
              aria-valuemax={handle.dirX ? CARD_MAX_W : CARD_MAX_H}
              aria-valuenow={Math.round(handle.dirX ? width : height)}
              aria-valuetext={`${Math.round(width)}×${Math.round(height)}`}
              title={t("canvas.resizeCardTitle")}
              onPointerDown={(event) =>
                events.resizePointerDown(event, n.id, handle.dirX, handle.dirY, width, height)
              }
              onPointerMove={events.resizePointerMove}
              onPointerUp={events.resizePointerUp}
              onPointerCancel={events.resizePointerCancel}
              onKeyDown={(event) =>
                events.resizeKeyDown(event, n.id, handle.dirX, handle.dirY, width, height)
              }
            />
          ))}
        </div>
      )}
    </Fragment>
  );
});
