import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api, RequestError, ConflictError, type ThemePack } from "../api";
import { ConfirmDialogHost } from "../components/ConfirmDialog";
import { configurePreferenceStorage, readPreferences } from "../preferences";
import { useApp } from "../store";
import { useThemePacks } from "./themePackStore";
import { ThemePacksPanel } from "./ThemePacksPanel";

const viewer = vi.hoisted(() => ({ role: "member", team: true }));

vi.mock("../components/SignIn", () => ({
  useViewer: () => ({ id: "ann", name: "Ann", role: viewer.role }),
  useTeamServer: () => viewer.team,
  useSignOut: () => () => {},
}));

function fakeStorage() {
  const map = new Map<string, string>();
  return {
    getItem: (key: string) => map.get(key) ?? null,
    setItem: (key: string, value: string) => void map.set(key, value),
    removeItem: (key: string) => void map.delete(key),
  };
}

const magic: ThemePack = {
  id: "magic",
  name: { en: "Magic world", "zh-TW": "魔法世界" },
  version: "1.0.0",
  author: "someone",
  owner: "ann",
  sizeBytes: 2 * 1024 * 1024,
  createdAt: "2026-11-01T00:00:00Z",
  updatedAt: "2026-11-01T00:00:00Z",
  mine: true,
  canDelete: true,
};

const sea: ThemePack = {
  ...magic,
  id: "sea",
  name: { en: "Under the sea" },
  version: "2.1.0",
  author: "",
  owner: "bob",
  mine: false,
  canDelete: false,
};

const zip = () => new File(["zip"], "magic.zip", { type: "application/zip" });

function serve(packs: ThemePack[] = [magic, sea], defaultId = "") {
  return vi.spyOn(api, "listThemePacks").mockResolvedValue({ packs, default: defaultId });
}

beforeEach(() => {
  configurePreferenceStorage(fakeStorage());
  useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
  useThemePacks.getState().reset();
  viewer.role = "member";
  viewer.team = true;
  vi.spyOn(api, "getThemeManifest").mockResolvedValue({});
});

afterEach(() => vi.restoreAllMocks());

const yours = () => screen.getByRole("combobox", { name: "Your theme" });
const NAME = ".backup-list-name";
const listed = (name: string) => screen.findByText(name, { selector: NAME });
const row = (name: string) => screen.getByText(name, { selector: NAME }).closest("li") as HTMLElement;

