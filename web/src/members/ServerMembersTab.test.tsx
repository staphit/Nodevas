import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Actor } from "../api";
import { ProjectSettings } from "../components/ProjectSettings";
import { readPreferences } from "../preferences";
import { useApp } from "../store";

let viewer: Actor | null = null;
// A signed-in viewer only exists on a server with accounts.
vi.mock("../components/SignIn", () => ({
  useViewer: () => viewer,
  useTeamServer: () => viewer !== null,
}));

beforeEach(() => {
  useApp.setState({ activeProject: "main", preferences: { ...readPreferences(), language: "en" } });
  useApp.getState().updateUIPreference("language", "en");
});

describe("Members of this server tab", () => {
  it("is offered to a server administrator", () => {
    viewer = { id: "u-ann", name: "ann", role: "admin" };
    render(<ProjectSettings onClose={() => {}} />);
    expect(screen.getByRole("tab", { name: "Members of this server" })).toBeTruthy();
  });

  it("is not offered to a member", () => {
    viewer = { id: "u-bea", name: "bea", role: "member" };
    render(<ProjectSettings onClose={() => {}} />);
    expect(screen.queryByRole("tab", { name: "Members of this server" })).toBeNull();
  });

  it("is not offered on a server without accounts", () => {
    viewer = null;
    render(<ProjectSettings onClose={() => {}} />);
    expect(screen.queryByRole("tab", { name: "Members of this server" })).toBeNull();
  });
});
