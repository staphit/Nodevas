import { render } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { formatLocalizedDate, formatLocalizedDateTime, formatLocalizedTime } from "../../i18n";
import { readPreferences } from "../../preferences";
import { useApp } from "../../store";
import type { Language } from "../../preferences/schema";
import type { GraphNode, HistoryEvent, PlanMilestone } from "../../types";
import type { Col } from "../canvas/geometry";
import { TimelineCards } from "./TimelineCards";

// A planned card's time sits beside the actual records of the same day. Raw
// "14:00" next to 「凌晨2:42」 reads as two clocks; both go through the one
// time-of-day format, and so does the card's tooltip.

const node = { id: "n1", title: "Weekly review" } as GraphNode;
const column: Col = { node, row: 0, index: 0, width: 200, height: 100 };

function renderCards(language: Language, plans: PlanMilestone[], events: HistoryEvent[] = []) {
  useApp.setState({ preferences: { ...readPreferences(), language } });
  const noop = vi.fn();
  return render(
    <TimelineCards
      column={column}
      date="2026-08-13"
      plans={plans}
      events={events}
      planStatusDefinitions={[]}
      customStatuses={[]}
      planDrag={null}
      actualDrag={null}
      startPlanDrag={noop}
      movePlanDrag={noop}
      endPlanDrag={noop}
      startActualDrag={noop}
      moveActualDrag={noop}
      endActualDrag={noop}
      selectNode={noop}
      openTab={() => Promise.resolve()}
      openPlanMenu={noop}
      nodeTitle={() => ""}
    />,
  );
}

describe("planned timeline card time", () => {
  for (const language of ["en", "zh-TW"] as const) {
    it(`uses the shared time-of-day format (${language})`, () => {
      const at = new Date(2026, 7, 13, 14, 0);
      const { container } = renderCards(language, [
        { date: "2026-08-13", time: "14:00", status: "started" },
      ]);
      const time = container.querySelector(".plan-time")?.textContent;
      expect(time).toBe(formatLocalizedTime(at, language));
      expect(time).not.toBe("14:00");
    });

    it(`writes the tooltip's moment like an actual record's (${language})`, () => {
      const at = new Date(2026, 7, 13, 14, 0);
      const { container } = renderCards(language, [
        { date: "2026-08-13", time: "14:00", status: "started" },
      ]);
      const title = container.querySelector(".plan-card")?.getAttribute("title") ?? "";
      expect(title).toContain(formatLocalizedDateTime(at, language));
      expect(title).not.toContain("2026-08-13");
    });

    it(`writes a date-only tooltip in the short local date (${language})`, () => {
      const { container } = renderCards(language, [{ date: "2026-08-13", status: "started" }]);
      const title = container.querySelector(".plan-card")?.getAttribute("title") ?? "";
      expect(title).toContain(
        formatLocalizedDate(new Date(2026, 7, 13), language, { dateStyle: "short" }),
      );
      expect(title).not.toContain("2026-08-13");
      expect(container.querySelector(".plan-time")).toBeNull();
    });
  }
});
