/**
 * The copilot's open draft on the board: ghost nodes where the Create would
 * put them, and dashed lines for what each waits for.
 *
 * Built like PeerOverlay: its own subscription to the copilot's draft, so an
 * edit in the panel redraws this layer and no card. Ghosts are never part of
 * the graph; they take no pointer events except their link handle, which is
 * dragged onto another ghost or a real card to make this step wait for it.
 * The cells it lays out are published (useDraftUi.ghost) so the Create sends
 * exactly them.
 */

import { useEffect, useMemo, useState, type PointerEvent as ReactPointerEvent } from "react";
import { useI18n } from "../../i18n";
import { canvasSelectionIds } from "../../state/documentSlice";
import { useApp } from "../../store";
import { activeDraftId, currentDraft, useCopilot } from "../../copilot/copilotStore";
import { editDraft } from "../../copilot/draft/draftApi";
import { layoutDraft, occupiedOf, waitedForIds, type Cell } from "../../copilot/draft/draftLayout";
import { addAfter, existingIdOf, existingRef, isExistingRef } from "../../copilot/draft/draftStore";
import { useDraftUi } from "../../copilot/draft/draftUi";
import { CARD_H, CARD_W, ROW_H, contentPointFromClient, type Col, type Point, type Rect } from "./geometry";

interface GhostBox {
  key: string;
  title: string;
  rect: Rect;
}

