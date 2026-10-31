/**
 * A node's short menu [B-06]: right-click on a card, or the card's `⋯`.
 *
 * The everyday actions only. Dependencies used to fill this menu as a form;
 * "Add dependency…" now opens the node's panel on its Dependencies view, and
 * the agent actions sit together under one submenu. The two submenus open in
 * place, below their row, so the menu never grows a second floating panel
 * that a phone has no room for.
 *
 * Keyboard: the first item takes focus, the arrow keys move, → opens a
 * submenu and ← closes it, and Escape (LaneContextMenuLayer) closes the menu
 * and hands focus back to the card it was opened from.
 */

import {
  useEffect,
  useRef,
  useState,
  type KeyboardEvent as ReactKeyboardEvent,
  type ReactNode,
} from "react";

import { IconSend, IconTerminal } from "../../icons";
import { useI18n } from "../../i18n";
import { openAutopilot } from "../../autopilot/openAutopilot";
import { nodeTypeById } from "../../domain/graph/nodeTypes";
import { openNodeDetail } from "../drawer/openNodeDetail";
import { openNodeRuns } from "../../runs/RunsRoot";
import { useRuns } from "../../runs/runStore";
import { useWorkstationVisible } from "../../runners/workstation";
import { reportError } from "../../store";
import type { Graph } from "../../types";
import { confirmAction } from "../ConfirmDialog";
import type { LaneContextMenu } from "../LaneView";
import { nodeLabels, sendNodesToProject } from "../canvas/nodeTransfer";

export interface NodeActionsMenuProps {
  contextMenu: Extract<LaneContextMenu, { kind: "node-actions" }>;
  graph: Graph | null;
  nodeTitle: (id: string | undefined) => string;
  openTab: (id: string) => Promise<void>;
  selectNode: (id: string) => void;
  duplicateNode: (id: string) => Promise<string>;
  deleteNode: (id: string) => Promise<void>;
  /** Draws a group background around these nodes (the selection's, by default). */
  createGroupFromSelection: (ids?: readonly string[]) => void;
  setContextMenu: (menu: LaneContextMenu | null) => void;
}

type Submenu = "agent" | "transfer" | null;

const ITEM_SELECTOR = '[role="menuitem"]:not(:disabled)';

