/**
 * The copilot's open draft on the board: ghost nodes where the Create would
 * put them, and dashed lines for what each waits for.
 *
 * Built like PeerOverlay: its own subscription to the copilot's draft, so an
 * edit in the panel redraws this layer and no card. Ghosts are never part of
 * the graph. A ghost takes the pointer only for itself: a press on it starts
 * no marquee and no pan, a double-click renames it, and its link handle is
 * dragged onto another ghost or a real card to make this step wait for it.
 * With a theme pack on, a ghost is a picture card (the kind's glyph, large,
 * in the skin's colours, and the title only); a click shows its body and what
 * it waits for. The cells it lays out are published (useDraftUi.ghost) so the
 * Create sends exactly them.
 */

import {
  useEffect,
  useMemo,
  useRef,
  useState,
  type KeyboardEvent as ReactKeyboardEvent,
  type PointerEvent as ReactPointerEvent,
} from "react";
import { nodeTypeById } from "../../domain/graph/nodeTypes";
import { useI18n } from "../../i18n";
import { canvasSelectionIds } from "../../state/documentSlice";
import { useApp } from "../../store";
import { activeDraftId, currentDraft, isPlanDraft, isRefining, useCopilot } from "../../copilot/copilotStore";
import { editDraft } from "../../copilot/draft/draftApi";
import { useThemeActive } from "../../copilot/draft/DraftCard";
import { KindGlyph } from "../../copilot/draft/DraftList";
import { layoutDraft, occupiedOf, waitedForIds, type Cell } from "../../copilot/draft/draftLayout";
import {
  addAfter,
  existingIdOf,
  existingRef,
  isExistingRef,
  MAX_PLAN_TITLE_CHARS,
  MAX_TITLE_CHARS,
  renameNode,
} from "../../copilot/draft/draftStore";
import { useDraftUi } from "../../copilot/draft/draftUi";
import { CARD_H, CARD_W, ROW_H, contentPointFromClient, type Col, type Point, type Rect } from "./geometry";

interface GhostBox {
  key: string;
  title: string;
  kind: string;
  body: string;
  after: string[];
  rect: Rect;
}

/** The ghost's title as a text box: Enter or leaving it renames, Escape
 * keeps the title. */
function GhostRename({
  title,
  label,
  maxLength,
  onDone,
}: {
  title: string;
  label: string;
  maxLength: number;
  onDone: (title: string | null) => void;
}) {
  const [value, setValue] = useState(title);
  // Once: the blur that follows an Enter or an Escape is not a second answer.
  const done = useRef(false);
  const finish = (answer: string | null) => {
    if (done.current) return;
    done.current = true;
    onDone(answer);
  };
  const onKeyDown = (event: ReactKeyboardEvent<HTMLInputElement>) => {
    if (event.nativeEvent.isComposing) return;
    if (event.key === "Enter") {
      event.preventDefault();
      finish(value);
    } else if (event.key === "Escape") {
      // The board and the panel act on Escape; here it only ends the rename.
      event.preventDefault();
      event.stopPropagation();
      finish(null);
    }
  };
  return (
    <input
      className="draft-ghost-input"
      aria-label={label}
      value={value}
      maxLength={maxLength}
      autoFocus
      onChange={(event) => setValue(event.target.value)}
      onBlur={() => finish(value)}
      onKeyDown={onKeyDown}
    />
  );
}

