import { useRef, type KeyboardEvent } from "react";
import type { PageFormat } from "../../api";
import { IconImport, IconPages, IconPlus, IconPopout, IconTrash } from "../../icons";
import { PAGE_FORMATS } from "./pageFormats";
import type { NodePages } from "./useNodePages";
import { useI18n } from "../../i18n";

/**
 * A node's pages as tabs in plain sight — the main page, then each subpage —
 * with adding a subpage and popping the page out as buttons beside them.
 * Nothing hides behind a menu; importing a file is offered in the
 * new-subpage form.
 */
export function SubpageMenu({
  pages: subpages,
  onPopout,
}: {
  pages: NodePages;
  onPopout: () => void;
}) {
  const { t } = useI18n();
  const { pages, pagesLoading, activePageID, setPageCreateOpen, selectPage, pageImportInputRef, importPage } =
    subpages;
  const listRef = useRef<HTMLDivElement>(null);
  const tabs: { id: string | null; title: string; hint?: string }[] = [
    { id: null, title: t("subpage.main") },
    ...pages.map((page) => ({
      id: page.id,
      title: page.title,
      hint: (page.format ?? "md") !== "md" ? t(`format.${page.format ?? "md"}`) : undefined,
    })),
  ];

  // Arrow keys move between tabs, Home and End to the ends.
  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    const all = Array.from(listRef.current?.querySelectorAll<HTMLButtonElement>('[role="tab"]') ?? []);
    const at = all.indexOf(document.activeElement as HTMLButtonElement);
    if (at < 0) return;
    const next =
      event.key === "ArrowRight" ? (at + 1) % all.length
      : event.key === "ArrowLeft" ? (at - 1 + all.length) % all.length
      : event.key === "Home" ? 0
      : event.key === "End" ? all.length - 1
      : -1;
    if (next < 0) return;
    event.preventDefault();
    all[next]?.focus();
  };

  return (
    <div className="content-page-bar">
      <div
        ref={listRef}
        className="content-page-tabs"
        role="tablist"
        aria-label={t("subpage.tabs")}
        onKeyDown={onKeyDown}
      >
        {tabs.map((tab) => {
          const selected = activePageID === tab.id;
          return (
            <button
              key={tab.id ?? "main"}
              type="button"
              role="tab"
              aria-selected={selected}
              tabIndex={selected ? 0 : -1}
              className={`content-page-tab${selected ? " active" : ""}`}
              title={tab.hint ? `${tab.title} (${tab.hint})` : tab.title}
              onClick={() => void selectPage(tab.id)}
            >
              <span className="content-page-current">{tab.title}</span>
            </button>
          );
        })}
      </div>
      <button
        type="button"
        className="icon-btn content-page-action"
        aria-label={t("subpage.add")}
        title={pagesLoading ? t("subpage.loading") : t("subpage.add")}
        onClick={() => setPageCreateOpen(true)}
      >
        <IconPlus size={14} />
      </button>
      <button
        type="button"
        className="icon-btn content-page-action"
        aria-label={t("subpage.popout")}
        title={t("subpage.popoutTitle")}
        onClick={onPopout}
      >
        <IconPopout size={14} />
      </button>
      <input
        ref={pageImportInputRef}
        type="file"
        hidden
        accept=".md,.markdown,.txt,.html,.htm,.docx"
        onChange={(event) => void importPage(event.target.files)}
      />
    </div>
  );
}

/**
 * What appears under the page switcher when there is something to do with a
 * page: managing the open subpage, the new-subpage form, a failure, a conflict.
 */
