import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { api, GlobalRunError, RunnerRequestError } from "../api";
import { readPreferences } from "../preferences";
import { useRunners } from "../runners/runnerStore";
import { useApp } from "../store";
import { AddAgentDialog } from "./AddAgentDialog";
import { localRunner } from "./localFixtures";

const HOME = "C:\\Users\\me";
const BIN = `${HOME}\\.local\\bin`;
const CLAUDE = `${BIN}\\claude.exe`;

let listDirs: ReturnType<typeof vi.spyOn>;

function open(kind = "claude") {
  const onClose = vi.fn();
  const onAdded = vi.fn();
  render(<AddAgentDialog runnerId="local1" kind={kind} onClose={onClose} onAdded={onAdded} />);
  return { onClose, onAdded };
}

beforeEach(() => {
  useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
  useRunners.setState({ loaded: true, loading: false, runners: [localRunner()], error: null, creating: false, revoking: {} });
  listDirs = vi.spyOn(api, "listDirs").mockImplementation(async (path = "", options = {}) => {
    if (path === "") return { path: "", parent: "", home: HOME, dirs: [{ name: "C:", path: "C:\\" }] };
    if (path === HOME) {
      return {
        path: HOME,
        parent: "C:\\Users",
        home: HOME,
        dirs: options.hidden ? [{ name: ".local", path: `${HOME}\\.local` }] : [{ name: "Documents", path: `${HOME}\\Documents` }],
        files: [],
      };
    }
    if (path === `${HOME}\\.local`) return { path, parent: HOME, home: HOME, dirs: [{ name: "bin", path: BIN }], files: [] };
    if (path === BIN) return { path, parent: `${HOME}\\.local`, home: HOME, dirs: [], files: [{ name: "claude.exe", path: CLAUDE }] };
    return { path, parent: HOME, home: HOME, dirs: [], files: [] };
  });
});

afterEach(() => {
  vi.restoreAllMocks();
});

/** Shows hidden folders and walks to the folder holding claude.exe. */
async function walkToClaude() {
  await screen.findByText("No file named claude in this folder. Open another folder.");
  fireEvent.click(screen.getByRole("checkbox", { name: "Show hidden folders" }));
  fireEvent.click(await screen.findByRole("button", { name: /\.local/ }));
  fireEvent.click(await screen.findByRole("button", { name: /bin/ }));
  fireEvent.click(await screen.findByRole("button", { name: /claude\.exe/ }));
}

describe("AddAgentDialog", () => {
  it("starts in the home folder and only lists files named like the agent's program", async () => {
    open();
    const dialog = screen.getByRole("dialog", { name: "Show Nodevas where Claude is" });
    expect(dialog).toHaveTextContent("Pick the Claude program file on this computer. Only files named claude are shown.");
    await waitFor(() => expect(listDirs).toHaveBeenCalledTimes(2));
    expect(listDirs.mock.calls[0]).toEqual([""]);
    expect(listDirs.mock.calls[1]).toEqual([HOME, { files: "claude" }]);
    expect(await screen.findByRole("button", { name: /Documents/ })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Use this file" })).toBeDisabled();
  });

  it("names Antigravity's program by its file", async () => {
    open("antigravity");
    await waitFor(() => expect(listDirs).toHaveBeenCalledWith(HOME, { files: "agy" }));
    expect(screen.getByRole("dialog")).toHaveTextContent("Only files named agy are shown.");
  });

  it("hands the picked file to this computer, which checks it before using it", async () => {
    let answer: () => void = () => {};
    const save = vi.spyOn(api, "setRunnerSettings").mockReturnValue(
      new Promise((resolve) => {
        answer = () => resolve({ runner: localRunner(), persisted: true });
      }),
    );
    const { onAdded } = open();
    await walkToClaude();
    expect(listDirs).toHaveBeenCalledWith(BIN, { files: "claude", hidden: true });
    fireEvent.click(screen.getByRole("button", { name: "Use this file" }));
    expect(await screen.findByText("Checking that it answers…")).toBeInTheDocument();
    expect(save).toHaveBeenCalledWith("local1", { agentPath: { claude: CLAUDE }, redetect: true });
    answer();
    await waitFor(() => expect(onAdded).toHaveBeenCalledWith(localRunner()));
  });

  it.each([
    ["name", "That isn't the Claude program. Its file is named claude."],
    ["place", "Nodevas can't use a program kept inside a folder agents work in. Pick one somewhere else."],
    ["no_answer", "That file didn't answer as Claude. Pick another one."],
    ["invalid", "Nodevas couldn't use that file: bad file"],
  ])("says plainly why a file is refused (%s)", async (refused, text) => {
    vi.spyOn(api, "setRunnerSettings").mockRejectedValue(
      new RunnerRequestError(new GlobalRunError(400, "INVALID_ARGUMENT", "", "agentPath", 0, "bad file"), refused),
    );
    const { onAdded } = open();
    await walkToClaude();
    fireEvent.click(screen.getByRole("button", { name: "Use this file" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(text);
    expect(onAdded).not.toHaveBeenCalled();
    expect(screen.getByRole("button", { name: "Use this file" })).toBeEnabled();
  });

  it("closes on Escape without closing what it sits on", async () => {
    const outer = vi.fn();
    window.addEventListener("keydown", outer);
    const { onClose } = open();
    await screen.findByRole("button", { name: /Documents/ });
    fireEvent.keyDown(screen.getByRole("dialog"), { key: "Escape" });
    expect(onClose).toHaveBeenCalled();
    expect(outer).not.toHaveBeenCalled();
    window.removeEventListener("keydown", outer);
  });

  it("is a modal dialog with a close button", async () => {
    const { onClose } = open();
    const dialog = screen.getByRole("dialog");
    expect(dialog).toHaveAttribute("aria-modal", "true");
    fireEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));
    expect(onClose).toHaveBeenCalled();
  });
});