describe("the theme packs part of Settings", () => {
  it("lists each pack: name, version, author, size and who imported it", async () => {
    serve();
    render(<ThemePacksPanel />);

    await listed("Magic world");
    const mine = within(row("Magic world"));
    expect(mine.getByText(/1\.0\.0/)).toHaveTextContent("1.0.0 · by someone · 2.0 MB · imported by you");
    // No author: nothing said about one.
    const theirs = within(row("Under the sea"));
    expect(theirs.getByText(/2\.1\.0/)).toHaveTextContent("2.1.0 · 2.0 MB · imported by bob");
  });

  it("shows a pack's name in the reader's language, English when it has none there", async () => {
    useApp.getState().updateUIPreference("language", "zh-TW");
    serve();
    render(<ThemePacksPanel />);
    expect(await listed("魔法世界")).toBeInTheDocument();
    expect(row("Under the sea")).toBeInTheDocument();
  });

  it("says where a pack comes from and what it can do", async () => {
    serve();
    render(<ThemePacksPanel />);
    await listed("Magic world");
    expect(screen.getByText(/whoever imported it/)).toHaveTextContent(/images only, no code runs/i);
  });

  it("says when there are none", async () => {
    serve([]);
    render(<ThemePacksPanel />);
    expect(await screen.findByText(/No theme packs yet/)).toBeInTheDocument();
  });

  it("says so when the list cannot be read", async () => {
    vi.spyOn(api, "listThemePacks").mockRejectedValue(new Error("down"));
    render(<ThemePacksPanel />);
    expect(await screen.findByRole("alert")).toHaveTextContent("down");
  });

  describe("choosing for yourself", () => {
    it("starts on the site default, naming it, and offers the built-in people and each pack", async () => {
      serve([magic, sea], "sea");
      render(<ThemePacksPanel />);
      await listed("Magic world");

      expect(yours()).toHaveValue("");
      expect(within(yours()).getAllByRole("option").map((option) => option.textContent)).toEqual([
        "Site default (Under the sea)",
        "Built-in pixel people",
        "Magic world",
        "Under the sea",
      ]);
    });

    it("names the built-in people as the default when the site has none", async () => {
      serve();
      render(<ThemePacksPanel />);
      await listed("Magic world");
      expect(within(yours()).getAllByRole("option")[0]).toHaveTextContent("Site default (Built-in pixel people)");
    });

    it("remembers the choice as a preference", async () => {
      const user = userEvent.setup();
      serve();
      render(<ThemePacksPanel />);
      await listed("Magic world");

      await user.selectOptions(yours(), "Magic world");
      expect(useApp.getState().preferences.themePack).toBe("magic");
      await user.selectOptions(yours(), "Built-in pixel people");
      expect(useApp.getState().preferences.themePack).toBe("none");
    });

    it("reads a picked pack that is gone as the site default", async () => {
      useApp.getState().updateUIPreference("themePack", "gone");
      serve();
      render(<ThemePacksPanel />);
      await listed("Magic world");
      expect(yours()).toHaveValue("");
    });

    // The preference's "none" means the built-in people, so a pack with that id
    // cannot be told apart from them.
    it("does not offer a pack whose id is the word for the built-in people", async () => {
      serve([{ ...magic, id: "none", name: { en: "Odd one" } }, sea]);
      render(<ThemePacksPanel />);
      await listed("Odd one");
      expect(within(yours()).queryByRole("option", { name: "Odd one" })).not.toBeInTheDocument();
    });
  });

  describe("importing", () => {
    const fileInput = () => screen.getByLabelText("Import a zip") as HTMLInputElement;

    it("shows the upload's progress, then the check, then the pack", async () => {
      const user = userEvent.setup();
      const list = serve([]);
      let finish: (pack: ThemePack) => void = () => {};
      const importPack = vi.spyOn(api, "importThemePack").mockImplementation((_file, onProgress) => {
        onProgress?.(25, 100);
        return new Promise((resolve) => (finish = resolve));
      });
      render(<ThemePacksPanel />);
      await screen.findByText(/No theme packs yet/);
      list.mockResolvedValue({ packs: [magic], default: "" });

      await user.upload(fileInput(), zip());
      expect(importPack).toHaveBeenCalledOnce();
      expect(await screen.findByRole("progressbar")).toHaveAttribute("value", "25");
      expect(screen.getByRole("status")).toHaveTextContent("Uploading magic.zip: 25%");
      expect(screen.getByRole("button", { name: "Import a zip" })).toBeDisabled();

      // All sent: the server is still reading the zip.
      act(() => importPack.mock.calls[0][1]?.(100, 100));
      expect(screen.getByRole("status")).toHaveTextContent("Checking magic.zip");
      expect(screen.getByRole("progressbar")).not.toHaveAttribute("value");

      await act(async () => finish(magic));
      expect(await listed("Magic world")).toBeInTheDocument();
      expect(screen.queryByRole("progressbar")).not.toBeInTheDocument();
      expect(screen.getByRole("button", { name: "Import a zip" })).toBeEnabled();
    });

    it("gives the server's refusal, and lets the next try clear it", async () => {
      const user = userEvent.setup();
      serve([]);
      const importPack = vi
        .spyOn(api, "importThemePack")
        .mockRejectedValueOnce(new RequestError(400, "theme.json is missing"));
      render(<ThemePacksPanel />);
      await screen.findByText(/No theme packs yet/);

      await user.upload(fileInput(), zip());
      expect(await screen.findByRole("alert")).toHaveTextContent("theme.json is missing");

      importPack.mockResolvedValueOnce(magic);
      await user.upload(fileInput(), zip());
      await waitFor(() => expect(screen.queryByRole("alert")).not.toBeInTheDocument());
    });

    it("gives the words of a 409, not the class's", async () => {
      const user = userEvent.setup();
      serve([]);
      vi.spyOn(api, "importThemePack").mockRejectedValueOnce(new ConflictError("", "", "someone else owns magic"));
      render(<ThemePacksPanel />);
      await screen.findByText(/No theme packs yet/);

      await user.upload(fileInput(), zip());
      expect(await screen.findByRole("alert")).toHaveTextContent("someone else owns magic");
    });

    it("says something for a 409 that has no words", async () => {
      const user = userEvent.setup();
      serve([]);
      vi.spyOn(api, "importThemePack").mockRejectedValueOnce(new ConflictError("", ""));
      render(<ThemePacksPanel />);
      await screen.findByText(/No theme packs yet/);

      await user.upload(fileInput(), zip());
      expect(await screen.findByRole("alert")).toHaveTextContent("The pack could not be imported.");
    });

    it("can take the same file twice", async () => {
      const user = userEvent.setup();
      serve([]);
      const importPack = vi.spyOn(api, "importThemePack").mockResolvedValue(magic);
      render(<ThemePacksPanel />);
      await screen.findByText(/No theme packs yet/);

      const file = zip();
      await user.upload(fileInput(), file);
      await waitFor(() => expect(screen.getByRole("button", { name: "Import a zip" })).toBeEnabled());
      await user.upload(fileInput(), file);
      await waitFor(() => expect(importPack).toHaveBeenCalledTimes(2));
    });

    it("keeps its progress when the part is closed and opened again", async () => {
      const user = userEvent.setup();
      serve([]);
      vi.spyOn(api, "importThemePack").mockImplementation((_file, onProgress) => {
        onProgress?.(50, 100);
        return new Promise(() => {});
      });
      const { unmount } = render(<ThemePacksPanel />);
      await screen.findByText(/No theme packs yet/);
      await user.upload(fileInput(), zip());
      await screen.findByRole("progressbar");

      unmount();
      render(<ThemePacksPanel />);
      expect(await screen.findByRole("progressbar")).toHaveAttribute("value", "50");
    });
  });

  describe("deleting", () => {
    it("is offered only where the server allows it", async () => {
      serve();
      render(<ThemePacksPanel />);
      await listed("Magic world");
      expect(within(row("Magic world")).getByRole("button", { name: "Delete Magic world" })).toBeInTheDocument();
      expect(within(row("Under the sea")).queryByRole("button")).not.toBeInTheDocument();
    });

    it("asks inside the page, and does nothing when told no", async () => {
      const user = userEvent.setup();
      const browserConfirm = vi.spyOn(window, "confirm");
      serve();
      const remove = vi.spyOn(api, "deleteThemePack").mockResolvedValue(undefined);
      render(
        <>
          <ConfirmDialogHost />
          <ThemePacksPanel />
        </>,
      );
      await listed("Magic world");

      await user.click(screen.getByRole("button", { name: "Delete Magic world" }));
      const dialog = await screen.findByRole("alertdialog");
      expect(dialog).toHaveTextContent("Magic world");
      await user.click(within(dialog).getByRole("button", { name: "Cancel" }));

      expect(remove).not.toHaveBeenCalled();
      expect(browserConfirm).not.toHaveBeenCalled();
    });

    it("deletes when told yes, and the list follows", async () => {
      const user = userEvent.setup();
      const list = serve();
      const remove = vi.spyOn(api, "deleteThemePack").mockResolvedValue(undefined);
      render(
        <>
          <ConfirmDialogHost />
          <ThemePacksPanel />
        </>,
      );
      await listed("Magic world");
      list.mockResolvedValue({ packs: [sea], default: "" });

      await user.click(screen.getByRole("button", { name: "Delete Magic world" }));
      const dialog = await screen.findByRole("alertdialog");
      await user.click(within(dialog).getByRole("button", { name: "Delete" }));

      await waitFor(() => expect(remove).toHaveBeenCalledWith("magic"));
      await waitFor(() => expect(screen.queryByText("Magic world", { selector: NAME })).not.toBeInTheDocument());
    });

    it("says why when it could not", async () => {
      const user = userEvent.setup();
      serve();
      vi.spyOn(api, "deleteThemePack").mockRejectedValue(new Error("files could not be removed"));
      render(
        <>
          <ConfirmDialogHost />
          <ThemePacksPanel />
        </>,
      );
      await listed("Magic world");

      await user.click(screen.getByRole("button", { name: "Delete Magic world" }));
      const dialog = await screen.findByRole("alertdialog");
      await user.click(within(dialog).getByRole("button", { name: "Delete" }));

      expect(await screen.findByRole("alert")).toHaveTextContent("files could not be removed");
      // A failure of one action says nothing about the pack on show.
      expect(useThemePacks.getState().error).toBeNull();
      expect(row("Magic world")).toBeInTheDocument();
    });
  });

  describe("the site default", () => {
    const siteDefault = () => screen.queryByRole("combobox", { name: "Site default" });

    it("is not offered to a member", async () => {
      serve();
      render(<ThemePacksPanel />);
      await listed("Magic world");
      expect(siteDefault()).not.toBeInTheDocument();
    });

    it("is not offered on a standalone install", async () => {
      viewer.role = "admin";
      viewer.team = false;
      serve();
      render(<ThemePacksPanel />);
      await listed("Magic world");
      expect(siteDefault()).not.toBeInTheDocument();
    });

    it("is an administrator's to set and clear", async () => {
      const user = userEvent.setup();
      viewer.role = "admin";
      const list = serve([magic, sea], "sea");
      const set = vi.spyOn(api, "setDefaultThemePack").mockResolvedValue({ default: "magic" });
      render(<ThemePacksPanel />);
      await listed("Magic world");
      expect(siteDefault()).toHaveValue("sea");

      list.mockResolvedValue({ packs: [magic, sea], default: "magic" });
      await user.selectOptions(siteDefault()!, "Magic world");
      expect(set).toHaveBeenCalledWith("magic");
      await waitFor(() => expect(siteDefault()).toHaveValue("magic"));

      list.mockResolvedValue({ packs: [magic, sea], default: "" });
      await user.selectOptions(siteDefault()!, "Built-in pixel people");
      expect(set).toHaveBeenCalledWith("");
      await waitFor(() => expect(siteDefault()).toHaveValue(""));
    });

    it("says why when it could not be set", async () => {
      const user = userEvent.setup();
      viewer.role = "admin";
      serve();
      vi.spyOn(api, "setDefaultThemePack").mockRejectedValue(new RequestError(404, "no such pack"));
      render(<ThemePacksPanel />);
      await listed("Magic world");

      await user.selectOptions(siteDefault()!, "Magic world");
      expect(await screen.findByRole("alert")).toHaveTextContent("no such pack");
      expect(siteDefault()).toHaveValue("");
    });
  });
});
