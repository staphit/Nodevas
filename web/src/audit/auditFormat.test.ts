import { describe, expect, it } from "vitest";
import type { AuditEntry } from "../api";
import { auditCategory, auditDetailPairs, auditFailed, filterAudit } from "./auditFormat";

function entry(op: string, extra: Partial<AuditEntry> = {}): AuditEntry {
  return { at: "2026-10-26T08:00:00Z", actor: "", id: "", op, target: "", ip: "", detail: null, ...extra };
}

describe("auditCategory", () => {
  it.each([
    ["auth.signin", "signIn"],
    ["auth.signin.failed", "signIn"],
    ["auth.signin.pending", "signIn"],
    ["auth.signout", "signIn"],
    ["auth.passcode.request", "signIn"],
    ["auth.stepup", "stepUp"],
    ["auth.stepup.request", "stepUp"],
    ["auth.stepup.failed", "stepUp"],
    ["auth.totp.enable", "twoFactor"],
    ["auth.totp.recovery.used", "twoFactor"],
    ["auth.totp.reset", "twoFactor"],
    ["runner.link.create", "devices"],
    ["runner.link.redeem", "devices"],
    ["runner.access-token.clear", "devices"],
    ["runner.create", "devices"],
    ["account.invite", "accounts"],
    ["account.remove", "accounts"],
    ["PUT projects/members", "accounts"],
    ["POST projects/members/open", "accounts"],
    ["PUT notify/settings", "settings"],
    ["PUT runs/routing", "settings"],
    ["node.update", "other"],
    ["POST nodes", "other"],
    ["run.merge.failed", "other"],
    ["pack.install", "other"],
  ])("%s is %s", (op, category) => {
    expect(auditCategory(op)).toBe(category);
  });

  it("does not take a lookalike prefix for an accounts write", () => {
    expect(auditCategory("PUT projects/membersx")).toBe("other");
  });
});

describe("auditFailed", () => {
  it("reads the .failed suffix the server writes", () => {
    expect(auditFailed("auth.signin.failed")).toBe(true);
    expect(auditFailed("auth.signin")).toBe(false);
  });
});

describe("auditDetailPairs", () => {
  it("keeps strings as they are and writes anything else as JSON text", () => {
    expect(auditDetailPairs({ reason: "bad-credentials", delivered: true, roles: ["a"] })).toEqual([
      ["reason", "bad-credentials"],
      ["delivered", "true"],
      ["roles", '["a"]'],
    ]);
    expect(auditDetailPairs(null)).toEqual([]);
  });
});

describe("filterAudit", () => {
  const entries = [
    entry("auth.signin", { actor: "ann", id: "u-ann", ip: "203.0.113.7" }),
    entry("auth.signin.failed", { detail: { email: "mallory@example.test", reason: "unknown-account" } }),
    entry("auth.stepup", { actor: "ann" }),
    entry("account.invite", { actor: "ann", target: "u-dan", detail: { name: "dan" } }),
    entry("node.update", { actor: "bea", target: "alpha" }),
  ];

  it("keeps one kind", () => {
    expect(filterAudit(entries, "signIn", "").map((e) => e.op)).toEqual(["auth.signin", "auth.signin.failed"]);
    expect(filterAudit(entries, "all", "")).toHaveLength(entries.length);
  });

  it("searches every field, the detail included, ignoring case", () => {
    expect(filterAudit(entries, "all", "MALLORY").map((e) => e.op)).toEqual(["auth.signin.failed"]);
    expect(filterAudit(entries, "all", "203.0.113").map((e) => e.op)).toEqual(["auth.signin"]);
    expect(filterAudit(entries, "all", "u-dan").map((e) => e.op)).toEqual(["account.invite"]);
  });

  it("needs every word, and combines with the kind", () => {
    expect(filterAudit(entries, "all", "ann invite").map((e) => e.op)).toEqual(["account.invite"]);
    expect(filterAudit(entries, "stepUp", "bea")).toEqual([]);
  });
});
