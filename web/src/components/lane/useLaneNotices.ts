/**
 * The two transient banners a pane can show. They are confirmations, not
 * errors to be dismissed by hand, so each clears itself after a few seconds.
 */

import { useEffect, useState } from "react";

export interface LaneNotice {
  text: string;
  kind: "ok" | "error";
}

export function useLaneNotices() {
  const [graphNotice, setGraphNotice] = useState<LaneNotice | null>(null);
  const [planNotice, setPlanNotice] = useState<LaneNotice | null>(null);

  useEffect(() => {
    if (!planNotice) return;
    const timer = window.setTimeout(() => setPlanNotice(null), 3200);
    return () => window.clearTimeout(timer);
  }, [planNotice]);

  useEffect(() => {
    if (!graphNotice) return;
    const timer = window.setTimeout(() => setGraphNotice(null), 3200);
    return () => window.clearTimeout(timer);
  }, [graphNotice]);

  return { graphNotice, setGraphNotice, planNotice, setPlanNotice };
}
