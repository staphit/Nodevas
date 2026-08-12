import type { PageFormat } from "../../api";
import {
  IconDoc,
  IconImport,
  IconPages,
  IconPlus,
  IconPopout,
  IconTrash,
} from "../../icons";
import { PAGE_FORMATS } from "./pageFormats";
import type { NodePages } from "./useNodePages";
import { useI18n } from "../../i18n";

/** Everything above the editor that picks, creates or edits a subpage. */
export function SubpageControls({
  pages: subpages,
  onPopout,
  canEdit = true,
}: {
  pages: NodePages;
  onPopout: () => void;
  /**
   * False for a read-only session. The page tabs stay — switching pages is
   * reading — while everything that renames, reorders, converts, creates,
   * imports or deletes a page is not rendered. Not rendered rather than
   * disabled, because a strip of dead buttons next to live tabs reads as
   * broken, not as read-only.
   */
  canEdit?: boolean;
}) {
  const { t } = useI18n();
  const {
    pages,
    pagesLoading,
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
    selectPage,
    createPage,
    importPage,
    convertPage,
    renamePage,
    movePage,
    removePage,
  } = subpages;

  return (
    <>
      <div className="content-page-bar">
        <div className="content-page-tabs" role="tablist" aria-label={t("subpage.tabs")}>
          <button
            type="button"
            role="tab"
            aria-selected={activePageID === null}
            className={activePageID === null ? "active" : ""}
            onClick={() => void selectPage(null)}
          >
            <IconDoc size={13} />
            {t("subpage.main")}
          </button>
          {pages.map((page) => (
            <button
              type="button"
              role="tab"
              key={page.id}
              aria-selected={activePageID === page.id}
              className={activePageID === page.id ? "active" : ""}
              onClick={() => void selectPage(page.id)}
              title={page.title}
            >
              <IconPages size={13} />
              {page.title}
              {(page.format ?? "md") !== "md" && (
                <span className="content-page-format">
                  {t(`format.${page.format ?? "md"}`)}
                </span>
              )}
            </button>
          ))}
          {pagesLoading && <span className="content-page-loading">{t("subpage.loading")}</span>}
        </div>
        {canEdit && (
          <>
            <button
              type="button"
              className="content-page-action"
              aria-label={t("subpage.add")}
              title={t("subpage.add")}
              onClick={() => setPageCreateOpen((open) => !open)}
            >
              <IconPlus size={15} />
            </button>
            <button
              type="button"
              className="content-page-action"
              aria-label={t("subpage.import")}
              title={t("subpage.importTitle")}
              disabled={pageBusy}
              onClick={() => pageImportInputRef.current?.click()}
            >
              <IconImport size={15} />
            </button>
            <input
              ref={pageImportInputRef}
              type="file"
              hidden
              accept=".md,.markdown,.txt,.html,.htm,.docx"
              onChange={(event) => void importPage(event.target.files)}
            />
          </>
        )}
        <button
          type="button"
          className="content-page-action"
          aria-label={t("subpage.popout")}
          title={t("subpage.popoutTitle")}
          onClick={onPopout}
        >
          <IconPopout size={15} />
        </button>
      </div>

      {canEdit && activePageID && (
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
                {t(`format.${format}`)} ({extension})
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

      {canEdit && pageCreateOpen && (
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
                {t(`format.${format}`)} ({extension})
              </option>
            ))}
          </select>
          <button type="submit" disabled={!pageTitle.trim() || pageBusy}>
            {pageBusy ? t("subpage.creating") : t("subpage.create")}
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