export function SubpageControls({ pages: subpages }: { pages: NodePages }) {
  const { t } = useI18n();
  const {
    pages,
    activePageID,
    activeFormat,
    pageDoc,
    setPageDoc,
    pageError,
    setPageError,
    pageCreateOpen,
    setPageCreateOpen,
    pageTitle,
    setPageTitle,
    pageFormat,
    setPageFormat,
    pageBusy,
    pageRename,
    setPageRename,
    pageImportInputRef,
    createPage,
    convertPage,
    renamePage,
    movePage,
    removePage,
  } = subpages;

  return (
    <>
      {activePageID && (
        <div className="content-page-manage">
          <input
            value={pageRename}
            maxLength={256}
            aria-label={t("subpage.rename")}
            onChange={(event) => setPageRename(event.target.value)}
            onBlur={() => void renamePage()}
            onKeyDown={(event) => {
              if (event.key === "Enter") event.currentTarget.blur();
              if (event.key === "Escape") {
                setPageRename(
                  pages.find((page) => page.id === activePageID)?.title ?? "",
                );
                event.currentTarget.blur();
              }
            }}
          />
          <button
            type="button"
            disabled={pageBusy || pages[0]?.id === activePageID}
            onClick={() => void movePage(-1)}
            title={t("subpage.moveLeft")}
          >
            ←
          </button>
          <button
            type="button"
            disabled={pageBusy || pages.at(-1)?.id === activePageID}
            onClick={() => void movePage(1)}
            title={t("subpage.moveRight")}
          >
            →
          </button>
          <select
            className="content-page-format-select"
            aria-label={t("subpage.format")}
            title={t("subpage.convert")}
            value={activeFormat}
            disabled={pageBusy}
            onChange={(event) =>
              void convertPage(event.target.value as PageFormat)
            }
          >
            {PAGE_FORMATS.map(([format, _label, extension]) => (
              <option key={format} value={format}>
                {t("common.withHint", { label: t(`format.${format}`), hint: extension })}
              </option>
            ))}
          </select>
          <button
            type="button"
            className="danger"
            disabled={pageBusy}
            onClick={() => void removePage()}
            title={t("subpage.delete")}
          >
            <IconTrash size={13} />
          </button>
        </div>
      )}

      {pageCreateOpen && (
        <form
          className="content-page-create"
          onSubmit={(event) => {
            event.preventDefault();
            void createPage();
          }}
        >
          <IconPages size={16} />
          <input
            autoFocus
            maxLength={256}
            value={pageTitle}
            onChange={(event) => setPageTitle(event.target.value)}
            placeholder={t("subpage.titlePlaceholder")}
            aria-label={t("subpage.titlePlaceholder")}
          />
          <select
            aria-label={t("subpage.format")}
            value={pageFormat}
            onChange={(event) =>
              setPageFormat(event.target.value as PageFormat)
            }
          >
            {PAGE_FORMATS.map(([format, _label, extension]) => (
              <option key={format} value={format}>
                {t("common.withHint", { label: t(`format.${format}`), hint: extension })}
              </option>
            ))}
          </select>
          <button type="submit" disabled={!pageTitle.trim() || pageBusy}>
            {pageBusy ? t("subpage.creating") : t("subpage.create")}
          </button>
          <button
            type="button"
            className="ghost"
            aria-label={t("subpage.import")}
            title={t("subpage.importTitle")}
            disabled={pageBusy}
            onClick={() => pageImportInputRef.current?.click()}
          >
            <IconImport size={13} /> {t("subpage.importShort")}
          </button>
          <button
            type="button"
            className="ghost"
            onClick={() => {
              setPageCreateOpen(false);
              setPageTitle("");
            }}
          >
            {t("subpage.cancel")}
          </button>
          {pageFormat === "docx" && (
            <p className="content-page-create-note">
              {t("subpage.docxNote")}
            </p>
          )}
          {pageFormat === "html" && (
            <p className="content-page-create-note">
              {t("subpage.htmlNote")}
            </p>
          )}
        </form>
      )}

      {pageError && (
        <div className="banner banner-warn content-page-error">
          <span>{pageError}</span>
          <button type="button" onClick={() => setPageError(null)}>
            {t("subpage.close")}
          </button>
        </div>
      )}

      {pageDoc?.conflict && (
        <div className="banner banner-conflict">
          <div className="banner-text">
            <b>{t("subpage.conflictTitle")}</b> {t("subpage.conflictDescription")}
          </div>
          <div className="banner-actions">
            <button
              type="button"
              onClick={() =>
                setPageDoc((current) =>
                  current?.conflict
                    ? {
                        ...current,
                        content: current.conflict.diskContent,
                        rev: current.conflict.diskRev,
                        dirty: false,
                        conflict: null,
                      }
                    : current,
                )
              }
            >
              {t("subpage.loadDisk")}
            </button>
            <button
              type="button"
              onClick={() =>
                setPageDoc((current) =>
                  current?.conflict
                    ? {
                        ...current,
                        rev: current.conflict.diskRev,
                        dirty: true,
                        conflict: null,
                      }
                    : current,
                )
              }
            >
              {t("subpage.keepMine")}
            </button>
          </div>
        </div>
      )}
    </>
  );
}
