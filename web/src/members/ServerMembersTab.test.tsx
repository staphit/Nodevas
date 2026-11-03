import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { api, type Actor } from "../api";
import { readPreferences } from "../preferences";
import SettingsView from "../settings/SettingsView";
import { useApp } from "../store";

let viewer: Actor | null = null;
// A signed-in viewer only exists on a server with accounts.
vi.mock("../components/SignIn", () => ({
  useViewer: () => viewer,
  useTeamServer: () => viewer !== null,
  useSignOut: () => () => {},
}));

beforeEach(() => {
  useApp.setState({ activeProject: "main", preferences: { ...readPreferences(), language: "en" } });
  useApp.getState().updateUIPreference("language", "en");
  useApp.getState().updateUIPreference("settingsSection", "account");
  // Account security asks the hub; it never answers here.
  vi.spyOn(api, "getTotp").mockReturnValue(new Promise(() => {}));
});

describe("Server accounts in Settings › Account", () => {
  it("is offered to a server administrator", () => {
    viewer = { id: "u-ann", name: "ann", role: "admin" };
    render(<SettingsView />);
    expect(screen.getByRole("button", { name: /Server accounts/ })).toBeTruthy();
    expect(screen.getByRole("button", { name: /Audit log/ })).toBeTruthy();
  });

  it("is not offered to a member", () => {
    viewer = { id: "u-bea", name: "bea", role: "member" };
    render(<SettingsView />);
    expect(screen.getByText("Signed in as bea")).toBeTruthy();
    expect(screen.queryByRole("button", { name: /Server accounts/ })).toBeNull();
    expect(screen.queryByRole("button", { name: /Audit log/ })).toBeNull();
  });

  it("is not offered on a server without accounts", () => {
    viewer = null;
    render(<SettingsView />);
    expect(screen.queryByRole("button", { name: /Server accounts/ })).toBeNull();
    expect(screen.getByText(/This install is standalone/)).toBeTruthy();
  });
});
