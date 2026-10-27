import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { api, ConflictError, RequestError, type AuditEntry, type AuditHealth } from "../api";
import { ConfirmDialogHost } from "../components/ConfirmDialog";
import { readPreferences } from "../preferences";
import { useApp } from "../store";
import { AuditPanel } from "./AuditPanel";

const HEALTHY: AuditHealth = {
  status: "healthy",
  writeStatus: "healthy",
  fallbackEvents: 0,
  acknowledgedFallbackEvents: 0,
  unreconciledEvents: 0,
};

const RECOVERED: AuditHealth = {
  status: "degraded",
  writeStatus: "healthy",
  fallbackEvents: 3,
  acknowledgedFallbackEvents: 1,
  unreconciledEvents: 2,
  lastFailureAt: "2026-10-26T07:00:00Z",
  lastError: "database is locked",
};

const HOSTILE = '<script>window.__pwned = true</script><img src=x onerror="window.__pwned = true">';

const ENTRIES: AuditEntry[] = [
  {
    at: "2026-10-26T08:00:00Z",
    actor: "",
    id: "",
    op: "auth.signin.failed",
    target: "",
    ip: "198.51.100.9",
    detail: { email: HOSTILE, reason: "unknown-account" },
  },
  { at: "2026-10-26T07:59:00Z", actor: "ann", id: "u-ann", op: "auth.signin", target: "", ip: "203.0.113.7", detail: { method: "otp" } },
  { at: "2026-10-26T07:58:00Z", actor: "ann", id: "u-ann", op: "auth.stepup", target: "", ip: "203.0.113.7", detail: null },
  { at: "2026-10-26T07:57:00Z", actor: "ann", id: "u-ann", op: "account.invite", target: "u-dan", ip: "203.0.113.7", detail: { name: "dan", role: "member" } },
];

const LIMITER = {
  active_buckets: 4,
  max_entries: 10000,
  rejected_total: 7,
  rejected_by_class: { "public-auth": 6, write: 1 },
  semaphores: { heavy: { in_use: 0, capacity: 2 }, docx: { in_use: 0, capacity: 2 }, remote: { in_use: 1, capacity: 4 } },
};

function mockApi({ health = HEALTHY, entries = ENTRIES } = {}) {
  return {
    health: vi.spyOn(api, "getAuditHealth").mockResolvedValue(health),
    audit: vi.spyOn(api, "getAudit").mockResolvedValue({ entries }),
    limiter: vi.spyOn(api, "getAuditLimiter").mockResolvedValue(LIMITER),
  };
}

function open() {
  const view = render(
    <>
      <AuditPanel />
      <ConfirmDialogHost />
    </>,
  );
  return { ...view, user: userEvent.setup() };
}

function trail() {
  return screen.getByRole("region", { name: "Recent events" });
}

beforeEach(() => {
  vi.restoreAllMocks();
  delete (window as { __pwned?: boolean }).__pwned;
  useApp.setState({ activeProject: "main", preferences: { ...readPreferences(), language: "en" } });
  useApp.getState().updateUIPreference("language", "en");
});

describe("AuditPanel trail", () => {
  it("asks for the server-wide trail and lists it", async () => {
    const { audit } = mockApi();
    open();
    expect(await screen.findByText("account.invite")).toBeTruthy();
    expect(audit).toHaveBeenCalledWith({ scope: "server", limit: 500 });
    expect(within(trail()).getByText("4 of 4 events")).toBeTruthy();
    // A refused sign-in names nobody and is marked as failed.
    const failed = screen.getByText("auth.signin.failed").closest("tr")!;
    expect(failed.getAttribute("data-failed")).toBe("true");
    expect(within(failed).getByText("Nobody signed in")).toBeTruthy();
    expect(within(failed).getByText("198.51.100.9")).toBeTruthy();
  });

  it("draws attacker-typed detail as text, never as markup", async () => {
    mockApi();
    const { container } = open();
    // The whole string is there, verbatim, as the text of one element.
    expect(await screen.findByText(HOSTILE)).toBeTruthy();
    expect(container.querySelector("script")).toBeNull();
    expect(container.querySelector("img")).toBeNull();
    expect((window as { __pwned?: boolean }).__pwned).toBeUndefined();
  });

  it("filters by kind and by text", async () => {
    mockApi();
    const { user } = open();
    await screen.findByText("account.invite");

    await user.selectOptions(screen.getByLabelText("Kind of event"), "stepUp");
    expect(within(trail()).getByText("1 of 4 events")).toBeTruthy();
    expect(screen.getByText("auth.stepup")).toBeTruthy();
    expect(screen.queryByText("account.invite")).toBeNull();

    await user.selectOptions(screen.getByLabelText("Kind of event"), "all");
    await user.type(screen.getByLabelText("Search events"), "u-dan");
    expect(within(trail()).getByText("1 of 4 events")).toBeTruthy();
    expect(screen.getByText("account.invite")).toBeTruthy();

    await user.clear(screen.getByLabelText("Search events"));
    await user.type(screen.getByLabelText("Search events"), "nothing like this");
    expect(within(trail()).getByText("No events match these filters.")).toBeTruthy();
  });

  it("switches to the project's writes and refreshes", async () => {
    const { audit } = mockApi();
    const { user } = open();
    await screen.findByText("account.invite");
    await user.selectOptions(screen.getByLabelText("Which events"), "project");
    await waitFor(() => expect(audit).toHaveBeenLastCalledWith({ scope: "project", limit: 500 }));
    await user.click(within(trail()).getByRole("button", { name: "Refresh" }));
    await waitFor(() => expect(audit).toHaveBeenCalledTimes(3));
  });

  it("says so when there is nothing", async () => {
    mockApi({ entries: [] });
    open();
    expect(await screen.findByText("No events recorded yet.")).toBeTruthy();
  });

  it("shows the server's error", async () => {
    mockApi();
    vi.spyOn(api, "getAudit").mockRejectedValue(new RequestError(500, "Internal Server Error"));
    open();
    expect(await within(trail()).findByText("Internal Server Error")).toBeTruthy();
  });
});

