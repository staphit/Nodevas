/**
 * Who else is in the document you have open [P2].
 *
 * The server has always known this — it publishes a peer list per project with
 * the document each person holds and a stable colour per account — but the only
 * place it surfaced was a strip of unlabelled circles inside the drawer, below
 * the fold of anything but a tall window. This is the glanceable version, in the
 * top bar next to the save state, which is where a reader looks to answer "am I
 * alone in here?".
 *
 * Only peers on the *active* document are shown. Everyone else in the project is
 * real, but they are not who you are about to collide with, and a bar that
 * counts them reads as a crowd that is not there.
 */

import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type KeyboardEvent as ReactKeyboardEvent,
} from "react";
import { useShallow } from "zustand/react/shallow";
import { useI18n } from "../i18n";

// The type comes from the module rather than the `api` barrel: the barrel is
// the write path components are not supposed to reach for, and
// scripts/check-api-imports.mjs reads the import line, not what it pulls.
import type { Peer } from "../api/ws";
import type { PresenceSelection, PresenceState } from "../collab/presence";
import { useApp, usePeersOnNode } from "../store";

/** How many faces the cluster shows before it collapses the rest into "+N". */
const MAX_FACES = 3;

/** The narrow bar has less room, so it keeps fewer faces rather than wrapping. */
const MAX_FACES_NARROW = 2;

/** Asks the open drawer to move to where a peer is. */
export const FOLLOW_PEER_EVENT = "nodevas-follow-peer";

export interface FollowPeerDetail {
  peerId: string;
  nodeId: string;
  pageId?: string;
  selection?: PresenceSelection;
}

/** Two characters is what fits in a 22px circle at a readable size. */
export function initials(name: string): string {
  return (name.trim() || "?").slice(0, 2);
}

export function PresenceAvatars({ narrow = false }: { narrow?: boolean }) {
  const activeTab = useApp((s) => s.activeTab);
  const peers = usePeersOnNode(activeTab ?? null);
  const presences = useApp(useShallow((s) => s.presences));
  const { t } = useI18n();

  const [open, setOpen] = useState(false);
  const rootRef = useRef<HTMLDivElement | null>(null);
  const triggerRef = useRef<HTMLButtonElement | null>(null);
  const menuRef = useRef<HTMLDivElement | null>(null);

  // Same contract as the overflow menu: Escape and a choice hand the keyboard
  // back to the trigger, an outside click leaves the caret where it was aimed.
  const closeAndReturnFocus = useCallback(() => {
    setOpen(false);
    triggerRef.current?.focus();
  }, []);

  const menuItems = useCallback(
    () =>
      Array.from(menuRef.current?.querySelectorAll<HTMLButtonElement>('[role="menuitem"]') ?? []),
    [],
  );

  useEffect(() => {
    if (!open) return;
    menuItems()[0]?.focus();
  }, [open, menuItems]);

  useEffect(() => {
    if (!open) return;
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return;
      // Claimed here so the drawer and the board below do not also close.
      event.stopPropagation();
      closeAndReturnFocus();
    };
    const onPointerDown = (event: PointerEvent) => {
      const target = event.target;
      if (target instanceof Node && rootRef.current?.contains(target)) return;
      setOpen(false);
    };
    document.addEventListener("keydown", onKeyDown, true);
    document.addEventListener("pointerdown", onPointerDown, true);
    return () => {
      document.removeEventListener("keydown", onKeyDown, true);
      document.removeEventListener("pointerdown", onPointerDown, true);
    };
  }, [open, closeAndReturnFocus]);

  // A cluster that is empty most of the time must leave no gap behind: a
  // single-user install should not carry a permanent hole in its top bar.
  const maxFaces = narrow ? MAX_FACES_NARROW : MAX_FACES;
  const shown = useMemo(() => peers.slice(0, maxFaces), [peers, maxFaces]);
  const hidden = peers.length - shown.length;

  useEffect(() => {
    if (peers.length === 0) setOpen(false);
  }, [peers.length]);

  if (!activeTab || peers.length === 0) return null;

  const onMenuKeyDown = (event: ReactKeyboardEvent<HTMLDivElement>) => {
    if (event.key !== "ArrowDown" && event.key !== "ArrowUp") return;
    const buttons = menuItems();
    if (buttons.length === 0) return;
    event.preventDefault();
    const at = buttons.indexOf(document.activeElement as HTMLButtonElement);
    const step = event.key === "ArrowDown" ? 1 : -1;
    const next = (at + step + buttons.length) % buttons.length;
    buttons[next]?.focus();
  };

  const follow = (peer: Peer) => {
    const presence = presences[peer.id];
    closeAndReturnFocus();
    window.dispatchEvent(
      new CustomEvent<FollowPeerDetail>(FOLLOW_PEER_EVENT, {
        detail: {
          peerId: peer.id,
          nodeId: activeTab,
          pageId: presence?.pageId,
          selection: presence?.selection,
        },
      }),
    );
  };

  const label = t("presence.peopleHere", { count: peers.length });

  return (
    <div className="topbar-presence" ref={rootRef}>
      <button
        type="button"
        className="topbar-presence-cluster"
        ref={triggerRef}
        onClick={() => setOpen((was) => !was)}
        title={label}
        aria-label={label}
        aria-haspopup="menu"
        aria-expanded={open}
      >
        {shown.map((peer) => (
          <Face key={peer.id} peer={peer} />
        ))}
        {hidden > 0 && <span className="topbar-presence-more">+{hidden}</span>}
      </button>
      {open && (
        <div
          className="topbar-presence-menu"
          role="menu"
          aria-label={label}
          ref={menuRef}
          onKeyDown={onMenuKeyDown}
        >
          {peers.map((peer) => (
            <button
              key={peer.id}
              type="button"
              role="menuitem"
              className="topbar-presence-item"
              onClick={() => follow(peer)}
            >
              <Face peer={peer} />
              <span className="topbar-presence-who">
                <span className="topbar-presence-name">{peer.actor.name || t("presence.unnamed")}</span>
                <span className="topbar-presence-where">{whereLabel(peer, presences[peer.id], t)}</span>
              </span>
            </button>
          ))}
        </div>
      )}
    </div>
  );
}

function Face({ peer }: { peer: Peer }) {
  return (
    <span
      className={`topbar-presence-face${peer.editing ? " editing" : ""}`}
      style={{ ["--peer-color" as string]: peer.color || "var(--accent)" }}
      aria-hidden
    >
      {initials(peer.actor.name)}
    </span>
  );
}

/** What the row says under the name: their role, and whether they are typing. */
function whereLabel(
  peer: Peer,
  presence: PresenceState | undefined,
  t: (key: string, values?: Record<string, string | number>) => string,
): string {
  const parts = [
    t(`presence.role.${peer.actor.role}`),
    peer.editing ? t("presence.editing") : t("presence.viewing"),
  ];
  if (presence?.pageId) parts.push(t("presence.subpage", { id: presence.pageId }));
  return parts.filter(Boolean).join(" · ");
}