export function DraftOverlay({
  colOf,
  centerX,
  rowTop,
  zoom,
}: {
  colOf: Map<string, Col>;
  centerX: (column: Col) => number;
  rowTop: (row: number) => number;
  zoom: number;
}) {
  const { t } = useI18n();
  const entryId = useCopilot((state) => activeDraftId(state));
  const draft = useCopilot((state) => (entryId ? currentDraft(state, entryId) : null));
  const selection = useApp(canvasSelectionIds);
  // The draft goes where the selection was when it arrived (else beside
  // the cards it waits for then); clicking around or linking a ghost to a
  // card afterwards does not move it.
  const anchorIds = useMemo(
    () => (selection.length > 0 ? selection : draft ? waitedForIds(draft) : []),
    // eslint-disable-next-line react-hooks/exhaustive-deps -- fixed per draft
    [entryId],
  );

  const occupied = useMemo(() => occupiedOf(colOf.values()), [colOf]);
  const cells = useMemo<Record<string, Cell>>(
    () =>
      draft ? layoutDraft(draft, { occupied, selection: anchorIds, followRefs: false }) : {},
    [draft, occupied, anchorIds],
  );

  useEffect(() => {
    useDraftUi.getState().setGhost(entryId && draft ? { entryId, cells } : null);
  }, [entryId, draft, cells]);
  useEffect(() => () => useDraftUi.getState().setGhost(null), []);

  const ghosts = useMemo<GhostBox[]>(() => {
    if (!draft) return [];
    return draft.nodes.flatMap((node) => {
      const cell = cells[node.key];
      if (!cell) return [];
      const column = { index: cell.col, row: cell.row, width: CARD_W, height: CARD_H } as Col;
      const left = centerX(column) - CARD_W / 2;
      const top = rowTop(cell.row) + ROW_H / 2 - CARD_H / 2;
      return [{ key: node.key, title: node.title, rect: { left, top, right: left + CARD_W, bottom: top + CARD_H } }];
    });
  }, [draft, cells, centerX, rowTop]);

  const realRect = (id: string): Rect | null => {
    const column = colOf.get(id);
    if (!column) return null;
    const left = centerX(column) - column.width / 2;
    const top = rowTop(column.row) + ROW_H / 2 - column.height / 2;
    return { left, top, right: left + column.width, bottom: top + column.height };
  };

  const [link, setLink] = useState<{ from: string; start: Point; end: Point } | null>(null);

  if (!entryId || !draft || ghosts.length === 0) return null;

  const ghostByKey = new Map(ghosts.map((ghost) => [ghost.key, ghost]));
  const rectOf = (ref: string): Rect | null =>
    isExistingRef(ref) ? realRect(existingIdOf(ref)) : (ghostByKey.get(ref)?.rect ?? null);

  /** The ghost or card under a board point, as a draft reference. */
  const targetAt = (point: Point, from: string): string | null => {
    const inside = (rect: Rect) =>
      point.x >= rect.left && point.x <= rect.right && point.y >= rect.top && point.y <= rect.bottom;
    for (const ghost of ghosts) if (ghost.key !== from && inside(ghost.rect)) return ghost.key;
    for (const [id] of colOf) {
      const rect = realRect(id);
      if (rect && inside(rect)) return existingRef(id);
    }
    return null;
  };

  const startLink = (event: ReactPointerEvent<HTMLElement>, key: string) => {
    if (event.pointerType === "mouse" && event.button !== 0) return;
    // The board would start a marquee or a pan.
    event.stopPropagation();
    event.preventDefault();
    const inner = event.currentTarget.closest<HTMLElement>(".board-inner");
    const toBoard = (clientX: number, clientY: number): Point =>
      inner
        ? contentPointFromClient(clientX, clientY, inner.getBoundingClientRect(), zoom)
        : { x: clientX, y: clientY };
    const ghost = ghostByKey.get(key)!;
    const start = { x: ghost.rect.left, y: (ghost.rect.top + ghost.rect.bottom) / 2 };
    setLink({ from: key, start, end: toBoard(event.clientX, event.clientY) });
    const move = (moveEvent: PointerEvent) => {
      setLink({ from: key, start, end: toBoard(moveEvent.clientX, moveEvent.clientY) });
    };
    const up = (upEvent: PointerEvent) => {
      window.removeEventListener("pointermove", move);
      window.removeEventListener("pointerup", up);
      window.removeEventListener("pointercancel", cancel);
      setLink(null);
      const target = targetAt(toBoard(upEvent.clientX, upEvent.clientY), key);
      if (target) editDraft(entryId, (current) => addAfter(current, key, target));
    };
    const cancel = () => {
      window.removeEventListener("pointermove", move);
      window.removeEventListener("pointerup", up);
      window.removeEventListener("pointercancel", cancel);
      setLink(null);
    };
    window.addEventListener("pointermove", move);
    window.addEventListener("pointerup", up);
    window.addEventListener("pointercancel", cancel);
  };

  // A dependency's line: from the right edge of what finishes first to the
  // left edge of what waits.
  const edges = draft.nodes.flatMap((node) => {
    const to = ghostByKey.get(node.key);
    if (!to) return [];
    return (node.after ?? []).flatMap((ref) => {
      const from = rectOf(ref);
      if (!from) return [];
      const x1 = from.right;
      const y1 = (from.top + from.bottom) / 2;
      const x2 = to.rect.left;
      const y2 = (to.rect.top + to.rect.bottom) / 2;
      const bend = Math.max(24, Math.abs(x2 - x1) / 2);
      return [
        {
          id: `${ref}->${node.key}`,
          d: `M ${x1} ${y1} C ${x1 + bend} ${y1}, ${x2 - bend} ${y2}, ${x2} ${y2}`,
        },
      ];
    });
  });

  return (
    <>
      <svg className="draft-edges" width={1} height={1} aria-hidden>
        {edges.map((edge) => (
          <path key={edge.id} d={edge.d} data-draft-edge={edge.id} />
        ))}
        {link && (
          <path
            className="draft-link-preview"
            d={`M ${link.start.x} ${link.start.y} L ${link.end.x} ${link.end.y}`}
          />
        )}
      </svg>
      {ghosts.map((ghost) => (
        <div
          key={ghost.key}
          className="draft-ghost"
          data-draft-key={ghost.key}
          title={t("draft.ghost", { title: ghost.title })}
          style={{
            left: ghost.rect.left,
            top: ghost.rect.top,
            width: CARD_W,
            height: CARD_H,
          }}
        >
          <span className="draft-ghost-title">{ghost.title}</span>
          <button
            type="button"
            className="draft-ghost-handle"
            aria-label={`${ghost.title} · ${t("draft.handle")}`}
            title={t("draft.handle")}
            onPointerDown={(event) => startLink(event, ghost.key)}
          />
        </div>
      ))}
    </>
  );
}
