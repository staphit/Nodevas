/**
 * Document links, in both directions [B-04].
 *
 * These are references written in the markdown, not dependencies: they may
 * point into another project, and they never gate anything. A link whose
 * target is gone is listed as broken rather than quietly dropped.
 *
 * Kept out of the Dependencies tab's own file because it is the one part that
 * reads the server directly: the links live in the markdown, not in the graph
 * the store holds.
 */

import { useEffect, useState } from "react";

import { api } from "../../api";
import { useI18n } from "../../i18n";
import { reportError } from "../../store";

export function NodeLinkSection({
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
