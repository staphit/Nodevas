import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api, AuthError, type RunnerLinkResponse } from "../api";
import { readPreferences } from "../preferences";
import { useApp } from "../store";
import { useStepUp } from "../stepup/stepUpStore";
import { LinkDeviceDialog, RunnerAccessTokenSettings } from "./LinkDevice";

const until = () => new Date(Date.now() + 10 * 60 * 1000).toISOString();

const created: RunnerLinkResponse = {
  link: "nvlink1.eyJodWIiOiJodHRwczovL2h1Yi5leGFtcGxlIiwiY29kZSI6ImMifQ",
  name: "laptop",
  hub: "https://hub.example",
  expiresAt: new Date(Date.now() + 10 * 60 * 1000).toISOString(),
  access: true,
};

function stepUpTitle() {
  return document.getElementById("stepup-title");
}

beforeEach(() => {
  useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
  useStepUp.getState().reset();
});

afterEach(() => {
  useStepUp.getState().reset();
  vi.restoreAllMocks();
});

describe("LinkDeviceDialog", () => {
  it("makes a link once stepped up and shows it with the commands and expiry", async () => {
    vi.spyOn(api, "getStepUp").mockResolvedValue({ required: true, active: true, until: until() });
    const createLink = vi.spyOn(api, "createRunnerLink").mockResolvedValue(created);
    render(<LinkDeviceDialog onClose={vi.fn()} />);
    await waitFor(() => expect(useStepUp.getState().status?.active).toBe(true));

    fireEvent.change(screen.getByLabelText("Runner name"), { target: { value: " laptop " } });
    fireEvent.click(screen.getByRole("button", { name: "Create link" }));

    await waitFor(() => expect(createLink).toHaveBeenCalledWith("laptop"));
    expect(((await screen.findByTestId("runner-link-value")) as HTMLInputElement).value).toBe(created.link);
    expect(screen.getByText("nodevas runner login --link")).toBeInTheDocument();
    expect(screen.getByText("scripts/install.sh --hub https://hub.example --link")).toBeInTheDocument();
    expect(screen.getByTestId("runner-link-expiry").textContent).toMatch(/10 minutes/);
    expect(screen.getByRole("alert").textContent).toMatch(/credential/);
    expect(stepUpTitle()).toBeNull();
  });

  it("asks for a step-up first and makes no link without one", async () => {
    vi.spyOn(api, "getStepUp").mockResolvedValue({ required: true, active: false });
    const createLink = vi.spyOn(api, "createRunnerLink").mockResolvedValue(created);
    render(<LinkDeviceDialog onClose={vi.fn()} />);
    await waitFor(() => expect(useStepUp.getState().status).toEqual({ required: true, active: false }));

    fireEvent.change(screen.getByLabelText("Runner name"), { target: { value: "laptop" } });
    fireEvent.click(screen.getByRole("button", { name: "Create link" }));

    await waitFor(() => expect(stepUpTitle()).not.toBeNull());
    expect(createLink).not.toHaveBeenCalled();
    expect(screen.queryByTestId("runner-link-value")).toBeNull();
  });

  it("opens the step-up when the hub refuses for want of one", async () => {
    vi.spyOn(api, "getStepUp")
      .mockResolvedValueOnce({ required: true, active: true, until: until() })
      .mockResolvedValue({ required: true, active: false });
    vi.spyOn(api, "createRunnerLink").mockRejectedValue(new AuthError(403, "verify it's you first"));
    render(<LinkDeviceDialog onClose={vi.fn()} />);
    await waitFor(() => expect(useStepUp.getState().status?.active).toBe(true));

    fireEvent.change(screen.getByLabelText("Runner name"), { target: { value: "laptop" } });
    fireEvent.click(screen.getByRole("button", { name: "Create link" }));

    await waitFor(() => expect(stepUpTitle()).not.toBeNull());
    expect(screen.queryByTestId("runner-link-value")).toBeNull();
  });
});

describe("RunnerAccessTokenSettings", () => {
  it("renders nothing for someone who may not see it", async () => {
    const get = vi.spyOn(api, "getRunnerAccessToken").mockRejectedValue(new AuthError(403, "admins only"));
    const { container } = render(<RunnerAccessTokenSettings />);
    await waitFor(() => expect(get).toHaveBeenCalled());
    expect(container.innerHTML).toBe("");
  });

  it("saves a new token for an administrator and never shows the secret", async () => {
    vi.spyOn(api, "getStepUp").mockResolvedValue({ required: true, active: true, until: until() });
    await useStepUp.getState().load();
    vi.spyOn(api, "getRunnerAccessToken").mockResolvedValue({ clientId: "old.access", set: true });
    const save = vi
      .spyOn(api, "setRunnerAccessToken")
      .mockResolvedValue({ clientId: "new.access", set: true });
    render(<RunnerAccessTokenSettings />);

    expect(await screen.findByText("Set: client ID old.access")).toBeInTheDocument();
    const secret = screen.getByLabelText("Client secret") as HTMLInputElement;
    expect(secret.type).toBe("password");
    expect(secret.value).toBe("");
    fireEvent.change(screen.getByLabelText("Client ID"), { target: { value: "new.access" } });
    fireEvent.change(secret, { target: { value: "s3cret" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => expect(save).toHaveBeenCalledWith("new.access", "s3cret"));
    expect(await screen.findByText("Set: client ID new.access")).toBeInTheDocument();
    expect(secret.value).toBe("");
  });
});
