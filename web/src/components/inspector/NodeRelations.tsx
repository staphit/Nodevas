/** Node relations panel [B-06]: incoming and outgoing dependencies. */

import { useEffect, useMemo, useState } from "react";
import { api } from "../../api";
import { topLevelOp, useApp } from "../../store";
import {
  LINE_LABELS,
  RELATION_LABELS,
  edgeLine,
  edgeRelation,
} from "../../domain/graph/edgeStyle";

const RELATION_OPERATOR_LABELS: Record<string, string> = {
  and: "全部成立 · AND",
  or: "任一成立 · OR",
  xor: "擇一成立 · XOR",
  nand: "非全部 · NAND",
  nor: "皆不成立 · NOR",
};

export function NodeRelations({ id }: { id: string }) {
  const graph = useApp((state) => state.graph);
  const openTab = useApp((state) => state.openTab);
  const openNodeLink = useApp((state) => state.openNodeLink);
  const activeProject = useApp((state) => state.activeProject);
  const nodes = graph?.nodes ?? [];
  const edges = graph?.edges ?? [];
  const node = nodes.find((item) => item.id === id);
  const byId = useMemo(
    () => new Map(nodes.map((item) => [item.id, item])),
    [nodes],
  );
  const incoming = edges.filter((edge) => edge.to === id);
  const outgoing = edges.filter((edge) => edge.from === id);
  const operator = topLevelOp(node?.requires);

  const relationList = (
    title: string,
    relations: typeof edges,
    direction: "incoming" | "outgoing",
  ) => (
    <section className="node-relations-section">
      <h3>
        {title}
        <span>{relations.length}</span>
      </h3>
      {relations.length === 0 ? (
        <div className="node-relations-empty">沒有關係</div>
      ) : (
        <div className="node-relations-list">
          {relations.map((edge) => {
            const relatedId = direction === "incoming" ? edge.from : edge.to;
            const related = byId.get(relatedId);
            return (
              <button
                type="button"
                className="node-relation-item"
                key={`${edge.from}->${edge.to}`}
                onClick={() => void openTab(relatedId).catch(reportError)}
              >
                <span className="node-relation-direction">
                  {direction === "incoming" ? "前置" : "後續"}
                </span>
                <span className="node-relation-copy">
                  <b>{related?.title || relatedId}</b>
                </span>
                <span
                  className={`node-relation-style${
                    edgeRelation(edge) ? ` relation-${edgeRelation(edge)}` : ""
                  }`}
                >
                  {`${LINE_LABELS[edgeLine(edge)]}／${
                    RELATION_LABELS[edgeRelation(edge)]
                  }`}
                </span>
              </button>
            );
          })}
        </div>
      )}
    </section>
  );

  return (
    <div className="node-relations" role="tabpanel" aria-label="關係圖">
      <div className="node-relations-summary">
        <div>
          <span>邏輯運算</span>
          <b>
            {operator
              ? RELATION_OPERATOR_LABELS[operator] ?? operator.toUpperCase()
              : incoming.length > 1
                ? "全部成立 · AND"
                : "單一條件"}
          </b>
        </div>
        <div>
          <span>關係數</span>
          <b>{incoming.length + outgoing.length}</b>
        </div>
      </div>
      {relationList("前置節點", incoming, "incoming")}
      {relationList("後續節點", outgoing, "outgoing")}
      <NodeLinkSection
        project={activeProject}
        nodeId={id}
        openNodeLink={openNodeLink}
      />
      <div className="node-relations-help">
        點擊節點可切換資訊頁；關係線樣式請在主關係圖右鍵修改。文件裡的
        <code>[[連結]]</code> 屬於參照，不影響前置條件。
      </div>
    </div>
  );
}

/**
 * Document links, in both directions [B-04].
 *
 * These are references written in the markdown, not dependencies: they may
 * point into another project, and they never gate anything. A link whose
 * target is gone is listed as broken rather than quietly dropped.
 */
function NodeLinkSection({
  project,
  nodeId,
  openNodeLink,
}: {
  project: string;
  nodeId: string;
  openNodeLink: (target: { project: string; nodeId: string }) => Promise<void>;
}) {
  const [links, setLinks] = useState<{
    backlinks: { fromProject: string; fromNode: string; fromTitle?: string }[];
    outgoing: { toProject: string; toNode: string; label?: string; missing: boolean }[];
  } | null>(null);
  const [failed, setFailed] = useState(false);

  useEffect(() => {
    if (!project || !nodeId) return;
    let cancelled = false;
    setLinks(null);
    setFailed(false);
    void api
      .getNodeLinks(project, nodeId)
      .then((result) => {
        if (!cancelled) setLinks(result);
      })
      .catch(() => {
        if (!cancelled) setFailed(true);
      });
    return () => {
      cancelled = true;
    };
  }, [project, nodeId]);

  const open = (target: { project: string; nodeId: string }) =>
    void openNodeLink(target).catch(reportError);

  return (
    <section className="node-relations-section">
      <h3>文件連結</h3>
      {failed && <p className="node-relations-empty">無法讀取連結。</p>}
      {!failed && !links && <p className="node-relations-empty">讀取中…</p>}
      {links && (
        <div className="node-relations-list">
          {links.outgoing.map((link) => (
            <button
              type="button"
              className="node-relation-item"
              key={`out-${link.toProject}/${link.toNode}`}
              onClick={() => open({ project: link.toProject, nodeId: link.toNode })}
            >
              <span className="node-relation-direction">連出</span>
              <span className="node-relation-copy">
                <b>{link.label || link.toNode}</b>
                <code>
                  {link.toProject === project
                    ? link.toNode
                    : `${link.toProject} / ${link.toNode}`}
                </code>
              </span>
              {link.missing && (
                <span className="node-relation-style relation-broken">失效</span>
              )}
            </button>
          ))}
          {links.backlinks.map((link) => (
            <button
              type="button"
              className="node-relation-item"
              key={`in-${link.fromProject}/${link.fromNode}`}
              onClick={() =>
                open({ project: link.fromProject, nodeId: link.fromNode })
              }
            >
              <span className="node-relation-direction">連入</span>
              <span className="node-relation-copy">
                <b>{link.fromTitle || link.fromNode}</b>
                <code>
                  {link.fromProject === project
                    ? link.fromNode
                    : `${link.fromProject} / ${link.fromNode}`}
                </code>
              </span>
            </button>
          ))}
          {links.outgoing.length === 0 && links.backlinks.length === 0 && (
            <p className="node-relations-empty">
              還沒有文件連結。在內容中用 [[ ]] 按鈕插入。
            </p>
          )}
        </div>
      )}
    </section>
  );
}
