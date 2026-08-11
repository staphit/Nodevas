import type { PageFormat } from "../../api";
import {
  IconDoc,
  IconImport,
  IconPages,
  IconPlus,
  IconPopout,
  IconTrash,
} from "../../icons";
import { PAGE_FORMAT_LABEL, PAGE_FORMATS } from "./pageFormats";
import type { NodePages } from "./useNodePages";

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
        <div className="content-page-tabs" role="tablist" aria-label="內容頁面">
          <button
            type="button"
            role="tab"
            aria-selected={activePageID === null}
            className={activePageID === null ? "active" : ""}
            onClick={() => void selectPage(null)}
          >
            <IconDoc size={13} />
            主頁
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
                  {PAGE_FORMAT_LABEL[page.format ?? "md"]}
                </span>
              )}
            </button>
          ))}
          {pagesLoading && <span className="content-page-loading">讀取頁面…</span>}
        </div>
        {canEdit && (
          <>
            <button
              type="button"
              className="content-page-action"
              aria-label="新增子頁"
              title="新增子頁"
              onClick={() => setPageCreateOpen((open) => !open)}
            >
              <IconPlus size={15} />
            </button>
            <button
              type="button"
              className="content-page-action"
              aria-label="匯入檔案為子頁"
              title="把 .md / .txt / .html / .docx 檔加入為子頁"
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
          aria-label="彈出目前頁面"
          title="在獨立視窗編輯目前頁面"
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
            aria-label="重新命名目前子頁"
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
            title="向左移動子頁"
          >
            ←
          </button>
          <button
            type="button"
            disabled={pageBusy || pages.at(-1)?.id === activePageID}
            onClick={() => void movePage(1)}
            title="向右移動子頁"
          >
            →
          </button>
          <select
            className="content-page-format-select"
            aria-label="子頁檔案格式"
            title="轉換這一頁的檔案格式"
            value={activeFormat}
            disabled={pageBusy}
            onChange={(event) =>
              void convertPage(event.target.value as PageFormat)
            }
          >
            {PAGE_FORMATS.map(([format, label, extension]) => (
              <option key={format} value={format}>
                {label}（{extension}）
              </option>
            ))}
          </select>
          <button
            type="button"
            className="danger"
            disabled={pageBusy}
            onClick={() => void removePage()}
            title="刪除子頁"
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
            placeholder="子頁標題，例如：角色設定"
            aria-label="子頁標題"
          />
          <select
            aria-label="子頁檔案格式"
            value={pageFormat}
            onChange={(event) =>
              setPageFormat(event.target.value as PageFormat)
            }
          >
            {PAGE_FORMATS.map(([format, label, extension]) => (
              <option key={format} value={format}>
                {label}（{extension}）
              </option>
            ))}
          </select>
          <button type="submit" disabled={!pageTitle.trim() || pageBusy}>
            {pageBusy ? "建立中…" : "建立頁面"}
          </button>
          <button
            type="button"
            className="ghost"
            onClick={() => {
              setPageCreateOpen(false);
              setPageTitle("");
            }}
          >
            取消
          </button>
          {pageFormat === "docx" && (
            <p className="content-page-create-note">
              Word 頁面以 Markdown 編輯，存檔時重新產生 .docx。
              在 Word 端做的進階排版（欄位、頁首頁尾、追蹤修訂）會在下次存檔時流失。
            </p>
          )}
          {pageFormat === "html" && (
            <p className="content-page-create-note">
              HTML 頁面直接編輯原始碼，預覽會渲染它。
            </p>
          )}
        </form>
      )}

      {pageError && (
        <div className="banner banner-warn content-page-error">
          <span>{pageError}</span>
          <button type="button" onClick={() => setPageError(null)}>
            關閉
          </button>
        </div>
      )}

      {pageDoc?.conflict && (
        <div className="banner banner-conflict">
          <div className="banner-text">
            <b>子頁已有較新的版本。</b>
            可載入磁碟內容，或以目前內容覆寫。
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
              載入磁碟版本
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
              保留我的內容
            </button>
          </div>
        </div>
      )}
    </>
  );
}
