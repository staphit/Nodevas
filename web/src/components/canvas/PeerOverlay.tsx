/**
 * Other people on the board: their pointers, and the cards they are dragging.
 *
 * Both change many times a second while somebody is working, and neither
 * changes anything about the cards themselves. So this layer subscribes to the
 * collaboration state on its own; the board above it never hears about a
 * remote pointer and no card re-renders for one.
 */

import { useMemo } from "react";
import { useApp } from "../../store";
import { ROW_H, type Col, type Point } from "./geometry";

/** Somebody else's selection, mid-gesture. */
export interface PeerGhost {
  peerId: string;
  color: string;
  name: string;
  ids: string[];
  dx: number;
  dy: number;
}

/** Where somebody else's pointer is, in board coordinates. */
export interface PeerPointer {
  peerId: string;
  color: string;
  name: string;
  point: Point;
}

export function PeerOverlay({
  colOf,
  centerX,
  rowTop,
  offsetX,
  offsetY,
}: {
  colOf: Map<string, Col>;
  centerX: (column: Col) => number;
  rowTop: (row: number) => number;
  /** Where the board's origin is drawn; pointers arrive offset-free. */
  offsetX: number;
  offsetY: number;
}) {
  const ghosts = useApp((state) => state.ghosts);
  const peers = useApp((state) => state.peers);
  const presences = useApp((state) => state.presences);

  const pointers = useMemo<PeerPointer[]>(
    () =>
      peers
        .filter((peer) => presences[peer.id]?.pointer)
        .map((peer) => ({
          peerId: peer.id,
          name: peer.actor?.name || "?",
          color: peer.color || "#888",
          point: presences[peer.id].pointer!,
        })),
    [peers, presences],
  );

  const peerGhosts = useMemo<PeerGhost[]>(
    () =>
      Object.entries(ghosts).map(([peerId, ghost]) => {
        const peer = peers.find((candidate) => candidate.id === peerId);
        return {
          peerId,
          color: peer?.color || "#888",
          name: peer?.actor?.name ?? "",
          ...ghost,
        };
      }),
    [ghosts, peers],
  );

  return (
    <>
      {/* An outline rather than a copy of the card: it says where the card is
          going without competing with the one that is still where it was. */}
      {peerGhosts.flatMap((ghost) =>
        ghost.ids.map((nodeId) => {
          const column = colOf.get(nodeId);
          if (!column) return null;
          return (
            <div
              key={`${ghost.peerId}-${nodeId}`}
              className="peer-ghost"
              aria-hidden
              title={ghost.name}
              style={{
                left: centerX(column) - column.width / 2 + ghost.dx,
                top: rowTop(column.row) + ROW_H / 2 - column.height / 2 + ghost.dy,
                width: column.width,
                height: column.height,
                ["--peer-color" as string]: ghost.color,
              }}
            />
          );
        }),
      )}
      {pointers.map((peer) => (
        <div
          key={peer.peerId}
          className="peer-pointer"
          aria-hidden
          style={{
            left: offsetX + peer.point.x,
            top: offsetY + peer.point.y,
            ["--peer-color" as string]: peer.color,
          }}
        >
          <span className="peer-pointer-label">{peer.name}</span>
        </div>
      ))}
    </>
  );
}
