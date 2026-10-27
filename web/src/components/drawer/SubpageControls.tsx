import type { PageFormat } from "../../api";
import {
  IconChevronDown,
  IconDoc,
  IconImport,
  IconPages,
  IconPlus,
  IconPopout,
  IconTrash,
} from "../../icons";
import { DrawerMenu } from "./DrawerMenu";
import { PAGE_FORMATS } from "./pageFormats";
import type { NodePages } from "./useNodePages";
import { useI18n } from "../../i18n";

/**
 * Which page of the node the editor shows, as one compact button: the page's
 * name, opening a menu of the node's pages and the page actions — add a
 * subpage, import a file as one, pop the page out into its own window. A node
 * with no subpages, which is most of them, pays one button for all of it.
 */
export function SubpageMenu({
  pages: subpages,
  onPopout,
}: {
  pages: NodePages;
  onPopout: () => void;
}) {
  const { t } = useI18n();
  const {
    pages,
    pagesLoading,
    activePageID,
    pageBusy,
    pageImportInputRef,
    setPageCreateOpen,
    selectPage,
    importPage,
  } = subpages;
  const current = pages.find((page) => page.id === activePageID);
  const currentTitle = current?.title ?? t("subpage.main");

  return (
    <>
      <DrawerMenu
        label={t("subpage.tabs")}
        triggerTitle={t("drawer.pages.switch", { page: currentTitle })}
        trigger={
          <>
            {activePageID ? <IconPages size={13} /> : <IconDoc size={13} />}
            <span className="content-page-current">{currentTitle}</span>
            {pages.length > 0 && <span className="content-page-count">{pages.length + 1}</span>}
            <IconChevronDown size={12} />
          </>
        }
        triggerClassName="content-page-trigger"
        items={[
          {
            id: "main",
            label: t("subpage.main"),
            checked: activePageID === null,
            onSelect: () => void selectPage(null),
          },
          ...pages.map((page) => ({
            id: `page-${page.id}`,
            label: page.title,
            hint:
              (page.format ?? "md") !== "md" ? t(`format.${page.format ?? "md"}`) : undefined,
            checked: activePageID === page.id,
            onSelect: () => void selectPage(page.id),
          })),
          {
            id: "add",
            separatorBefore: true,
            label: t("subpage.add"),
            hint: pagesLoading ? t("subpage.loading") : undefined,
            icon: <IconPlus size={14} />,
            onSelect: () => setPageCreateOpen(true),
          },
          {
            id: "import",
            label: t("subpage.import"),
            title: t("subpage.importTitle"),
            icon: <IconImport size={14} />,
            disabled: pageBusy,
            onSelect: () => pageImportInputRef.current?.click(),
          },
          {
            id: "popout",
            label: t("subpage.popout"),
            title: t("subpage.popoutTitle"),
            icon: <IconPopout size={14} />,
            onSelect: onPopout,
          },
        ]}
      />
      <input
        ref={pageImportInputRef}
        type="file"
        hidden
        accept=".md,.markdown,.txt,.html,.htm,.docx"
        onChange={(event) => void importPage(event.target.files)}
      />
    </>
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
