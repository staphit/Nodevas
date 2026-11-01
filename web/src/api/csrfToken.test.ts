import { describe, expect, it } from "vitest";
import { csrfToken } from "./http";

describe("csrfToken", () => {
  it("reads the plain cookie a hub on plain HTTP sets", () => {
    expect(csrfToken("nodevas_csrf=plain")).toBe("plain");
  });

  // Over TLS the hub names its cookies __Host-, which no sibling subdomain
  // can set; the header must carry that one's value.
  it("reads the __Host- cookie a hub on TLS sets, before a plain one", () => {
    expect(csrfToken("__Host-nodevas_csrf=secure")).toBe("secure");
    expect(csrfToken("nodevas_csrf=tossed; __Host-nodevas_csrf=secure")).toBe("secure");
  });
});
