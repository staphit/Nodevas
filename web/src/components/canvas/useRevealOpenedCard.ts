/**
 * Opening a node from the board keeps its card in sight.
 *
 * The node drawer is laid over the board's right side (a sheet over the
 * bottom on a phone), so a card opened from where it sits, or picked from the
 * Problems panel, could end up under its own document. Once the drawer is
 * there, a card that is not wholly in the part of the board still showing is
 * revealed the way a Ctrl+K jump is: centred in that part. A card already
 * clear of the drawer stays where the reader put it.
 */

import { useCallback, useEffect, useState, type RefObject } from "react";
import { useApp } from "../../store";
import { visibleBoardArea, type Area } from "../lane/boardVisibleArea";

function within(rect: Area, area: Area): boolean {
  return (
    rect.left >= area.left &&
    rect.top >= area.top &&
    rect.right <= area.right &&
    rect.bottom <= area.bottom
  );
}

export function useRevealOpenedCard({
  enabled,
  boardRef,
  openTab,
}: {
  enabled: boolean;
  boardRef: RefObject<HTMLDivElement | null>;
  openTab: (id: string) => Promise<void>;
}): (nodeId: string) => Promise<void> {
  const revealNode = useApp((state) => state.revealNode);
  const [opened, setOpened] = useState<{ nodeId: string } | null>(null);

  // Measured after the commit that mounts the drawer: opening a node puts it
  // on screen before its document has loaded.
  useEffect(() => {
    if (!opened) return;
    setOpened(null);
    const board = boardRef.current;
    if (!enabled || !board) return;
    const card = Array.from(board.querySelectorAll<HTMLElement>(".col-card[data-node-id]")).find(
      (element) => element.dataset.nodeId === opened.nodeId,
    );
    // A card the culling left unmounted is off-screen: it wants revealing too.
    if (card && within(card.getBoundingClientRect(), visibleBoardArea(board))) return;
    revealNode(opened.nodeId);
  }, [opened, enabled, boardRef, revealNode]);

  // `openTab` itself, so anything on the board that opens a node can take it.
  return useCallback(
    (nodeId: string) => {
      const opening = openTab(nodeId);
      if (enabled) setOpened({ nodeId });
      return opening;
    },
    [openTab, enabled],
  );
}
