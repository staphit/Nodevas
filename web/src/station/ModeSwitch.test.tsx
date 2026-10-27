import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it } from "vitest";
import type { AgentAsk } from "../api";
import { useAgents } from "../agents/agentStore";
import { readPreferences } from "../preferences";
import { useApp } from "../store";
import { ModeSwitch } from "./ModeSwitch";

const ask = (over: Partial<AgentAsk>): AgentAsk =>
  ({ id: "a", project: "p", sessionId: "s", sessionName: "s", actorId: "ann", question: "?", status: "pending", createdAt: 1, ...over }) as AgentAsk;

beforeEach(() => {
  useApp.setState({ preferences: { ...readPreferences(), language: "en", projectMode: "info", agentWorkstation: false } });
  useAgents.setState({ asks: [], held: [], viewer: { id: "ann", name: "ann", role: "member" } });
});

describe("ModeSwitch", () => {
  it("switches between Info and Station, and Station turns the workstation on", () => {
    render(<ModeSwitch />);
    expect(screen.getByRole("button", { name: "Info" }).getAttribute("aria-pressed")).toBe("true");
    fireEvent.click(screen.getByRole("button", { name: "Station" }));
    const prefs = useApp.getState().preferences;
    expect(prefs.projectMode).toBe("station");
    expect(prefs.agentWorkstation).toBe(true);
  });

  it("counts only the questions this viewer may answer", () => {
    useAgents.setState({
      asks: [ask({}), ask({ id: "b", actorId: "carol" }), ask({ id: "c", status: "approved" })],
    });
    render(<ModeSwitch />);
    const station = screen.getByRole("button", { name: "Station, 1 waiting on you" });
    expect(station.querySelector(".mode-switch-badge")?.textContent).toBe("1");
  });
});