/** A picture card's body and what it waits for, under the card. */
function GhostDetail({
  ghost,
  titleOf,
  onClose,
}: {
  ghost: GhostBox;
  titleOf: (ref: string) => string;
  onClose: () => void;
}) {
  const { t } = useI18n();
  return (
    <div
      className="draft-ghost-detail"
      role="dialog"
      aria-label={t("draft.ghostDetails", { title: ghost.title })}
      data-draft-key={ghost.key}
      style={{ left: ghost.rect.left, top: ghost.rect.bottom + 8, width: CARD_W + 40 }}
      // Inside it, a press is not the board's either.
      onPointerDown={(event) => event.stopPropagation()}
    >
      <p className="draft-ghost-detail-title">{ghost.title}</p>
      <p className={`draft-ghost-detail-body${ghost.body ? "" : " is-empty"}`}>
        {ghost.body || t("draft.ghostNoBody")}
      </p>
      <div className="draft-after">
        {ghost.after.length > 0 ? (
          ghost.after.map((ref) => (
            <span key={ref} className={`draft-chip${isExistingRef(ref) ? " is-existing" : ""}`}>
              {t("draft.after", { title: titleOf(ref) })}
            </span>
          ))
        ) : (
          <span className="draft-ghost-detail-first">{t("draft.ghostFirst")}</span>
        )}
      </div>
      <button type="button" className="draft-text-btn draft-ghost-detail-close" onClick={onClose}>
        {t("draft.ghostClose")}
      </button>
    </div>
  );
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
  // A coding agent's plan keeps longer titles than the copilot's own draft.
  const maxTitle = useCopilot((state) => (isPlanDraft(state, entryId) ? MAX_PLAN_TITLE_CHARS : MAX_TITLE_CHARS));
  // While a refine of it runs the draft takes no edits (what lands replaces
  // it): the ghosts offer no rename and no link handle, and say why, as the
  // card does.
  const refining = useCopilot((state) => Boolean(entryId && isRefining(state, entryId)));
  const selection = useApp(canvasSelectionIds);
  const graph = useApp((state) => state.graph);
  const themed = useThemeActive();
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
      return [
        {
          key: node.key,
          title: node.title,
          kind: node.kind,
          body: node.body ?? "",
          after: node.after ?? [],
          rect: { left, top, right: left + CARD_W, bottom: top + CARD_H },
        },
      ];
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
  const [renaming, setRenaming] = useState<string | null>(null);
  // The picture card whose body and chips show; none once it is gone.
  const [shown, setShown] = useState<string | null>(null);
  const detailKey = themed && shown && ghosts.some((ghost) => ghost.key === shown) ? shown : null;

  // A press anywhere else, or Escape, puts the details away.
  useEffect(() => {
    if (!detailKey) return;
    const away = (event: PointerEvent) => {
      const target = event.target instanceof Element ? event.target : null;
      if (target?.closest(".draft-ghost-detail")) return;
      if (target?.closest(".draft-ghost")?.getAttribute("data-draft-key") === detailKey) return;
      setShown(null);
    };
    const escape = (event: KeyboardEvent) => {
      if (event.key === "Escape") setShown(null);
    };
    window.addEventListener("pointerdown", away, true);
    window.addEventListener("keydown", escape);
    return () => {
      window.removeEventListener("pointerdown", away, true);
      window.removeEventListener("keydown", escape);
    };
  }, [detailKey]);

  // A refine that starts mid-rename ends it: what lands replaces the draft.
  useEffect(() => {
    if (refining) setRenaming(null);
  }, [refining]);

  if (!entryId || !draft || ghosts.length === 0) return null;

  const ghostByKey = new Map(ghosts.map((ghost) => [ghost.key, ghost]));
  const rectOf = (ref: string): Rect | null =>
    isExistingRef(ref) ? realRect(existingIdOf(ref)) : (ghostByKey.get(ref)?.rect ?? null);

  const titleOfRef = (ref: string) => {
    if (isExistingRef(ref)) {
      const id = existingIdOf(ref);
      return graph?.nodes?.find((node) => node.id === id)?.title || id;
    }
    return ghostByKey.get(ref)?.title || ref;
  };

  /** A press on a ghost is the ghost's: the board would start a marquee or a
   * pan, or clear its selection. Other buttons go on to the board. */
  const holdPress = (event: ReactPointerEvent<HTMLElement>) => {
    if (event.pointerType === "mouse" && event.button !== 0) return;
    event.stopPropagation();
  };

  const finishRename = (key: string, title: string | null) => {
    setRenaming(null);
    const next = title?.trim();
    if (next && next !== ghostByKey.get(key)?.title) {
      editDraft(entryId, (current) => renameNode(current, key, next, maxTitle));
    }
  };

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

  const detail = detailKey ? ghostByKey.get(detailKey) : undefined;

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
      {ghosts.map((ghost) => {
        const editing = renaming === ghost.key && !refining;
        const tip = refining
          ? t("draft.refining")
          : t(themed ? "draft.ghostTipThemed" : "draft.ghostTip", { title: ghost.title });
        return (
          <div
            key={ghost.key}
            className={`draft-ghost${themed ? " is-picture" : ""}${detailKey === ghost.key ? " is-open" : ""}${
              refining ? " is-refining" : ""
            }`}
            data-draft-key={ghost.key}
            data-kind={ghost.kind}
            title={editing ? undefined : tip}
            style={{
              left: ghost.rect.left,
              top: ghost.rect.top,
              width: CARD_W,
              height: CARD_H,
            }}
            onPointerDown={holdPress}
            onClick={(event) => {
              if (!themed || editing) return;
              if ((event.target as Element).closest(".draft-ghost-handle")) return;
              setShown(detailKey === ghost.key ? null : ghost.key);
            }}
            onDoubleClick={(event) => {
              if ((event.target as Element).closest(".draft-ghost-handle, .draft-ghost-input")) return;
              event.stopPropagation();
              if (refining) return;
              setShown(null);
              setRenaming(ghost.key);
            }}
          >
            {themed && <KindGlyph type={nodeTypeById(graph, ghost.kind)} large />}
            {editing ? (
              <GhostRename
                title={ghost.title}
                label={t("draft.ghostRename", { title: ghost.title })}
                maxLength={maxTitle}
                onDone={(title) => finishRename(ghost.key, title)}
              />
            ) : (
              <span className="draft-ghost-title">{ghost.title}</span>
            )}
            {!refining && (
              <button
                type="button"
                className="draft-ghost-handle"
                aria-label={`${ghost.title} · ${t("draft.handle")}`}
                title={t("draft.handle")}
                onPointerDown={(event) => startLink(event, ghost.key)}
              />
            )}
          </div>
        );
      })}
      {detail && <GhostDetail ghost={detail} titleOf={titleOfRef} onClose={() => setShown(null)} />}
    </>
  );
}
