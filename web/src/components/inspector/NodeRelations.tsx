/** Node relations panel [B-06]: incoming and outgoing dependencies. */

import { useEffect, useMemo, useState } from "react";
import { api } from "../../api";
import { topLevelOp, useApp } from "../../store";
import {
  edgeLine,
  edgeRelation,
} from "../../domain/graph/edgeStyle";
import { localizedLineLabel, localizedRelationLabel, useI18n } from "../../i18n";

export function NodeRelations({ id }: { id: string }) {
  const { t, language } = useI18n();
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
        <div className="node-relations-empty">{t("relations.empty")}</div>
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
                  {direction === "incoming" ? t("relations.incoming") : t("relations.outgoing")}
                </span>
                <span className="node-relation-copy">
                  <b>{related?.title || relatedId}</b>
                </span>
                <span
                  className={`node-relation-style${
                    edgeRelation(edge) ? ` relation-${edgeRelation(edge)}` : ""
                  }`}
                >
                  {`${localizedLineLabel(edgeLine(edge), language)} / ${localizedRelationLabel(edgeRelation(edge), language)}`}
                </span>
              </button>
            );
          })}
        </div>
      )}
    </section>
  );

  return (
    <div className="node-relations" role="tabpanel" aria-label={t("relations.aria")}>
      <div className="node-relations-summary">
        <div>
          <span>{t("relations.logic")}</span>
          <b>
            {operator
              ? t(`relations.operator.${operator}`)
              : incoming.length > 1
                ? t("relations.operator.and")
                : t("relations.singleCondition")}
          </b>
        </div>
        <div>
          <span>{t("relations.count")}</span>
          <b>{incoming.length + outgoing.length}</b>
        </div>
      </div>
      {relationList(t("relations.prerequisites"), incoming, "incoming")}
      {relationList(t("relations.dependents"), outgoing, "outgoing")}
      <NodeLinkSection
        project={activeProject}
        nodeId={id}
        openNodeLink={openNodeLink}
      />
      <div className="node-relations-help">
        {t("relations.help")}
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
  const { t } = useI18n();
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
      <h3>{t("relations.documentLinks")}</h3>
      {failed && <p className="node-relations-empty">{t("relations.readFailed")}</p>}
      {!failed && !links && <p className="node-relations-empty">{t("relations.loading")}</p>}
      {links && (
        <div className="node-relations-list">
          {links.outgoing.map((link) => (
            <button
              type="button"
              className="node-relation-item"
              key={`out-${link.toProject}/${link.toNode}`}
              onClick={() => open({ project: link.toProject, nodeId: link.toNode })}
            >
              <span className="node-relation-direction">{t("relations.linkOut")}</span>
              <span className="node-relation-copy">
                <b>{link.label || link.toNode}</b>
                <code>
                  {link.toProject === project
                    ? link.toNode
                    : `${link.toProject} / ${link.toNode}`}
                </code>
              </span>
              {link.missing && (
                <span className="node-relation-style relation-broken">{t("relations.broken")}</span>
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
              <span className="node-relation-direction">{t("relations.linkIn")}</span>
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
              {t("relations.noLinks")}
            </p>
          )}
        </div>
      )}
    </section>
  );
}
