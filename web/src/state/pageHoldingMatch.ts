/**
 * Which subpage of a node holds a search hit. Search matches a node's main
 * page and all its subpages as one text and names no page, so the jump has to
 * find it: the node's text subpages are read in order, and the first that
 * holds the query wins. A read, not a write, so it lives beside the slices
 * rather than behind one.
 */

import { api } from "../api";

/** How many subpages a search jump reads looking for its match. */
const MATCH_PAGE_LIMIT = 100;

/** The id of the first text subpage holding query, or null when none does. */
export async function findPageHoldingMatch(nodeId: string, query: string): Promise<string | null> {
  const needle = query.trim().toLowerCase();
  if (!needle) return null;
  const { pages } = await api.listNodePages(nodeId);
  for (const page of (pages ?? []).slice(0, MATCH_PAGE_LIMIT)) {
    // A .docx page is not text the editor shows, nor what search reads.
    if (page.format === "docx") continue;
    const { content } = await api.getNodePage(nodeId, page.id);
    if (content.toLowerCase().includes(needle)) return page.id;
  }
  return null;
}
