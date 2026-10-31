import { act, renderHook } from "@testing-library/react";
import type { FormEvent } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "../../api";
import { readPreferences } from "../../preferences";
import type { ProjectEntry } from "../../state/types";
import { useApp } from "../../store";
import { useProjectCreate } from "./ProjectCreatePanel";
import { useProjectRename } from "./ProjectRenamePanel";
import { useSaveAs } from "./SaveAsPanel";

// Windows file names ignore case, and so does the hub's "already exists"
// check there: "MAIN" beside "main" got past the form, reached the server and
// came back as its refusal. The form says so itself, whatever the case, and
// when only the case differs it says that case does not count, naming the
// project that holds the name; "already exists" beside a list that shows no
// "MAIN" read as a bug.

const OTHER_CASE_EN = "“main” is already here. Project names ignore letter case, as folder names do on Windows and macOS.";
const OTHER_CASE_ZH = "這個位置已經有「main」。專案名稱不分大小寫，和 Windows 與 macOS 的資料夾名稱一樣。";

const main: ProjectEntry = { name: "main", label: "main", depth: 0, path: "/ws/main", nodes: 1 };
const projectByName = new Map([["main", main]]);
const event = { preventDefault() {} } as FormEvent;
const common = {
  projectByName,
  expandedProjects: new Set<string>(),
  persistExpandedProjects: () => {},
  setProjectTransferNotice: () => {},
};

let switchProject: ReturnType<typeof vi.fn>;

beforeEach(() => {
  switchProject = vi.fn(async () => {});
  useApp.setState({
    preferences: { ...readPreferences(), language: "en" },
    activeProject: "main",
    projects: [main],
    switchProject: switchProject as unknown as ReturnType<typeof useApp.getState>["switchProject"],
    changeProjects: (async (run: () => Promise<unknown>) => run()) as unknown as ReturnType<
      typeof useApp.getState
    >["changeProjects"],
    saveAllTabs: (async () => true) as unknown as ReturnType<typeof useApp.getState>["saveAllTabs"],
  });
});

describe("a project name taken in another case", () => {
  it("is refused by the new-project form before it reaches the hub", async () => {
    const { result } = renderHook(() => useProjectCreate(common));
    act(() => result.current.beginProjectCreate({ mode: "project", parent: "" }));
    act(() => result.current.setProjectName("MAIN"));
    await act(() => result.current.createProject(event));
    expect(switchProject).not.toHaveBeenCalled();
    expect(result.current.projectCreateError).toBe(OTHER_CASE_EN);
  });

  it("is refused by Save as", async () => {
    const { result } = renderHook(() => useSaveAs({ ...common, activeProject: "main" }));
    act(() => result.current.openSaveAs());
    act(() => result.current.setSaveAsName("Main"));
    await act(() => result.current.saveProjectAs(event));
    expect(switchProject).not.toHaveBeenCalled();
    expect(result.current.saveAsError).toBe(OTHER_CASE_EN);
  });

  it("is refused by rename when another project holds it", async () => {
    const other: ProjectEntry = { name: "probs", label: "probs", depth: 0, path: "/ws/probs", nodes: 1 };
    const both = new Map([...projectByName, ["probs", other]]);
    const moveProject = vi.spyOn(api, "moveProject");
    const { result } = renderHook(() => useProjectRename({ ...common, projectByName: both }));
    act(() =>
      result.current.beginProjectRename({
        name: "probs",
        label: "probs",
        names: ["probs"],
        path: "/ws/probs",
        isFolder: false,
        childCount: 0,
        x: 0,
        y: 0,
      }),
    );
    act(() => result.current.setProjectRenameValue("Main"));
    await act(() => result.current.renameProject(event));
    expect(moveProject).not.toHaveBeenCalled();
    expect(result.current.projectRenameError).toBe(OTHER_CASE_EN);
    moveProject.mockRestore();
  });

  it("is explained to a Chinese reader in Chinese", async () => {
    useApp.setState({ preferences: { ...readPreferences(), language: "zh-TW" } });
    const { result } = renderHook(() => useProjectCreate(common));
    act(() => result.current.beginProjectCreate({ mode: "project", parent: "" }));
    act(() => result.current.setProjectName("MAIN"));
    await act(() => result.current.createProject(event));
    expect(result.current.projectCreateError).toBe(OTHER_CASE_ZH);
  });

  it("leaves the plain duplicate message for the very same name", async () => {
    const { result } = renderHook(() => useProjectCreate(common));
    act(() => result.current.beginProjectCreate({ mode: "project", parent: "" }));
    act(() => result.current.setProjectName("main"));
    await act(() => result.current.createProject(event));
    expect(result.current.projectCreateError).toBe("A project with the same name already exists here.");
  });
});
