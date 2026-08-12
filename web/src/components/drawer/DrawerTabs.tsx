import {
  useEffect,
  useRef,
  useState,
  type KeyboardEvent as ReactKeyboardEvent,
  type MutableRefObject,
} from "react";

import { api } from "../../api";
import {
  IconClose,
  IconCopy,
  IconDoc,
  IconFolderOpen,
  IconPages,
  IconPin,
} from "../../icons";
import { nodeById, reportError, useApp } from "../../store";
import { useI18n } from "../../i18n";

type TabContextMenuState = {
  id: string;
  x: number;
  y: number;
};

function joinNativePath(base: string, ...parts: string[]): string {
  const windowsSeparator = String.fromCharCode(92);
  const separator = base.includes(windowsSeparator) ? windowsSeparator : "/";
  return [
    base.replace(/[\\/]+$/, ""),
    ...parts.map((part) => part.replace(/^[\\/]+|[\\/]+$/g, "")),
  ]
    .filter(Boolean)
    .join(separator);
}

async function copyText(value: string): Promise<void> {
  try {
    await navigator.clipboard.writeText(value);
    return;
  } catch {
    const field = document.createElement("textarea");
    field.value = value;
    field.style.position = "fixed";
    field.style.opacity = "0";
    document.body.appendChild(field);
    field.select();
    document.execCommand("copy");
    field.remove();
  }
}

