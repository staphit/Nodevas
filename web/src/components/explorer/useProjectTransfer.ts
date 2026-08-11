/**
 * Moving whole projects in and out of the workspace [B-06].
 *
 * Everything the explorer can import or export: project archives, Markdown
 * files, JSON Canvas graphs and whole-project documents. The file inputs live
 * here because both the transfer bar and the workspace context menu open them.
 */

import { useRef, useState } from "react";
import { api, type ExportFormat } from "../../api";
import { downloadExport, printExport } from "../../editor/documentExport";
import { reportError, useApp } from "../../store";
import { edgeRelation } from "../../domain/graph/edgeStyle";
import type { Graph, GraphNode } from "../../types";
import type { ImportBundleChoice } from "./ImportBundleDialog";
import { readBundleManifest, type BundleManifest } from "./projectArchive";

/** The three things the explorer can pull in, each with its own file input. */
export type ImportKind = "archive" | "markdown" | "canvas";

function quickFrontmatter(text: string): { id?: string } {
  const match = text.match(/^---\r?\n([\s\S]*?)\r?\n---/);
  if (!match) return {};
  const result: { id?: string } = {};
  for (const line of match[1].split(/\r?\n/)) {
    const keyValue = line.match(/^id:\s*(.+)\s*$/);
    if (keyValue) result.id = keyValue[1].replace(/^["']|["']$/g, "");
  }
  return result;
}

function stripFrontmatter(text: string): string {
  return text.replace(/^---\r?\n[\s\S]*?\r?\n---(?:\r?\n)?/, "");
}

export function useProjectTransfer({
  graph,
  activeProject,
  expandedProjects,
  persistExpandedProjects,
  setProjectTransferNotice,
}: {
  graph: Graph | null;
  activeProject: string;
  expandedProjects: Set<string>;
  persistExpandedProjects: (next: Set<string>) => void;
  setProjectTransferNotice: (notice: string | null) => void;
}) {
  const switchProject = useApp((state) => state.switchProject);
  const loadAll = useApp((state) => state.loadAll);
  const runGraphCommand = useApp((state) => state.runGraphCommand);
  const importInputRef = useRef<HTMLInputElement>(null);
  const markdownImportInputRef = useRef<HTMLInputElement>(null);
  const jsonCanvasImportInputRef = useRef<HTMLInputElement>(null);
  const [markdownImportBusy, setMarkdownImportBusy] = useState(false);
  const [jsonCanvasBusy, setJsonCanvasBusy] = useState(false);
  const [documentExportBusy, setDocumentExportBusy] = useState(false);
  const importMenuRef = useRef<HTMLDetailsElement>(null);
  const exportMenuRef = useRef<HTMLDetailsElement>(null);
  const [projectTransferBusy, setProjectTransferBusy] = useState(false);
  // Where the next import should land. The transfer bar names nothing and gets
  // the active project; the explorer's row menu names the row right-clicked.
  const [importTarget, setImportTarget] = useState("");
  // Held between picking the file and answering the dialog: the upload has not
  // started yet, so the File — not the input, which is cleared straight away —
  // is what the confirmed choice is applied to.
  const [pendingBundleImport, setPendingBundleImport] = useState<{
    file: File;
    manifest: BundleManifest;
  } | null>(null);

  // A folder, or a project with sub-projects, comes back as a bundle holding
  // the whole subtree; the server decides, the caller only names the root.
  const exportProjectArchive = (target?: { name: string; label: string }) => {
    const anchor = document.createElement("a");
    anchor.href = api.projectExportURL(target?.name);
    anchor.download = "";
    document.body.appendChild(anchor);
    anchor.click();
    anchor.remove();
    setProjectTransferNotice(
      `正在匯出 ${target?.label || activeProject || "目前專案"}…`,
    );
  };

  const closeTransferMenus = () => {
    importMenuRef.current?.removeAttribute("open");
    exportMenuRef.current?.removeAttribute("open");
  };

  /**
   * Opens one of the file pickers, remembering which project the result should
   * land in. Empty — what the transfer bar uses — means the active project, so
   * the bar keeps the behaviour it has always had.
   */
  const beginImport = (target: string, kind: ImportKind) => {
    setImportTarget(target);
    const input =
      kind === "archive"
        ? importInputRef
        : kind === "markdown"
          ? markdownImportInputRef
          : jsonCanvasImportInputRef;
    input.current?.click();
  };

  /**
   * Markdown and Canvas imports write nodes through whichever project is
   * active, so landing them elsewhere means switching first. That is also the
   * kinder outcome: the user asked for this row, and ends up looking at what
   * was imported rather than at the project they started from.
   */
  const enterImportTarget = async (): Promise<boolean> => {
    if (!importTarget || importTarget === activeProject) return true;
    try {
      await switchProject(importTarget);
      return true;
    } catch (error) {
      setProjectTransferNotice(
        `無法開啟 ${importTarget}：${(error as Error).message}`,
      );
      reportError(error);
      return false;
    }
  };

  // The whole project as one readable document, next to the archive formats
  // — the editor toolbar only reaches the node it has open.
  const exportProjectDocument = async (choice: ExportFormat | "pdf") => {
    if (documentExportBusy) return;
    setDocumentExportBusy(true);
    setProjectTransferNotice(`正在匯出整個專案的文件…`);
    try {
      if (choice === "pdf") {
        await printExport({ scope: "project" });
        setProjectTransferNotice("已開啟列印視窗，在目的地選擇「另存為 PDF」");
      } else {
        const name = await downloadExport({ format: choice, scope: "project" });
        setProjectTransferNotice(`已匯出 ${name}`);
      }
    } catch (error) {
      setProjectTransferNotice(`文件匯出失敗：${(error as Error).message}`);
    } finally {
      setDocumentExportBusy(false);
    }
  };

  const runImport = async (file: File, choice?: ImportBundleChoice) => {
    setProjectTransferBusy(true);
    setProjectTransferNotice(`正在匯入 ${file.name}…`);
    try {
      const imported = await api.importProject(
        file,
        choice?.mode === "folder" ? choice.name : undefined,
        choice?.mode,
        importTarget,
      );
      await switchProject(imported.active);
      const next = new Set(expandedProjects);
      next.add(imported.active);
      persistExpandedProjects(next);
      setProjectTransferNotice(`已匯入為 ${imported.active}`);
    } catch (error) {
      setProjectTransferNotice(`匯入失敗：${(error as Error).message}`);
      reportError(error);
    } finally {
      setProjectTransferBusy(false);
      setImportTarget("");
    }
  };

  const importProject = async (file: File) => {
    if (projectTransferBusy || pendingBundleImport) return;
    // Only a bundle has anywhere else to go, and only the file itself can say
    // whether it is one. A single project — or an archive whose head we cannot
    // read — goes straight up, with no mode and no question asked.
    const manifest = await readBundleManifest(file);
    // The input is cleared as soon as the file is in hand rather than after the
    // upload, so that cancelling the dialog and picking the same file again
    // still fires a change event.
    if (importInputRef.current) importInputRef.current.value = "";
    if (manifest) {
      setPendingBundleImport({ file, manifest });
      return;
    }
    await runImport(file);
  };

  const confirmBundleImport = async (choice: ImportBundleChoice) => {
    const pending = pendingBundleImport;
    if (!pending) return;
    setPendingBundleImport(null);
    await runImport(pending.file, choice);
  };

  const importMarkdownFiles = async (files: FileList | null) => {
    if (!files?.length || markdownImportBusy) return;
    if (!(await enterImportTarget())) {
      setImportTarget("");
      return;
    }
    setMarkdownImportBusy(true);
    const importedIds: string[] = [];
    const failures: string[] = [];
    try {
      for (const file of Array.from(files)) {
        const text = await file.text();
        const metadata = quickFrontmatter(text);
        const fallback = file.name
          .replace(/\.md$/i, "")
          .replace(/[^A-Za-z0-9_.-]+/g, "-")
          .replace(/^[^A-Za-z_]+/, "n-");
        const id = metadata.id ?? (fallback || `import-${Date.now()}`);
        try {
          await api.createNode({ id }, text);
          importedIds.push(id);
        } catch (error) {
          failures.push(`${file.name}: ${(error as Error).message}`);
        }
      }
      await loadAll();
      for (const id of importedIds) {
        const requires =
          useApp.getState().graph?.nodes?.find((node) => node.id === id)?.requires ?? "";
        if (requires) await useApp.getState().commitRequires(id, requires);
      }
      if (failures.length > 0) {
        setProjectTransferNotice(
          `已匯入 ${importedIds.length} 個節點；${failures.length} 個失敗`,
        );
        reportError(new Error(`Markdown 匯入失敗：${failures.join("；")}`));
      } else {
        setProjectTransferNotice(`已匯入 ${importedIds.length} 個 Markdown 節點`);
      }
    } finally {
      setMarkdownImportBusy(false);
      setImportTarget("");
      if (markdownImportInputRef.current) {
        markdownImportInputRef.current.value = "";
      }
    }
  };

  const exportJSONCanvas = async () => {
    if (!graph || jsonCanvasBusy) return;
    setJsonCanvasBusy(true);
    try {
      const positions = graph.ui?.positions ?? {};
      const styles = graph.ui?.nodeStyles ?? {};
      const nodes = await Promise.all(
        (graph.nodes ?? []).map(async (node, index) => {
          const position = positions[node.id] ?? { x: index % 6, y: Math.floor(index / 6) };
          const style = styles[node.id] ?? {};
          let text = `# ${node.title || node.id}`;
          try {
            text = (await api.getNode(node.id)).content;
          } catch {
            // Metadata-only export remains a valid JSON Canvas file.
          }
          return {
            id: node.id,
            type: "text",
            x: Math.round(position.x * 164),
            y: Math.round(position.y * 80),
            width: Math.round(style.width ?? 240),
            height: Math.round(style.height ?? 120),
            text,
            ...(style.color ? { color: style.color } : {}),
            vised: {
              title: node.title,
              kind: node.kind,
              priority: node.priority,
              assignee: node.assignee,
              tags: node.tags,
            },
          };
        }),
      );
      const edges = (graph.edges ?? []).map((edge, index) => ({
        id: `edge-${index + 1}`,
        fromNode: edge.from,
        toNode: edge.to,
        fromSide: "right",
        toSide: "left",
        ...(edgeRelation(edge) ? { label: edgeRelation(edge) } : {}),
      }));
      const blob = new Blob([JSON.stringify({ nodes, edges }, null, 2)], {
        type: "application/json",
      });
      const url = URL.createObjectURL(blob);
      const anchor = document.createElement("a");
      anchor.href = url;
      anchor.download = `${(activeProject || "project").split("/").at(-1)}.canvas`;
      anchor.click();
      URL.revokeObjectURL(url);
      setProjectTransferNotice("已匯出 JSON Canvas（.canvas）");
    } catch (error) {
      reportError(error);
      setProjectTransferNotice(`JSON Canvas 匯出失敗：${(error as Error).message}`);
    } finally {
      setJsonCanvasBusy(false);
    }
  };

  const importJSONCanvas = async (file: File) => {
    if (jsonCanvasBusy) return;
    if (!(await enterImportTarget())) {
      setImportTarget("");
      return;
    }
    setJsonCanvasBusy(true);
    try {
      const parsed = JSON.parse(await file.text()) as {
        nodes?: Array<{
          id: string;
          type: string;
          x: number;
          y: number;
          width?: number;
          height?: number;
          text?: string;
          color?: string;
          vised?: {
            title?: string;
            kind?: string;
            priority?: string;
            assignee?: string;
            tags?: string[];
          };
        }>;
        edges?: Array<{ fromNode: string; toNode: string; label?: string }>;
      };
      const canvasNodes = (parsed.nodes ?? []).filter(
        (node) =>
          node &&
          typeof node.id === "string" &&
          Number.isFinite(node.x) &&
          Number.isFinite(node.y),
      );
      if (!canvasNodes.length) throw new Error("檔案沒有可匯入的節點。");
      if (canvasNodes.length > 1000) throw new Error("單次最多匯入 1000 個節點。");
      const idMap = new Map<string, string>();
      const imported: Array<{
        source: (typeof canvasNodes)[number];
        id: string;
      }> = [];
      for (const source of canvasNodes) {
        const heading = source.text?.match(/^#\s+(.+)$/m)?.[1]?.trim();
        const created = await api.createNode(
          {
            title: source.vised?.title || heading || "未命名節點",
            kind: source.vised?.kind || "task",
          },
          stripFrontmatter(source.text || ""),
        );
        const id = created.id;
        idMap.set(source.id, id);
        imported.push({ source, id });
      }
      await loadAll();
      // One command for the whole import: one revision, one undo step [A-04].
      const result = await runGraphCommand({
        type: "graph.applyImport",
        payload: {
          nodes: imported.map(({ source, id }) => ({
            id,
            position: {
              x: source.x / 164,
              y: source.y / 80,
            },
            style: {
              width: source.width,
              height: source.height,
              color: source.color,
            },
            priority: source.vised?.priority as GraphNode["priority"],
            assignee: source.vised?.assignee,
            tags: source.vised?.tags,
          })),
          edges: (parsed.edges ?? []).flatMap((edge) => {
            const from = idMap.get(edge.fromNode);
            const to = idMap.get(edge.toNode);
            if (!from || !to || from === to) return [];
            const relation =
              edge.label === "optional" || edge.label === "deprecated"
                ? edge.label
                : "";
            return [{ from, to, relation }];
          }),
        },
      });
      if (!result.ok) throw new Error(result.message);
      setProjectTransferNotice(`已從 JSON Canvas 匯入 ${imported.length} 個節點`);
    } catch (error) {
      reportError(error);
      setProjectTransferNotice(`JSON Canvas 匯入失敗：${(error as Error).message}`);
    } finally {
      setJsonCanvasBusy(false);
      setImportTarget("");
      if (jsonCanvasImportInputRef.current) jsonCanvasImportInputRef.current.value = "";
    }
  };

  return {
    importInputRef,
    markdownImportInputRef,
    jsonCanvasImportInputRef,
    importMenuRef,
    exportMenuRef,
    projectTransferBusy,
    markdownImportBusy,
    jsonCanvasBusy,
    documentExportBusy,
    closeTransferMenus,
    beginImport,
    importTarget,
    exportProjectArchive,
    exportProjectDocument,
    importProject,
    pendingBundleImport,
    confirmBundleImport,
    cancelBundleImport: () => setPendingBundleImport(null),
    importMarkdownFiles,
    exportJSONCanvas,
    importJSONCanvas,
  };
}

/** Everything the transfer bar and the workspace menu need to drive an import
 * or an export. */
export type ProjectTransfer = ReturnType<typeof useProjectTransfer>;
