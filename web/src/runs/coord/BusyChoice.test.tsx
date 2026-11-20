import { fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { api, type BusyResponse, type RunHolder, type ThemePack } from "../../api";
import { translate } from "../../i18n";
import { readPreferences, writePreference } from "../../preferences";
import { useApp } from "../../store";
import { parseThemeManifest } from "../../theme-pack/themeManifest";
import { useThemePacks } from "../../theme-pack/themePackStore";
import { BusyChoice } from "./BusyChoice";

const PACK: ThemePack = {
  id: "studio",
  name: { en: "Studio" },
  version: "1.0.0",
  author: "tester",
  owner: "u1",
  sizeBytes: 1,
  createdAt: "2026-11-18T00:00:00Z",
  updatedAt: "2026-11-18T00:00:00Z",
  mine: true,
  canDelete: true,
};

const MANIFEST = {
  schema: 1,
  id: "studio",
  name: { en: "Studio" },
  version: "1.0.0",
  author: "tester",
  canvas: [200, 240],
  characters: {
    sage: {
      match: { agents: ["claude"], models: ["claude*", "opus*"] },
      title: { en: "Sage" },
      scenes: {
        idle: {
          image: "sage/idle.webp",
          x: 0,
          y: 0,
          w: 160,
          h: 200,
          box: [20, 20, 140, 180],
        },
      },
    },
    smith: {
      match: { agents: ["codex"], models: ["*"] },
      title: { en: "Smith" },
      scenes: {
        idle: {
          image: "smith/idle.webp",
          x: 0,
          y: 0,
          w: 160,
          h: 200,
          box: [20, 20, 140, 180],
        },
      },
    },
  },
};

function withPack() {
  const value = parseThemeManifest(MANIFEST);
  if (!value) throw new Error("manifest parse failed");
  useThemePacks.setState({ loaded: true, packs: [PACK], defaultId: PACK.id, manifest: { id: PACK.id, value } });
}

function makeHolder(overrides: Partial<RunHolder> = {}): RunHolder {
  return {
    runId: "run-1",
    title: "Fix login",
    agent: "claude",
    model: "opus",
    mode: "interactive",
    status: "running",
    waiting: false,
    since: new Date(Date.now() - 5 * 60_000).toISOString(),
    by: "Ann",
    nodeId: "node-0003",
    repoName: "api",
    mayStop: true,
    sameNode: true,
    ...overrides,
  };
}

function makeBusy(overrides: Partial<BusyResponse> = {}): BusyResponse {
  return {
    holders: [makeHolder()],
    others: 0,
    choices: ["queue", "takeOver", "both"],
    ...overrides,
  };
}

beforeEach(() => {
  vi.spyOn(api, "listThemePacks").mockResolvedValue({ packs: [], default: "" });
  useThemePacks.getState().reset();
  useThemePacks.setState({ loaded: true, packs: [], defaultId: "", manifest: null });
  writePreference("language", "en");
  writePreference("themePack", "");
  useApp.setState({ preferences: readPreferences() });
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe("BusyChoice", () => {
  it("renders choices strictly in the server's order and omits take-over when absent", () => {
    const busy = makeBusy({
      choices: ["both", "queue"],
      others: 1,
    });
    render(<BusyChoice busy={busy} simple={false} onChoose={vi.fn()} onCancel={vi.fn()} />);

    const radios = screen.getAllByRole("radio");
    expect(radios).toHaveLength(2);
    expect(radios[0]).toHaveTextContent(translate("busy.both", "en"));
    expect(radios[1]).toHaveTextContent(translate("busy.queue", "en"));
    expect(screen.queryByText(translate("busy.takeOver", "en"))).toBeNull();
    expect(screen.getByText(translate("busy.askOwner", "en"))).toBeInTheDocument();
  });

  it("renders hidden holders as one muted card with no name or details", () => {
    const busy = makeBusy({
      holders: [],
      others: 2,
      choices: ["queue", "both"],
    });
    const { container } = render(<BusyChoice busy={busy} simple={false} onChoose={vi.fn()} onCancel={vi.fn()} />);

    const hiddenCard = container.querySelector(".coord-holder-card.is-hidden");
    expect(hiddenCard).not.toBeNull();
    expect(hiddenCard).toHaveTextContent(translate("busy.others", "en", { count: 2 }));
    expect(hiddenCard).not.toHaveTextContent(/Ann|Claude|opus|Fix login/i);
    expect(hiddenCard?.querySelector("img")).toBeNull();
  });

  it("marks waiting holders as dimmed with busy.waiting", () => {
    const busy = makeBusy({
      holders: [makeHolder({ runId: "run-2", waiting: true, status: "queued" })],
    });
    const { container } = render(<BusyChoice busy={busy} simple={false} onChoose={vi.fn()} onCancel={vi.fn()} />);

    const card = container.querySelector(".coord-holder-card.is-waiting");
    expect(card).not.toBeNull();
    expect(card).toHaveTextContent(translate("busy.waiting", "en"));
  });

  it("shows continue choice with its agent and model, and adds a character still in pack mode", () => {
    const busy = makeBusy({
      choices: ["continue", "queue"],
      continue: { runId: "run-1", agent: "codex", model: "o3", openPR: false },
    });
    const { rerender } = render(<BusyChoice busy={busy} simple={false} onChoose={vi.fn()} onCancel={vi.fn()} />);

    const continueRadio = screen.getAllByRole("radio")[0];
    expect(continueRadio).toHaveTextContent("Codex");
    expect(continueRadio).toHaveTextContent("o3");
    expect(continueRadio.querySelector("img")).toBeNull();

    withPack();
    rerender(<BusyChoice busy={busy} simple={false} onChoose={vi.fn()} onCancel={vi.fn()} />);
    const continueRadioPack = screen.getAllByRole("radio")[0];
    const img = continueRadioPack.querySelector("img");
    expect(img).not.toBeNull();
    expect(img?.getAttribute("src")).toBe("/api/themes/studio/files/smith/idle.webp");
  });

  it("shows a character still with no agent/model text when a pack is active, and initials with no img without a pack", () => {
    const busy = makeBusy({ holders: [makeHolder({ agent: "claude", model: "opus", title: "Fix login" })] });
    const { container, rerender } = render(
      <BusyChoice busy={busy} simple={false} onChoose={vi.fn()} onCancel={vi.fn()} />,
    );

    const plainCard = container.querySelector(".coord-holder-card")!;
    expect(plainCard.querySelector("img")).toBeNull();
    expect(plainCard.querySelector(".station-avatar")).toHaveTextContent("Cl");
    expect(plainCard).toHaveTextContent("Claude");

    withPack();
    rerender(<BusyChoice busy={busy} simple={false} onChoose={vi.fn()} onCancel={vi.fn()} />);

    const packCard = container.querySelector(".coord-holder-card")!;
    const img = packCard.querySelector("img")!;
    expect(img).not.toBeNull();
    expect(img.getAttribute("src")).toBe("/api/themes/studio/files/sage/idle.webp");
    expect(packCard).not.toHaveTextContent(/Claude|opus|Ann/i);

    // Broken image falls back to initials without img.
    fireEvent.error(img);
    expect(packCard.querySelector("img")).toBeNull();
    expect(packCard.querySelector(".station-avatar")).toHaveTextContent("Cl");
  });

  it("supports arrow-key navigation in the radiogroup and confirms the chosen option", () => {
    const onChoose = vi.fn();
    const onCancel = vi.fn();
    const busy = makeBusy({ choices: ["queue", "takeOver", "both"] });
    render(<BusyChoice busy={busy} simple={false} onChoose={onChoose} onCancel={onCancel} />);

    const group = screen.getByRole("radiogroup");
    const radios = within(group).getAllByRole("radio");
    expect(radios[0]).toHaveAttribute("aria-checked", "true");
    expect(radios[0]).toHaveAttribute("tabindex", "0");
    expect(radios[1]).toHaveAttribute("tabindex", "-1");

    fireEvent.keyDown(group, { key: "ArrowDown" });
    expect(radios[1]).toHaveAttribute("aria-checked", "true");
    expect(radios[1]).toHaveAttribute("tabindex", "0");
    expect(onChoose).not.toHaveBeenCalled();

    fireEvent.keyDown(group, { key: "ArrowRight" });
    expect(radios[2]).toHaveAttribute("aria-checked", "true");

    fireEvent.keyDown(group, { key: "ArrowUp" });
    expect(radios[1]).toHaveAttribute("aria-checked", "true");

    fireEvent.click(screen.getByRole("button", { name: translate("common.confirm", "en") }));
    expect(onChoose).toHaveBeenCalledWith("takeOver");

    fireEvent.click(screen.getByRole("button", { name: translate("common.cancel", "en") }));
    expect(onCancel).toHaveBeenCalledTimes(1);
  });

  it("adds the one-after-another note when maxConcurrent is 1", () => {
    const busy = makeBusy({ choices: ["queue", "both"] });
    render(<BusyChoice busy={busy} simple={false} maxConcurrent={1} onChoose={vi.fn()} onCancel={vi.fn()} />);

    expect(screen.getByText(translate("busy.bothSerial", "en"))).toBeInTheDocument();
  });

  it("uses plain Simple wording with no actor name ({who}) or developer terms", () => {
    const busy = makeBusy({
      holders: [makeHolder({ by: "local", mode: "interactive", status: "running", sameNode: false })],
      choices: ["queue", "takeOver", "both", "continue"],
      continue: { runId: "run-1", agent: "claude", model: "opus", openPR: false },
    });
    const { container } = render(
      <BusyChoice busy={busy} simple={true} onChoose={vi.fn()} onCancel={vi.fn()} />,
    );

    expect(container).toHaveTextContent(/Your helper is still working in this folder \(\d+ min\)/);
    expect(screen.getByText(translate("busy.simple.queue", "en"))).toBeInTheDocument();
    expect(screen.getByText(translate("busy.simple.takeOver", "en"))).toBeInTheDocument();
    expect(screen.getByText(translate("busy.simple.both", "en"))).toBeInTheDocument();
    expect(screen.getByText(translate("busy.simple.continue", "en"))).toBeInTheDocument();
    expect(container.textContent).not.toMatch(/\blocal\b|\bbranch\b|\bpull request\b|\bPR\b|\bworktree\b/i);
  });

  it("explains what take-over keeps or loses based on holder mode and scope", () => {
    // Interactive node holder -> keeps on branch, no PR.
    const { rerender } = render(
      <BusyChoice
        busy={makeBusy({
          holders: [makeHolder({ mode: "interactive", status: "running", nodeId: "node-1", sameNode: true })],
          choices: ["takeOver"],
        })}
        simple={false}
        onChoose={vi.fn()}
        onCancel={vi.fn()}
      />,
    );
    expect(screen.getByText(translate("busy.takeOverKeepsNoPR", "en"))).toBeInTheDocument();

    // Interactive global holder -> keeps work so far.
    rerender(
      <BusyChoice
        busy={makeBusy({
          holders: [makeHolder({ mode: "interactive", status: "running", nodeId: "", sameNode: false })],
          choices: ["takeOver"],
        })}
        simple={false}
        onChoose={vi.fn()}
        onCancel={vi.fn()}
      />,
    );
    expect(screen.getByText(translate("busy.takeOverKeeps", "en"))).toBeInTheDocument();

    // Headless holder -> unfinished changes are not kept.
    rerender(
      <BusyChoice
        busy={makeBusy({
          holders: [makeHolder({ mode: "", status: "running" })],
          choices: ["takeOver"],
        })}
        simple={false}
        onChoose={vi.fn()}
        onCancel={vi.fn()}
      />,
    );
    expect(screen.getByText(translate("busy.takeOverLoses", "en"))).toBeInTheDocument();
  });
});
