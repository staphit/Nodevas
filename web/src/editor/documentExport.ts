/**
 * Saving a document as something other than Markdown.
 *
 * The server does the conversion; this module only moves the result into the
 * browser — a download for real files, the print dialog for PDF, which is the
 * one renderer on the machine that already has the document's fonts.
 */

import { api, type ExportRequest } from "../api";

/** Downloads one export; resolves with the file name the server chose. */
export async function downloadExport(request: ExportRequest): Promise<string> {
  const file = await api.exportDocument(request);
  const url = URL.createObjectURL(file.blob);
  const anchor = document.createElement("a");
  anchor.href = url;
  anchor.download = file.name;
  document.body.appendChild(anchor);
  anchor.click();
  anchor.remove();
  // Revoking straight away cancels the download in some browsers.
  window.setTimeout(() => URL.revokeObjectURL(url), 60_000);
  return file.name;
}

/**
 * Renders the export as HTML in an offscreen frame and opens the print
 * dialog, where the user picks "Save as PDF".
 */
export async function printExport(
  request: Omit<ExportRequest, "format">,
): Promise<void> {
  const file = await api.exportDocument({ ...request, format: "html" });
  const html = await file.blob.text();
  const frame = document.createElement("iframe");
  frame.setAttribute("aria-hidden", "true");
  // The frame renders the server's rendering of arbitrary document content, so
  // it is sandboxed rather than trusted. The load-bearing part is the *absence*
  // of allow-scripts: with that flag unset the browser refuses to run any
  // script in the frame — inline, event handler or external — so the app CSP
  // stops being the only thing between an exported document and this origin.
  // The sandbox also blocks forms, popups, downloads, plugins and top-level
  // navigation out of the app.
  //
  // allow-same-origin is deliberate and safe *because* allow-scripts is absent:
  // there is no code in the frame that could make use of the origin, and a
  // script-free frame cannot reach out and strip its own sandbox attribute.
  // What it does buy is a same-origin contentWindow, which is the only way this
  // document can reach print() at all — print() is not exposed on a cross-origin
  // Window, so an opaque-origin frame here would silently break PDF export.
  // (A blob: URL is not an option either: the app CSP sets frame-src 'none',
  // which blocks blob: frames while leaving srcdoc — a local scheme — allowed.)
  // allow-modals is what permits the print dialog to open.
  frame.setAttribute("sandbox", "allow-same-origin allow-modals");
  frame.style.cssText =
    "position:fixed;right:0;bottom:0;width:0;height:0;border:0;visibility:hidden";
  // srcdoc is set before insertion so the sandbox governs the very first
  // document the frame ever holds; there is no about:blank window to write into.
  frame.srcdoc = html;
  try {
    await new Promise<void>((resolve) => {
      frame.addEventListener("load", () => resolve(), { once: true });
      document.body.appendChild(frame);
      window.setTimeout(resolve, 2000);
    });
    const target = frame.contentWindow;
    if (!target) throw new Error("無法建立列印視窗");
    const dispose = () => frame.remove();
    target.addEventListener("afterprint", dispose, { once: true });
    // A user who cancels the dialog never fires afterprint on every browser.
    window.setTimeout(dispose, 120_000);
    target.focus();
    target.print();
  } catch (error) {
    frame.remove();
    throw error;
  }
}
