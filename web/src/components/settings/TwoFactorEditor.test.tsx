import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api, AuthError, type TotpEnrollment } from "../../api";
import { readPreferences } from "../../preferences";
import { useApp } from "../../store";
import { useStepUp } from "../../stepup/stepUpStore";
import { groupKey, TwoFactorEditor } from "./TwoFactorEditor";

const enrollment: TotpEnrollment = {
  secret: "JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP",
  uri: "otpauth://totp/Nodevas:ming%40example.com?secret=JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP&issuer=Nodevas",
  qr: "data:image/png;base64,iVBORw0KGgo=",
  account: "ming@example.com",
  issuer: "Nodevas",
  digits: 6,
  period: 30,
};

const codes = Array.from({ length: 10 }, (_, index) => `AAAA-BBBB-CCCC-DD${index}${index}`);

const notify = { onError: vi.fn(), onNotice: vi.fn() };

beforeEach(() => {
  useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
  useStepUp.getState().reset();
});

afterEach(() => {
  vi.restoreAllMocks();
  notify.onError.mockClear();
  notify.onNotice.mockClear();
});

describe("groupKey", () => {
  it("groups the setup key in fours", () => {
    expect(groupKey("JBSWY3DPEHPK3PXP")).toBe("JBSW Y3DP EHPK 3PXP");
    expect(groupKey("ABCDEF")).toBe("ABCD EF");
  });
});

describe("TwoFactorEditor", () => {
  it("enrolls: QR and setup key, a confirming code, then the recovery codes once", async () => {
    const user = userEvent.setup();
    vi.spyOn(api, "getTotp")
      .mockResolvedValueOnce({ enabled: false, pending: false, recoveryCodesLeft: 0 })
      .mockResolvedValue({ enabled: true, pending: false, enabledAt: "2026-10-25T00:00:00Z", recoveryCodesLeft: 10 });
    const begin = vi.spyOn(api, "beginTotp").mockResolvedValue(enrollment);
    const confirm = vi
      .spyOn(api, "confirmTotp")
      .mockRejectedValueOnce(new AuthError(401, "wrong", "BAD_CODE"))
      .mockResolvedValueOnce({ enabled: true, recoveryCodes: codes });
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { value: { writeText }, configurable: true });

    render(<TwoFactorEditor notify={notify} />);
    expect(await screen.findByTestId("twofactor-status")).toHaveTextContent("Off");
    await user.click(screen.getByRole("button", { name: "Set up" }));
    expect(begin).toHaveBeenCalledTimes(1);

    expect(screen.getByRole("img", { name: "QR code for your authenticator app" })).toHaveAttribute("src", enrollment.qr);
    expect(screen.getByTestId("twofactor-key")).toHaveTextContent("JBSW Y3DP EHPK 3PXP JBSW Y3DP EHPK 3PXP");
    expect(screen.getByText("Account: ming@example.com")).toBeInTheDocument();

    const field = screen.getByLabelText("6-digit code from the app");
    const turnOn = screen.getByRole("button", { name: "Turn on" });
    // A recovery-code shape cannot confirm an enrollment: it needs the app.
    await user.type(field, "ABCD-EFGH-JKMN-PQRS");
    expect(turnOn).toBeDisabled();
    await user.clear(field);
    await user.type(field, "123 456");
    await user.click(turnOn);
    expect(await screen.findByRole("alert")).toHaveTextContent("That code is wrong, expired or already used.");

    await user.clear(field);
    await user.type(field, "654321{Enter}");
    expect(confirm).toHaveBeenLastCalledWith("654321");
    const list = await screen.findByTestId("twofactor-recovery");
    expect(list.querySelectorAll("li")).toHaveLength(10);
    expect(notify.onNotice).toHaveBeenCalledWith("Two-factor authentication is on.");

    await user.click(screen.getByRole("button", { name: /Copy codes/ }));
    expect(writeText).toHaveBeenCalledWith(codes.join("\n"));
    expect(await screen.findByRole("button", { name: /Copied/ })).toBeInTheDocument();

    // Once dismissed, the codes are gone from the page.
    await user.click(screen.getByRole("button", { name: "I've saved them" }));
    expect(screen.queryByTestId("twofactor-recovery")).toBeNull();
    await waitFor(() => expect(screen.getByTestId("twofactor-status")).toHaveTextContent("10 recovery codes left"));
  });

  it("asks for a step-up first when the hub requires one", async () => {
    const user = userEvent.setup();
    vi.spyOn(api, "getTotp").mockResolvedValue({ enabled: false, pending: false, recoveryCodesLeft: 0 });
    vi.spyOn(api, "beginTotp").mockRejectedValue(new AuthError(403, "verify first", "STEP_UP_REQUIRED"));
    vi.spyOn(api, "getStepUp").mockResolvedValue({ required: true, active: false, method: "email" });

    render(<TwoFactorEditor notify={notify} />);
    await user.click(await screen.findByRole("button", { name: "Set up" }));
    expect(await screen.findByText("Verify it's you first, then set up two-factor authentication.")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Verify it's you" }));
    expect(screen.getByRole("dialog", { name: "Verify it's you" })).toBeInTheDocument();
  });

  it("turns off with a recovery code", async () => {
    const user = userEvent.setup();
    vi.spyOn(api, "getTotp")
      .mockResolvedValueOnce({ enabled: true, pending: false, enabledAt: "2026-10-25T00:00:00Z", recoveryCodesLeft: 4 })
      .mockResolvedValue({ enabled: false, pending: false, recoveryCodesLeft: 0 });
    const disable = vi.spyOn(api, "disableTotp").mockResolvedValue({ enabled: false });

    render(<TwoFactorEditor notify={notify} />);
    await user.click(await screen.findByRole("button", { name: "Turn off" }));
    await user.type(screen.getByLabelText("Code"), "abcd-efgh-jkmn-pqrs{Enter}");
    expect(disable).toHaveBeenCalledWith("ABCDEFGHJKMNPQRS");
    await waitFor(() => expect(screen.getByTestId("twofactor-status")).toHaveTextContent("Off"));
    expect(notify.onNotice).toHaveBeenCalledWith("Two-factor authentication is off.");
  });
});
