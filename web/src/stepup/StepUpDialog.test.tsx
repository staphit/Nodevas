import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api, AuthError } from "../api";
import { formatLocalizedTime } from "../i18n";
import { readPreferences } from "../preferences";
import { useApp } from "../store";
import { compactStepUpCode, formatClock, StepUpDialog } from "./StepUpDialog";
import { stepUpActive, useStepUp } from "./stepUpStore";

const until = new Date(Date.now() + 10 * 60 * 1000).toISOString();

beforeEach(() => {
  useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
  useStepUp.getState().reset();
});

afterEach(() => {
  useStepUp.getState().reset();
  vi.restoreAllMocks();
});

function codeField(): HTMLInputElement {
  return screen.getByRole("textbox", { name: "8-character code" }) as HTMLInputElement;
}

describe("step-up helpers", () => {
  it("reads a window that ran out as inactive", () => {
    const now = Date.parse("2026-10-24T14:00:00Z");
    expect(stepUpActive({ required: true, active: true, until: "2026-10-24T14:05:00Z" }, now)).toBe(true);
    expect(stepUpActive({ required: true, active: true, until: "2026-10-24T13:59:00Z" }, now)).toBe(false);
    expect(stepUpActive({ required: true, active: false }, now)).toBe(false);
    expect(stepUpActive(null, now)).toBe(false);
  });

  it("drops spaces and dashes from a pasted code", () => {
    expect(compactStepUpCode(" 123 456 ")).toBe("123456");
    expect(compactStepUpCode("123-456")).toBe("123456");
  });

  it("formats the end of the window as a 24-hour clock", () => {
    expect(formatClock("2026-10-24T14:05:00")).toBe(formatLocalizedTime("2026-10-24T14:05:00"));
    expect(formatClock("nonsense")).toBe("");
  });
});

