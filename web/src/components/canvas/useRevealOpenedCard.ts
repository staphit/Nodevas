/**
 * Opening a node keeps its card in sight.
 *
 * The node drawer is laid over the board's right side (a sheet over the
 * bottom on a phone), so a card opened from where it sits, picked from the
 * Problems panel, or opened from outside the board — the explorer's tree, a
 * card menu's "Add dependency…", Agents — could end up under its own
 * document. Once the drawer is there, a card that is not wholly in the part of
 * the board still showing is revealed the way a Ctrl+K jump is: centred in
 * that part. A card already clear of the drawer stays where the reader put it.
 *
 * Most of those ways never pass through the board, so the node the drawer
 * shows is watched rather than the board's own `openTab`. That one is still
 * wrapped for the one open the drawer does not change on: the node it
 * already shows, opened again from its card.
 *
 * A card the board has just made — the toolbar's New node — is checked the
 * same way, drawer or not: it should land where the reader is looking, and
 * when it cannot (a new card never goes left of column 0) it is brought there.
 */

import { useCallback, useEffect, useRef, useState, type RefObject } from "react";
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
}): {
  openTab: (nodeId: string) => Promise<void>;
  revealIfHidden: (nodeId: string) => void;
} {
  const revealNode = useApp((state) => state.revealNode);
  const activeTab = useApp((state) => state.activeTab);
  const [opened, setOpened] = useState<{ nodeId: string } | null>(null);

  // A node the drawer comes to show, however it was opened. Not the one it
  // showed when the board came up: that was opened before, and left where it is.
  const shown = useRef(activeTab);
  useEffect(() => {
    if (activeTab === shown.current) return;
    shown.current = activeTab;
    if (activeTab !== null && enabled) setOpened({ nodeId: activeTab });
  }, [activeTab, enabled]);

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
  // A node the drawer moves to is caught above; this is the one it was on.
  const openTabInSight = useCallback(
    (nodeId: string) => {
      const already = useApp.getState().activeTab === nodeId;
      const opening = openTab(nodeId);
      if (enabled && already) setOpened({ nodeId });
      return opening;
    },
    [openTab, enabled],
  );
  // Measured after the commit that mounts the card.
  const revealIfHidden = useCallback(
    (nodeId: string) => {
      if (enabled) setOpened({ nodeId });
    },
    [enabled],
  );
  return { openTab: openTabInSight, revealIfHidden };
}
