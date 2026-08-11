import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { api } from "../../api";
import { ImportBundleDialog, type ImportBundleChoice } from "./ImportBundleDialog";

const MANIFEST = { name: "workspace", projects: 3 };

function open() {
  const onConfirm = vi.fn<(choice: ImportBundleChoice) => void>();
  const onCancel = vi.fn();
  render(
    <ImportBundleDialog
      manifest={MANIFEST}
      busy={false}
      onCancel={onCancel}
      onConfirm={onConfirm}
    />,
  );
  return { onConfirm, onCancel, user: userEvent.setup() };
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("ImportBundleDialog", () => {
  // The export was taken of a workspace; restoring it as a workspace is the
  // answer that needs no thought, so it is the one already selected.
  it("offers the top level as the selection the user only has to confirm", () => {
    open();

    expect(screen.getByRole("radio", { name: "還原到工作區最上層" })).toBeChecked();
    expect(screen.getByRole("radio", { name: /放進新資料夾/ })).not.toBeChecked();
    // The count comes from the file, and is the only way to tell this dialog
    // apart from a plain "are you sure".
    expect(screen.getByText("偵測到這是整個工作區的封裝（3 個專案）")).toBeInTheDocument();
    expect(screen.getByLabelText("新資料夾名稱")).toHaveValue("workspace");
  });

  it("imports at the top level with no folder name attached", async () => {
    const { onConfirm, user } = open();

    await user.click(screen.getByRole("button", { name: "匯入" }));

    expect(onConfirm).toHaveBeenCalledWith({ mode: "root", name: "workspace" });
  });

  it("imports into the folder the user names", async () => {
    const { onConfirm, user } = open();

    await user.click(screen.getByRole("radio", { name: /放進新資料夾/ }));
    await user.clear(screen.getByLabelText("新資料夾名稱"));
    await user.type(screen.getByLabelText("新資料夾名稱"), "匯入的工作區");
    await user.click(screen.getByRole("button", { name: "匯入" }));

    expect(onConfirm).toHaveBeenCalledWith({
      mode: "folder",
      name: "匯入的工作區",
    });
  });

  // Typing in the box without touching the radio is the mistake this guards:
  // the name would be collected and then ignored.
  it("treats typing a folder name as choosing the folder", async () => {
    const { onConfirm, user } = open();

    await user.type(screen.getByLabelText("新資料夾名稱"), "-2");
    await user.click(screen.getByRole("button", { name: "匯入" }));

    expect(onConfirm).toHaveBeenCalledWith({ mode: "folder", name: "workspace-2" });
  });

  it("backs out on 取消 without importing anything", async () => {
    const { onCancel, onConfirm, user } = open();

    await user.click(screen.getByRole("button", { name: "取消" }));

    expect(onCancel).toHaveBeenCalled();
    expect(onConfirm).not.toHaveBeenCalled();
  });
});

/**
 * The other half of the choice: what actually reaches the server. The mode is
 * a field of the multipart body, and an import that does not ask the question
 * must not send one at all — the server reads its absence as today's
 * behaviour.
 */
describe("api.importProject", () => {
  function stubUpload() {
    const fetched = vi.fn(async (_url: string, init?: RequestInit) => ({
      ok: true,
      status: 200,
      json: async () => ({ root: "/ws", active: "Stellaris", sent: init }),
    }));
    vi.stubGlobal("fetch", fetched);
    return () => fetched.mock.calls[0][1]?.body as FormData;
  }

  const file = () => new File([new Uint8Array([1, 2])], "workspace.veproj");

  it("sends mode root and no name for a top-level restore", async () => {
    const body = stubUpload();

    await api.importProject(file(), undefined, "root");

    expect(body().get("mode")).toBe("root");
    expect(body().get("name")).toBeNull();
  });

  it("sends mode folder together with the folder name", async () => {
    const body = stubUpload();

    await api.importProject(file(), "匯入的工作區", "folder");

    expect(body().get("mode")).toBe("folder");
    expect(body().get("name")).toBe("匯入的工作區");
  });

  it("omits the mode entirely when nothing was asked", async () => {
    const body = stubUpload();

    await api.importProject(file());

    expect(body().get("mode")).toBeNull();
  });
});
