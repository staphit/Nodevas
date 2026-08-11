import { useCallback, useRef, useState, type RefObject } from "react";

import { api, type PageFormat } from "../../api";
import { reportError } from "../../store";
import { attachmentSnippet } from "./pageFormats";

export type Attachments = {
  attachBusy: boolean;
  attachInputRef: RefObject<HTMLInputElement>;
  attachFiles: (files: FileList | null) => Promise<void>;
};

/** Uploads picked files to the node and writes a link to each into the page. */
export function useAttachments({
  nodeId,
  format,
  insertSnippet,
}: {
  nodeId: string;
  format: PageFormat;
  insertSnippet: (snippet: string) => void;
}): Attachments {
  const attachInputRef = useRef<HTMLInputElement>(null);
  const [attachBusy, setAttachBusy] = useState(false);
  const attachFiles = useCallback(
    async (files: FileList | null) => {
      if (!files?.length) return;
      setAttachBusy(true);
      try {
        for (const file of Array.from(files)) {
          const uploaded = await api.uploadNodeFile(nodeId, file);
          const isImage = /\.(png|jpe?g|gif|webp|avif|bmp)$/i.test(uploaded.name);
          insertSnippet(attachmentSnippet(format, uploaded, isImage));
        }
      } catch (error) {
        reportError(error);
      } finally {
        setAttachBusy(false);
        if (attachInputRef.current) attachInputRef.current.value = "";
      }
    },
    [nodeId, insertSnippet],
  );

  return { attachBusy, attachInputRef, attachFiles };
}