describe("AuditPanel health", () => {
  it("reads healthy with nothing to acknowledge", async () => {
    mockApi();
    open();
    expect(await screen.findByText("Every audit event is reaching the database.")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Acknowledge" })).toBeNull();
  });

  it("acknowledges exactly the fallback count it showed, after confirming", async () => {
    mockApi({ health: RECOVERED });
    const ack = vi.spyOn(api, "acknowledgeAuditHealth").mockResolvedValue({
      ...RECOVERED,
      status: "healthy",
      acknowledgedFallbackEvents: 3,
      unreconciledEvents: 0,
    });
    const { user } = open();
    expect(await screen.findByText(/2 events reached only the server log/)).toBeTruthy();
    expect(screen.getByText("database is locked")).toBeTruthy();

    await user.click(screen.getByRole("button", { name: "Acknowledge" }));
    const dialog = await screen.findByRole("alertdialog");
    await user.click(within(dialog).getByRole("button", { name: "Acknowledge" }));
    expect(ack).toHaveBeenCalledWith(3);
    expect(await screen.findByText("Acknowledged.")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Acknowledge" })).toBeNull();
  });

  it("does not acknowledge without confirmation", async () => {
    mockApi({ health: RECOVERED });
    const ack = vi.spyOn(api, "acknowledgeAuditHealth");
    const { user } = open();
    await user.click(await screen.findByRole("button", { name: "Acknowledge" }));
    const dialog = await screen.findByRole("alertdialog");
    await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
    expect(ack).not.toHaveBeenCalled();
  });

  it("refreshes when a newer fallback beat the acknowledgement", async () => {
    const { health } = mockApi({ health: RECOVERED });
    vi.spyOn(api, "acknowledgeAuditHealth").mockRejectedValue(
      new ConflictError("", "", "audit fallback count changed", "audit_fallback_count_changed"),
    );
    const { user } = open();
    await user.click(await screen.findByRole("button", { name: "Acknowledge" }));
    health.mockResolvedValue({ ...RECOVERED, fallbackEvents: 4, unreconciledEvents: 3 });
    await user.click(within(await screen.findByRole("alertdialog")).getByRole("button", { name: "Acknowledge" }));
    expect(await screen.findByText(/More events reached the log/)).toBeTruthy();
    expect(await screen.findByText(/3 events reached only the server log/)).toBeTruthy();
  });

  it("will not acknowledge while writes are still failing", async () => {
    mockApi({ health: { ...RECOVERED, writeStatus: "degraded" } });
    open();
    expect(await screen.findByText(/can't be written to the database right now/)).toBeTruthy();
    expect((screen.getByRole("button", { name: "Acknowledge" }) as HTMLButtonElement).disabled).toBe(true);
  });
});

describe("AuditPanel limiter", () => {
  it("shows refusals per kind of route and the busy slots", async () => {
    mockApi();
    open();
    const pane = screen.getByRole("region", { name: "Rate limiter" });
    const signIn = (await within(pane).findByText("Refused: sign-in")).closest("div")!;
    expect(within(signIn).getByText("6")).toBeTruthy();
    expect(within(pane).getByText("7")).toBeTruthy();
    expect(within(pane).getByText("1 / 4")).toBeTruthy();
  });
});
