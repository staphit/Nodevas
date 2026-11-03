import type { RunAsk, RunEvent } from "../api";

/** One line of a run's timeline: an event of its runner, or an ask. */
export type TimelineItem = { kind: "event"; event: RunEvent } | { kind: "ask"; ask: RunAsk };

/** A run's events with its asks between them: each ask goes before the first
 * event stamped after it was made. The events keep their seq order; an event
 * whose time does not parse places no ask, and asks newer than every event
 * go last. Events are on the runner's clock and asks on the hub's, so a
 * skew between the two only moves an ask a few lines. */
export function interleaveAsks(events: RunEvent[], asks: RunAsk[] | undefined): TimelineItem[] {
  if (!asks || asks.length === 0) return events.map((event) => ({ kind: "event", event }));
  const pending = [...asks].sort((a, b) => a.createdAt - b.createdAt);
  const items: TimelineItem[] = [];
  let next = 0;
  for (const event of events) {
    const at = Date.parse(event.at);
    while (next < pending.length && Number.isFinite(at) && pending[next].createdAt < at) {
      items.push({ kind: "ask", ask: pending[next++] });
    }
    items.push({ kind: "event", event });
  }
  while (next < pending.length) items.push({ kind: "ask", ask: pending[next++] });
  return items;
}

type Translate = (key: string, vars?: Record<string, string | number>) => string;

/** What an ask is about, on one line: a prompt's tool and input preview, or
 * a question's text. */
export function askSubject(t: Translate, ask: RunAsk): string {
  if (ask.kind === "permission") {
    const tool = ask.tool || t("runs.tool");
    return ask.preview ? `${tool} · ${ask.preview}` : tool;
  }
  return ask.question || ask.tool || ask.kind;
}

/** How a settled ask ended, with the person's note when the viewer may see
 * it; "" while it waits. */
export function askOutcome(t: Translate, ask: RunAsk): string {
  let outcome: string;
  switch (ask.decision) {
    case "approve_run":
      outcome = t("agents.ask.statusAllowedRun");
      break;
    case "approve":
      outcome = t(ask.kind === "permission" ? "agents.ask.statusAllowed" : "agents.ask.statusApproved");
      break;
    case "deny":
      outcome = t(
        ask.kind === "question" || ask.kind === "choice" ? "agents.ask.statusDismissed" : "agents.ask.statusDenied",
      );
      break;
    case "answer":
      outcome = t("agents.ask.statusAnswered");
      break;
    default:
      if (ask.status === "pending") return "";
      outcome = ask.status === "expired" ? t("agents.ask.expired") : ask.status;
  }
  return ask.note ? t("runs.ask.withNote", { outcome, note: ask.note }) : outcome;
}