export function NodeActionsMenu({
  contextMenu,
  graph,
  nodeTitle,
  openTab,
  selectNode,
  duplicateNode,
  deleteNode,
  createGroupFromSelection,
  setContextMenu,
}: NodeActionsMenuProps) {
  const { t } = useI18n();
  const workstation = useWorkstationVisible();
  const nodeId = contextMenu.nodeId;
  const title = nodeTitle(nodeId);
  // A type agents cannot work gets no agent submenu at all; a Goal's is
  // Autopilot first and no single dispatch.
  const agentMode = nodeTypeById(
    graph,
    graph?.nodes?.find((node) => node.id === nodeId)?.kind,
  ).agentMode;
  const [submenu, setSubmenu] = useState<Submenu>(null);
  const rootRef = useRef<HTMLDivElement>(null);

  // Focus moves in on open and goes back where it came from on close, unless
  // the action itself put it somewhere (a dialog, the drawer).
  useEffect(() => {
    const opener = document.activeElement as HTMLElement | null;
    rootRef.current?.querySelector<HTMLElement>(ITEM_SELECTOR)?.focus();
    return () => {
      const lost = !document.activeElement || document.activeElement === document.body;
      if (lost && opener?.isConnected) opener.focus();
    };
  }, []);

  const close = () => setContextMenu(null);
  const run = (action: () => void) => () => {
    close();
    action();
  };

  const focusSubmenu = (which: Exclude<Submenu, null>) => {
    requestAnimationFrame(() =>
      rootRef.current
        ?.querySelector<HTMLElement>(`[data-submenu="${which}"] ${ITEM_SELECTOR}`)
        ?.focus(),
    );
  };

  const onKeyDown = (event: ReactKeyboardEvent<HTMLDivElement>) => {
    const target = event.target as HTMLElement;
    if (event.key === "ArrowDown" || event.key === "ArrowUp") {
      const items = Array.from(
        rootRef.current?.querySelectorAll<HTMLElement>(ITEM_SELECTOR) ?? [],
      );
      if (!items.length) return;
      event.preventDefault();
      const at = items.indexOf(target);
      const step = event.key === "ArrowDown" ? 1 : -1;
      items[(at + step + items.length) % items.length]?.focus();
      return;
    }
    const opens = target.getAttribute("data-opens") as Exclude<Submenu, null> | null;
    if (event.key === "ArrowRight" && opens) {
      event.preventDefault();
      setSubmenu(opens);
      focusSubmenu(opens);
      return;
    }
    const inside = target.closest<HTMLElement>("[data-submenu]")?.dataset.submenu;
    if (event.key === "ArrowLeft" && inside) {
      event.preventDefault();
      setSubmenu(null);
      rootRef.current?.querySelector<HTMLElement>(`[data-opens="${inside}"]`)?.focus();
    }
  };

  const submenuTrigger = (which: Exclude<Submenu, null>, label: string, icon?: ReactNode) => (
    <button
      type="button"
      role="menuitem"
      className="node-actions-item"
      aria-haspopup="menu"
      aria-expanded={submenu === which}
      data-opens={which}
      onClick={() => setSubmenu((open) => (open === which ? null : which))}
    >
      {icon}
      <span>{label}</span>
      <span className="node-actions-chevron" aria-hidden>
        {submenu === which ? "▾" : "▸"}
      </span>
    </button>
  );

  return (
    // The menu role and its name sit on LaneContextMenuLayer's panel.
    <div ref={rootRef} className="node-actions" onKeyDown={onKeyDown}>
      <div className="lane-context-title node-actions-title" role="presentation">
        <small>{title}</small>
      </div>
      <button
        type="button"
        role="menuitem"
        className="node-actions-item"
        onClick={run(() => {
          selectNode(nodeId);
          void openTab(nodeId).catch(reportError);
        })}
      >
        <span>{t("board.nodeMenu.open")}</span>
      </button>
      {workstation && agentMode !== "none" && (
        <>
          {submenuTrigger("agent", t("board.nodeMenu.agent"), <IconSend size={13} />)}
          {submenu === "agent" && (
            <div
              className="node-actions-submenu"
              role="menu"
              aria-label={t("board.nodeMenu.agent")}
              data-submenu="agent"
            >
              {agentMode === "autopilot" && (
                <button
                  type="button"
                  role="menuitem"
                  className="node-actions-item"
                  onClick={run(() => openAutopilot(nodeId))}
                >
                  <IconSend size={13} />
                  <span>{t("nodeMenu.autopilot")}</span>
                </button>
              )}
              {agentMode === "dispatch" && (
                <button
                  type="button"
                  role="menuitem"
                  className="node-actions-item"
                  onClick={run(() => useRuns.getState().openDispatch(nodeId))}
                >
                  <IconSend size={13} />
                  <span>{t("nodeMenu.dispatch")}</span>
                </button>
              )}
              <button
                type="button"
                role="menuitem"
                className="node-actions-item"
                onClick={run(() => openNodeRuns(nodeId))}
              >
                <IconTerminal size={13} />
                <span>{t("nodeMenu.runs")}</span>
              </button>
              {agentMode === "dispatch" && (
                <button
                  type="button"
                  role="menuitem"
                  className="node-actions-item"
                  onClick={run(() => openAutopilot(nodeId))}
                >
                  <IconSend size={13} />
                  <span>{t("nodeMenu.autopilot")}</span>
                </button>
              )}
            </div>
          )}
        </>
      )}
      <button
        type="button"
        role="menuitem"
        className="node-actions-item"
        // The node's panel, on its Dependencies view: prerequisites, what
        // this node blocks and the advanced conditions, all in one place.
        onClick={run(() => openNodeDetail(nodeId, "dependencies"))}
      >
        <span>{t("board.nodeMenu.addDependency")}</span>
      </button>
      <button
        type="button"
        role="menuitem"
        className="node-actions-item"
        onClick={run(() => {
          void duplicateNode(nodeId)
            .then((id) => selectNode(id))
            .catch(reportError);
        })}
      >
        <span>{t("nodeMenu.duplicate")}</span>
      </button>
      <button
        type="button"
        role="menuitem"
        className="node-actions-item"
        title={t("batch.groupBackgroundTitle")}
        // The same background the batch toolbar draws, around this one card:
        // it becomes the selection so what is grouped is what is shown.
        onClick={run(() => {
          selectNode(nodeId);
          createGroupFromSelection([nodeId]);
        })}
      >
        <span>{t("batch.groupBackground")}</span>
      </button>
      {submenuTrigger("transfer", t("board.batch.transfer"))}
      {submenu === "transfer" && (
        <div
          className="node-actions-submenu"
          role="menu"
          aria-label={t("board.batch.transfer")}
          data-submenu="transfer"
        >
          {(["cut", "copy"] as const).map((mode) => (
            <button
              key={mode}
              type="button"
              role="menuitem"
              className="node-actions-item"
              title={t(mode === "cut" ? "batch.moveProjectTitle" : "batch.copyProjectTitle")}
              onClick={run(() => {
                void sendNodesToProject([nodeId], nodeLabels(graph, [nodeId]), mode).catch(
                  reportError,
                );
              })}
            >
              <span>{t(mode === "cut" ? "batch.moveProject" : "batch.copyProject")}</span>
            </button>
          ))}
        </div>
      )}
      <div className="node-actions-separator" role="separator" />
      <button
        type="button"
        role="menuitem"
        className="node-actions-item danger"
        onClick={run(() => {
          void confirmAction({
            title: t("nodeMenu.trashTitle"),
            description: t("nodeMenu.trashDescription", { title }),
            confirmLabel: t("batch.moveToTrash"),
            tone: "danger",
          })
            .then((confirmed) => {
              if (confirmed) return deleteNode(nodeId);
            })
            .catch(reportError);
        })}
      >
        <span>{t("nodeMenu.trash")}</span>
      </button>
    </div>
  );
}