/** The row of open node tabs, and the per-tab context menu it opens. */
export function DrawerTabs({
  activeTab,
  menuOpenRef,
  onToggleCollapsed,
  onClose,
}: {
  activeTab: string;
  menuOpenRef: MutableRefObject<boolean>;
  onToggleCollapsed: () => void;
  onClose: () => void;
}) {
  const { t } = useI18n();
  const tabs = useApp((s) => s.tabs);
  const graph = useApp((s) => s.graph);
  const setActiveTab = useApp((s) => s.setActiveTab);
  const closeTabs = useApp((s) => s.closeTabs);
  const setTabPinned = useApp((s) => s.setTabPinned);
  const projects = useApp((s) => s.projects);
  const activeProject = useApp((s) => s.activeProject);
  const [tabContextMenu, setTabContextMenu] = useState<TabContextMenuState | null>(null);
  const tabContextMenuRef = useRef<HTMLDivElement>(null);

  // The panel's own Escape handler stands down while the menu is open, so
  // that dismissing the menu does not also close the panel.
  menuOpenRef.current = tabContextMenu !== null;
  useEffect(
    () => () => {
      menuOpenRef.current = false;
    },
    [menuOpenRef],
  );

  useEffect(() => {
    if (!tabContextMenu) return;
    const closeOutside = (event: PointerEvent) => {
      if (!tabContextMenuRef.current?.contains(event.target as Node)) {
        setTabContextMenu(null);
      }
    };
    const closeMenu = () => setTabContextMenu(null);
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key === "Escape") setTabContextMenu(null);
    };
    window.addEventListener("pointerdown", closeOutside);
    window.addEventListener("resize", closeMenu);
    window.addEventListener("scroll", closeMenu, true);
    window.addEventListener("keydown", closeOnEscape);
    return () => {
      window.removeEventListener("pointerdown", closeOutside);
      window.removeEventListener("resize", closeMenu);
      window.removeEventListener("scroll", closeMenu, true);
      window.removeEventListener("keydown", closeOnEscape);
    };
  }, [tabContextMenu]);

  useEffect(() => {
    if (!tabContextMenu) return;
    const frame = window.requestAnimationFrame(() => {
      tabContextMenuRef.current
        ?.querySelector<HTMLButtonElement>('[role="menuitem"]:not(:disabled)')
        ?.focus();
    });
    return () => window.cancelAnimationFrame(frame);
  }, [tabContextMenu]);

  const contextTab = tabContextMenu
    ? tabs.find((tab) => tab.id === tabContextMenu.id) ?? null
    : null;
  const contextTabIndex = contextTab
    ? tabs.findIndex((tab) => tab.id === contextTab.id)
    : -1;
  const closeOtherIDs = contextTab
    ? tabs
        .filter((tab) => tab.id !== contextTab.id && !tab.pinned)
        .map((tab) => tab.id)
    : [];
  const closeRightIDs =
    contextTabIndex >= 0
      ? tabs
          .slice(contextTabIndex + 1)
          .filter((tab) => !tab.pinned)
          .map((tab) => tab.id)
      : [];
  const closeUnpinnedIDs = tabs.filter((tab) => !tab.pinned).map((tab) => tab.id);
  const activeProjectPath =
    projects.find((project) => project.name === activeProject && !project.isFolder)?.path ?? "";
  const contextRelativePath = contextTab ? `nodes/${contextTab.id}.md` : "";
  const contextFullPath = contextTab && activeProjectPath
    ? joinNativePath(activeProjectPath, "nodes", `${contextTab.id}.md`)
    : "";
  const contextFolderPath = activeProjectPath
    ? joinNativePath(activeProjectPath, "nodes")
    : "";

  const closeFromMenu = (ids: string[]) => {
    setTabContextMenu(null);
    void closeTabs(ids).catch(reportError);
  };

  const handleMenuKeyDown = (event: ReactKeyboardEvent<HTMLDivElement>) => {
    const items = Array.from(
      event.currentTarget.querySelectorAll<HTMLButtonElement>(
        '[role="menuitem"]:not(:disabled)',
      ),
    );
    if (items.length === 0) return;
    const current = items.indexOf(document.activeElement as HTMLButtonElement);
    let next = current;
    if (event.key === "ArrowDown") next = (current + 1 + items.length) % items.length;
    else if (event.key === "ArrowUp") next = (current - 1 + items.length) % items.length;
    else if (event.key === "Home") next = 0;
    else if (event.key === "End") next = items.length - 1;
    else return;
    event.preventDefault();
    items[next]?.focus();
  };

  return (
    <>
      <div className="drawer-head">
        <button
          type="button"
          className="drawer-collapse"
          title={t("drawer.tabs.collapseTitle")}
          aria-label={t("drawer.tabs.collapse")}
          onClick={onToggleCollapsed}
        >
          »
        </button>
        <div className="tab-bar">
          {tabs.map((tab) => (
            <div
              key={tab.id}
              className={`tab${tab.id === activeTab ? " active" : ""}${tab.pinned ? " pinned" : ""}`}
              onClick={() => setActiveTab(tab.id)}
              onContextMenu={(event) => {
                event.preventDefault();
                setTabContextMenu({ id: tab.id, x: event.clientX, y: event.clientY });
              }}
              aria-haspopup="menu"
            >
              <IconDoc size={12} />
              <span>{nodeById(graph, tab.id)?.title || tab.id}</span>
              {tab.pinned && <IconPin className="tab-pin" size={11} />}
              {tab.dirty && <span className="dirty-dot" aria-label={t("drawer.tabs.unsaved")} />}
              <button
                className="tab-close"
                aria-label={t("drawer.tabs.close", { id: tab.id })}
                onClick={(e) => {
                  e.stopPropagation();
                  void closeTabs([tab.id]).catch(reportError);
                }}
              >
                <IconClose size={11} />
              </button>
            </div>
          ))}
        </div>
        <button className="drawer-close" aria-label={t("drawer.tabs.closePanel")} onClick={onClose}>
          <IconClose size={15} />
        </button>
      </div>
      {tabContextMenu && contextTab && (
        <div
          ref={tabContextMenuRef}
          className="tab-context-menu"
          role="menu"
          aria-label={t("drawer.tabs.actions", {
            name: nodeById(graph, contextTab.id)?.title || contextTab.id,
          })}
          style={{
            left: Math.max(8, Math.min(tabContextMenu.x, window.innerWidth - 290)),
            top: Math.max(8, Math.min(tabContextMenu.y, window.innerHeight - 382)),
          }}
          onContextMenu={(event) => event.preventDefault()}
          onKeyDown={handleMenuKeyDown}
        >
          <div className="tab-context-heading">
            <span>{nodeById(graph, contextTab.id)?.title || contextTab.id}</span>
            <small>{contextRelativePath}</small>
          </div>
          <button
            type="button"
            role="menuitem"
            onClick={() => {
              setTabPinned(contextTab.id, !contextTab.pinned);
              setTabContextMenu(null);
            }}
          >
            <IconPin size={14} />
            <span>{contextTab.pinned ? t("drawer.tabs.unpin") : t("drawer.tabs.pin")}</span>
          </button>
          <div className="tab-context-separator" role="separator" />
          <button type="button" role="menuitem" onClick={() => closeFromMenu([contextTab.id])}>
            <IconClose size={14} />
            <span>{t("common.close")}</span>
          </button>
          <button
            type="button"
            role="menuitem"
            disabled={closeOtherIDs.length === 0}
            onClick={() => closeFromMenu(closeOtherIDs)}
          >
            <IconPages size={14} />
            <span>{t("drawer.tabs.closeOther")}</span>
          </button>
          <button
            type="button"
            role="menuitem"
            disabled={closeRightIDs.length === 0}
            onClick={() => closeFromMenu(closeRightIDs)}
          >
            <IconPages size={14} />
            <span>{t("drawer.tabs.closeRight")}</span>
          </button>
          <button
            type="button"
            role="menuitem"
            disabled={closeUnpinnedIDs.length === 0}
            onClick={() => closeFromMenu(closeUnpinnedIDs)}
          >
            <IconClose size={14} />
            <span>{t("drawer.tabs.closeUnpinned")}</span>
          </button>
          <div className="tab-context-separator" role="separator" />
          <button
            type="button"
            role="menuitem"
            onClick={() => {
              setTabContextMenu(null);
              void copyText(contextRelativePath).catch(reportError);
            }}
          >
            <IconCopy size={14} />
            <span>{t("drawer.tabs.copyRelative")}</span>
          </button>
          <button
            type="button"
            role="menuitem"
            disabled={!contextFullPath}
            onClick={() => {
              setTabContextMenu(null);
              void copyText(contextFullPath).catch(reportError);
            }}
          >
            <IconCopy size={14} />
            <span>{t("drawer.tabs.copyFull")}</span>
          </button>
          <button
            type="button"
            role="menuitem"
            disabled={!contextFolderPath}
            onClick={() => {
              setTabContextMenu(null);
              void api.openFolder(contextFolderPath).catch(reportError);
            }}
          >
            <IconFolderOpen size={14} />
            <span>{t("drawer.tabs.openFolder")}</span>
          </button>
        </div>
      )}
    </>
  );
}
