import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { readPreferences } from "../preferences";
import { useApp } from "../store";
import { AgentChatEmpty, DEFAULT_SERVER, MCP_DOCS_URL, mcpCommand, setupCommand, shellWord } from "./AgentChatEmpty";

let team = false;
vi.mock("../components/SignIn", async (original) => ({
  ...(await original<typeof import("../components/SignIn")>()),
  useTeamServer: () => team,
}));

const openSettings = vi.hoisted(() => vi.fn());
vi.mock("../settings/openSettings", async (original) => ({
  ...(await original<typeof import("../settings/openSettings")>()),
  openSettings,
}));

beforeEach(() => {
  team = false;
  openSettings.mockReset();
  useApp.setState({ activeProject: "my board", preferences: { ...readPreferences(), language: "en" } });
});

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
});

describe("the setup commands", () => {
  it("names the project, and the server only when it is not the default", () => {
    expect(setupCommand("app", DEFAULT_SERVER)).toBe("nodevas setup-agent --project app");
    expect(setupCommand("my board", "http://127.0.0.1:5680")).toBe(
      'nodevas setup-agent --server http://127.0.0.1:5680 --project "my board"',
    );
    expect(mcpCommand("app", DEFAULT_SERVER)).toBe(
      "nodevas mcp --server http://127.0.0.1:5666 --project app --actor mcp:claude",
    );
  });

  it("quotes what a shell would split or expand", () => {
    expect(shellWord("plain-name_1.0")).toBe("plain-name_1.0");
    expect(shellWord('a "b" $c')).toBe(String.raw`"a \"b\" \$c"`);
  });
});

describe("AgentChatEmpty", () => {
  it("says what Agent chat is and copies the command for this computer", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText } });
    render(<AgentChatEmpty />);
    expect(screen.getByText(/^Agent chat is where the coding agents you run/)).toBeInTheDocument();
    const command = setupCommand("my board", location.origin);
    expect(screen.getByText(command)).toBeInTheDocument();
    expect(command).toContain('--project "my board"');
    expect(command).toContain(location.origin === DEFAULT_SERVER ? "setup-agent --project" : `--server ${location.origin}`);
    expect(screen.getByText("Or add the MCP server by hand:")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Setup guide" })).toHaveAttribute("href", MCP_DOCS_URL);
    expect(screen.getByRole("link", { name: "Setup guide" })).toHaveAttribute("rel", "noopener noreferrer");

    await act(async () => {
      fireEvent.click(screen.getAllByRole("button", { name: "Copy" })[0]);
    });
    expect(writeText).toHaveBeenCalledWith(command);
    expect(screen.getAllByText("Copied").length).toBeGreaterThan(0);
    await act(async () => {
      vi.advanceTimersByTime(2_100);
    });
    expect(screen.queryByRole("button", { name: "Copied" })).not.toBeInTheDocument();
  });

  it("on a team server, sends agents through a runner", () => {
    team = true;
    render(<AgentChatEmpty />);
    expect(screen.getByText(/connect through a runner/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Copy" })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Add a runner" }));
    expect(openSettings).toHaveBeenCalledWith("runners", { add: true });
  });
});