describe("StepUpDialog", () => {
  it("requests a code, refuses a wrong one, then verifies", async () => {
    vi.spyOn(api, "getStepUp").mockResolvedValue({ required: true, active: false });
    const request = vi.spyOn(api, "requestStepUp").mockResolvedValue("email");
    const verify = vi
      .spyOn(api, "verifyStepUp")
      .mockRejectedValueOnce(new AuthError(401, "invalid code"))
      .mockResolvedValueOnce({ required: true, active: true, until });

    render(<StepUpDialog onClose={vi.fn()} />);
    expect(screen.getByRole("dialog", { name: "Verify it's you" })).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Send code" }));

    await waitFor(() => expect(request).toHaveBeenCalledTimes(1));
    expect(await screen.findByText("Code sent. Check your email.")).toBeInTheDocument();
    // The resend waits out the server's cooldown.
    expect(screen.getByRole("button", { name: /Resend in \d+s/ })).toBeDisabled();

    const submit = screen.getByRole("button", { name: "Verify" });
    fireEvent.change(codeField(), { target: { value: "123" } });
    expect(submit).toBeDisabled();
    fireEvent.change(codeField(), { target: { value: "WXYZ 2345" } });
    expect(submit).toBeEnabled();
    fireEvent.click(submit);

    expect(await screen.findByRole("alert")).toHaveTextContent("That code is wrong or has expired.");
    expect(verify).toHaveBeenLastCalledWith("WXYZ2345");
    expect(codeField()).toHaveAttribute("aria-invalid", "true");

    fireEvent.change(codeField(), { target: { value: "ABCD2345" } });
    fireEvent.click(screen.getByRole("button", { name: "Verify" }));

    expect(await screen.findByTestId("stepup-done")).toHaveTextContent(
      `Verified until ${formatClock(until)}`,
    );
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(useStepUp.getState().status).toEqual({ required: true, active: true, until });
  });

  it("words a throttled request and a throttled code", async () => {
    vi.spyOn(api, "getStepUp").mockResolvedValue({ required: true, active: false });
    const request = vi
      .spyOn(api, "requestStepUp")
      .mockRejectedValueOnce(new AuthError(429, "rate_limited"))
      .mockResolvedValueOnce("email");
    vi.spyOn(api, "verifyStepUp").mockRejectedValue(new AuthError(429, "rate_limited"));

    render(<StepUpDialog onClose={vi.fn()} />);
    fireEvent.click(screen.getByRole("button", { name: "Send code" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Too many attempts");
    // Still on the first step: nothing was sent.
    expect(screen.queryByRole("textbox", { name: "8-character code" })).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Send code" }));
    await waitFor(() => expect(request).toHaveBeenCalledTimes(2));
    fireEvent.change(await screen.findByRole("textbox", { name: "8-character code" }), {
      target: { value: "ABCD2345" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Verify" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Too many attempts");
    expect(useStepUp.getState().status?.active).toBe(false);
  });

  it("says so when the server cannot send mail", async () => {
    vi.spyOn(api, "getStepUp").mockResolvedValue({ required: true, active: false });
    vi.spyOn(api, "requestStepUp").mockRejectedValue(new AuthError(503, "mail_unavailable"));

    render(<StepUpDialog onClose={vi.fn()} />);
    fireEvent.click(screen.getByRole("button", { name: "Send code" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("can't send email");
  });

  it("opens on the verified state while a step-up is active", async () => {
    vi.spyOn(api, "getStepUp").mockResolvedValue({ required: true, active: true, until });
    const onClose = vi.fn();

    render(<StepUpDialog onClose={onClose} />);
    expect(await screen.findByTestId("stepup-done")).toHaveTextContent(formatClock(until));
    fireEvent.click(screen.getByRole("button", { name: "Done" }));
    expect(onClose).toHaveBeenCalled();
  });

  it("keeps Escape from reaching the dialog underneath", async () => {
    vi.spyOn(api, "getStepUp").mockResolvedValue({ required: true, active: false });
    const onClose = vi.fn();
    const below = vi.fn();
    window.addEventListener("keydown", below);
    try {
      render(<StepUpDialog onClose={onClose} />);
      await act(async () => {
        fireEvent.keyDown(document.body, { key: "Escape" });
      });
      expect(onClose).toHaveBeenCalledTimes(1);
      expect(below).not.toHaveBeenCalled();
    } finally {
      window.removeEventListener("keydown", below);
    }
  });
});

describe("StepUpDialog with an authenticator app", () => {
  it("asks for the app's code straight away and mails nothing", async () => {
    vi.spyOn(api, "getStepUp").mockResolvedValue({ required: true, active: false, method: "totp" });
    const request = vi.spyOn(api, "requestStepUp").mockResolvedValue("totp");
    const verify = vi
      .spyOn(api, "verifyStepUp")
      .mockRejectedValueOnce(new AuthError(401, "wrong"))
      .mockResolvedValueOnce({ required: true, active: true, until, method: "totp" });

    render(<StepUpDialog onClose={vi.fn()} />);
    const field = (await screen.findByRole("textbox", { name: "Authenticator code" })) as HTMLInputElement;
    expect(screen.queryByRole("button", { name: "Send code" })).toBeNull();
    expect(screen.getByText(/No email is sent/)).toBeInTheDocument();

    const verifyButton = screen.getByRole("button", { name: "Verify" });
    fireEvent.change(field, { target: { value: "12345" } });
    expect(verifyButton).toBeDisabled();
    fireEvent.change(field, { target: { value: "123 456" } });
    fireEvent.click(verifyButton);
    expect(await screen.findByRole("alert")).toHaveTextContent("That code is wrong, expired or already used.");
    expect(verify).toHaveBeenLastCalledWith("123456");

    // A recovery code works here too.
    fireEvent.change(field, { target: { value: "abcd-efgh-jkmn-pqrs" } });
    fireEvent.click(verifyButton);
    expect(await screen.findByTestId("stepup-done")).toBeInTheDocument();
    expect(verify).toHaveBeenLastCalledWith("ABCDEFGHJKMNPQRS");
    expect(request).not.toHaveBeenCalled();
  });
});
