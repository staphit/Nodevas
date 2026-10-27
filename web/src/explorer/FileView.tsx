import { useMemo, type ReactNode } from "react";
import { ExplorerError, explorerApi } from "../api/explorer";
import { openNodeDetail } from "../components/drawer/openNodeDetail";
import { useI18n } from "../i18n";
import { renderMarkdown } from "../markdown";
import { baseName, isMarkdown, sizeText } from "./explorerFormat";
import type { ExplorerView } from "./explorerStore";
import { useLoad } from "./useExplorerData";

export interface FileViewProps {
  view: Extract<ExplorerView, { kind: "file" }>;
}

/** What a file view shows, whichever side served it. */
interface Shown {
  path: string;
  size: number;
  kind: "text" | "image" | "binary";
  content: string;
  truncated: boolean;
  nodeId?: string;
  url?: string;
}

/** The cap the hub and the runner put on a file's text. */
const TEXT_CAP = 512 << 10;

/** Plain text with line numbers. */
export function CodeLines({ text }: { text: string }) {
  const lines = useMemo(() => {
    const all = text.split("\n");
    if (all.length > 1 && all[all.length - 1] === "") all.pop();
    return all;
  }, [text]);
  return (
    <div className="ex-code">
      {lines.map((line, i) => (
        <div key={i} className="ln">
          <span className="no">{i + 1}</span>
          <span className="tx">{line}</span>
        </div>
      ))}
    </div>
  );
}

function load(view: FileViewProps["view"]): Promise<Shown> {
  if (view.source === "workspace") {
    return explorerApi.workspaceFile(view.path).then((file) => ({
      path: file.path,
      size: file.size,
      kind: file.kind,
      content: file.content ?? "",
      truncated: file.truncated,
      nodeId: file.nodeId,
      url: file.url,
    }));
  }
  return explorerApi.repoFile(view.runnerId, view.repoId, view.path).then((file) => ({
    path: file.path,
    size: file.size,
    kind: file.binary ? "binary" : "text",
    content: file.content,
    truncated: file.truncated,
  }));
}

/** One file, read-only. */
export default function FileView({ view }: FileViewProps) {
  const { t } = useI18n();
  const file = useLoad(() => load(view), JSON.stringify(view));
  const shown = file.data;
  const html = useMemo(
    () => (shown && shown.kind === "text" && isMarkdown(shown.path) ? renderMarkdown(shown.content) : null),
    [shown],
  );

  let body: ReactNode = null;
  if (file.error) {
    const error = file.error;
    if (error instanceof ExplorerError && error.code === "HIDDEN_FILE") {
      body = <p className="ex-note">{t("explorer.file.hidden")}</p>;
    } else if (error instanceof ExplorerError && error.code === "RUNNER_OFFLINE" && view.source === "repo") {
      body = <p className="ex-note">{t("explorer.files.offlineNote", { runner: view.runnerName })}</p>;
    } else {
      body = <p className="ex-note">{error instanceof Error ? error.message : String(error)}</p>;
    }
  } else if (shown) {
    if (shown.kind === "image" && shown.url) {
      body = <img className="ex-img" src={shown.url} alt={shown.path} />;
    } else if (shown.kind === "binary" || shown.kind === "image") {
      body = <p className="ex-note">{t("explorer.file.binary")}</p>;
    } else {
      body = (
        <>
          {shown.truncated && (
            <p className="ex-note">{t("explorer.file.truncatedFile", { size: sizeText(TEXT_CAP) })}</p>
          )}
          {html !== null ? (
            <div className="ex-md" dangerouslySetInnerHTML={{ __html: html }} />
          ) : (
            <CodeLines text={shown.content} />
          )}
        </>
      );
    }
  }

  const nodeId = shown?.nodeId;
  return (
    <div className="ex-view">
      <div className="ex-mhead">
        <h1>{baseName(view.path)}</h1>
        <span className="ex-crumb">{view.path}</span>
        {view.source === "repo" && (
          <span className="ex-chip">{t("explorer.file.readOnly", { runner: view.runnerName })}</span>
        )}
        {view.source === "workspace" && nodeId && (
          <button type="button" className="ex-btn primary" onClick={() => openNodeDetail(nodeId)}>
            {t("explorer.file.openNode")}
          </button>
        )}
      </div>
      <div className="ex-mbody">{body}</div>
    </div>
  );
}
