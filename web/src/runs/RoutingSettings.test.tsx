import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api, AuthError, RequestError, type Actor, type RouteRule, type Runner } from "../api";
import { readPreferences } from "../preferences";
import { useApp } from "../store";
import { useRunners } from "../runners/runnerStore";
import { ruleProblems, rulesPayload, RoutingSettings } from "./RoutingSettings";
import { useRouting } from "./routingStore";

const viewer = vi.hoisted(() => ({ current: null as Actor | null }));
vi.mock("../components/SignIn", () => ({ useViewer: () => viewer.current }));

const alice: Actor = { id: "u1", name: "alice", role: "admin" };

const desktop: Runner = {
  id: "r1",
  name: "desktop",
  ownerId: "u1",
  ownerName: "alice",
  createdAt: "2026-10-23T00:00:00Z",
  lastSeenAt: "2026-10-24T08:00:00Z",
  online: true,
  info: {
    version: "1.3.0",
    hostname: "PC-01",
    os: "windows",
    arch: "amd64",
    capabilities: ["headless"],
    agents: [
      {
        kind: "claude",
        installed: true,
        models: [
          { id: "opus", label: "Opus" },
          { id: "sonnet", label: "Sonnet", default: true },
        ],
      },
      { kind: "codex", installed: true, models: [{ id: "gpt-5" }] },
      { kind: "gemini", installed: false, models: [{ id: "flash" }] },
    ],
    repos: [],
  },
};

const laptop: Runner = { ...desktop, id: "r2", name: "laptop", online: false };
const bobs: Runner = { ...desktop, id: "r3", name: "bob-pc", ownerId: "u2" };

function rule(overrides: Partial<RouteRule>): RouteRule {
  return {
    id: "a",
    project: "proj",
    match: "tag",
    value: "review",
    target: { agent: "claude", model: "opus" },
    ...overrides,
  };
}

function ruleItem(n: number): HTMLElement {
  return screen.getByRole("listitem", { name: `Rule ${n}` });
}

function combo(scope: HTMLElement, name: string | RegExp): HTMLSelectElement {
  return within(scope).getByRole("combobox", { name }) as HTMLSelectElement;
}

beforeEach(() => {
  viewer.current = alice;
  useApp.setState({
    preferences: { ...readPreferences(), language: "en" },
    activeProject: "proj",
    graph: { nodes: [{ id: "n1", title: "A", tags: ["review", "chore"] }] } as never,
  });
  useRunners.setState({
    loaded: true,
    loading: false,
    runners: [desktop, laptop, bobs],
    error: null,
    creating: false,
    revoking: {},
  });
  useRouting.getState().reset();
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe("RoutingSettings", () => {
  it("starts with no rules: nothing is built in", async () => {
    vi.spyOn(api, "getRouting").mockResolvedValue({ rules: null });
    render(<RoutingSettings />);
    expect(await screen.findByText(/No rules yet/)).toBeInTheDocument();
    expect(screen.queryByRole("listitem", { name: "Rule 1" })).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Save rules" })).toBeDisabled();
  });

  it("builds a rule with target, a reordered fallback chain and a pipeline, then PUTs it", async () => {
    vi.spyOn(api, "getRouting").mockResolvedValue({ rules: [] });
    const put = vi
      .spyOn(api, "putRouting")
      .mockImplementation(async (rules) => ({ rules }));
    render(<RoutingSettings />);
    await screen.findByText(/No rules yet/);

    fireEvent.click(screen.getByRole("button", { name: "Add rule" }));
    const item = ruleItem(1);
    // A new rule is blank: the owner picks every part of it.
    expect(combo(item, "Target agent").value).toBe("");
    expect(within(item).getByRole("combobox", { name: "Applies to" })).toHaveValue("proj");

    fireEvent.change(within(item).getByRole("combobox", { name: "Tag" }), {
      target: { value: " review " },
    });
    // Agents come from the viewer's runners' catalogs; uninstalled ones are left out.
    expect(
      within(combo(item, "Target agent"))
        .getAllByRole("option")
        .map((option) => option.textContent),
    ).toEqual(["Choose agent…", "claude", "codex", "Other…"]);
    fireEvent.change(combo(item, "Target agent"), { target: { value: "codex" } });
    expect(combo(item, "Target model").value).toBe("gpt-5");
    // Only one's own runners can be named.
    expect(
      within(combo(item, "Runner"))
        .getAllByRole("option")
        .map((option) => option.textContent),
    ).toEqual(["Any runner", "desktop", "laptop — offline"]);

    fireEvent.click(within(item).getByRole("button", { name: "Add fallback" }));
    fireEvent.click(within(item).getByRole("button", { name: "Add fallback" }));
    expect(combo(item, "Fallback 1 model").value).toBe("sonnet");
    fireEvent.change(combo(item, "Fallback 2 model"), { target: { value: "opus" } });
    fireEvent.click(within(item).getByRole("button", { name: "Move Fallback 2 up" }));
    expect(combo(item, "Fallback 1 model").value).toBe("opus");
    expect(within(item).getByRole("button", { name: "Move Fallback 1 up" })).toBeDisabled();

    fireEvent.click(within(item).getByRole("checkbox", { name: /Pipeline: implement → review/ }));
    // The reviewer defaults to a different agent than the implementer.
    expect(combo(item, "Implement agent").value).toBe("codex");
    expect(combo(item, "Review agent").value).toBe("claude");
    fireEvent.change(combo(item, "Rounds"), { target: { value: "2" } });

    expect(within(item).getByText("Tag = review → codex/gpt-5")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Save rules" }));

    await waitFor(() => expect(put).toHaveBeenCalledTimes(1));
    expect(put.mock.calls[0][0]).toEqual([
      {
        id: expect.any(String),
        project: "proj",
        match: "tag",
        value: "review",
        target: { agent: "codex", model: "gpt-5" },
        fallbacks: [
          { agent: "claude", model: "opus" },
          { agent: "claude", model: "sonnet" },
        ],
        pipeline: {
          implement: { agent: "codex", model: "gpt-5" },
          review: { agent: "claude", model: "sonnet" },
          maxRounds: 2,
        },
      },
    ]);
    expect(await screen.findByText("Saved")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Save rules" })).toBeDisabled();
  });

  it("reorders and removes rules", async () => {
    vi.spyOn(api, "getRouting").mockResolvedValue({
      rules: [
        rule({ id: "a" }),
        rule({ id: "b", match: "kind", value: "task", project: "" }),
        rule({ id: "c", match: "default", value: undefined, project: "" }),
      ],
    });
    const put = vi.spyOn(api, "putRouting").mockImplementation(async (rules) => ({ rules }));
    render(<RoutingSettings />);
    await screen.findByRole("listitem", { name: "Rule 3" });

    expect(within(ruleItem(1)).getByRole("button", { name: "Move Rule 1 up" })).toBeDisabled();
    expect(within(ruleItem(3)).getByRole("button", { name: "Move Rule 3 down" })).toBeDisabled();
    fireEvent.click(within(ruleItem(3)).getByRole("button", { name: "Move Rule 3 up" }));
    fireEvent.click(within(ruleItem(1)).getByRole("button", { name: "Remove Rule 1" }));
    expect(within(ruleItem(1)).getByText("Global default → claude/opus")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Save rules" }));

    await waitFor(() => expect(put).toHaveBeenCalledTimes(1));
    expect(put.mock.calls[0][0].map((item) => item.id)).toEqual(["c", "b"]);
    expect(put.mock.calls[0][0][0]).toEqual({
      id: "c",
      project: "",
      match: "default",
      target: { agent: "claude", model: "opus" },
    });
  });

  it("validates inline and does not PUT a broken rule", async () => {
    vi.spyOn(api, "getRouting").mockResolvedValue({ rules: [rule({ id: "a" })] });
    const put = vi.spyOn(api, "putRouting");
    render(<RoutingSettings />);
    await screen.findByRole("listitem", { name: "Rule 1" });

    fireEvent.click(screen.getByRole("button", { name: "Add rule" }));
    fireEvent.click(screen.getByRole("button", { name: "Save rules" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("Fix the marked fields (1 rules)");
    const item = ruleItem(2);
    expect(within(item).getByText("Enter a tag.")).toBeInTheDocument();
    expect(within(item).getByText("Choose an agent.")).toBeInTheDocument();
    expect(within(item).getByRole("combobox", { name: "Tag" })).toHaveAttribute("aria-invalid", "true");
    expect(put).not.toHaveBeenCalled();

    // The same tag in the same scope again: the second rule could never match.
    fireEvent.change(within(item).getByRole("combobox", { name: "Tag" }), {
      target: { value: "Review" },
    });
    expect(within(item).getByText(/An earlier rule already matches this/)).toBeInTheDocument();
  });

  it("ties a project default to this project and a global default to none", async () => {
    vi.spyOn(api, "getRouting").mockResolvedValue({ rules: [rule({ id: "a", project: "" })] });
    render(<RoutingSettings />);
    const item = await screen.findByRole("listitem", { name: "Rule 1" });
    expect(combo(item, "Applies to").value).toBe("");

    fireEvent.change(combo(item, "Match"), { target: { value: "project" } });
    expect(combo(item, "Applies to")).toBeDisabled();
    expect(combo(item, "Applies to").value).toBe("proj");
    expect(within(item).queryByRole("combobox", { name: "Tag" })).not.toBeInTheDocument();

    fireEvent.change(combo(item, "Match"), { target: { value: "default" } });
    expect(combo(item, "Applies to").value).toBe("");
  });

  it("types freely when the rule's runner is offline", async () => {
    vi.spyOn(api, "getRouting").mockResolvedValue({ rules: [] });
    const put = vi.spyOn(api, "putRouting").mockImplementation(async (rules) => ({ rules }));
    render(<RoutingSettings />);
    await screen.findByText(/No rules yet/);
    fireEvent.click(screen.getByRole("button", { name: "Add rule" }));
    const item = ruleItem(1);
    fireEvent.change(within(item).getByRole("combobox", { name: "Tag" }), {
      target: { value: "chore" },
    });
    fireEvent.change(combo(item, "Runner"), { target: { value: "r2" } });

    const agent = within(item).getByRole("combobox", { name: "Target agent" });
    expect(agent.tagName).toBe("INPUT");
    fireEvent.change(agent, { target: { value: "gemini" } });
    fireEvent.change(within(item).getByLabelText("Target model"), {
      target: { value: "gemini-2.5-flash" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Save rules" }));
    await waitFor(() => expect(put).toHaveBeenCalled());
    expect(put.mock.calls[0][0][0]).toMatchObject({
      runnerId: "r2",
      target: { agent: "gemini", model: "gemini-2.5-flash" },
    });
  });

  it("caps the fallback chain at five", async () => {
    vi.spyOn(api, "getRouting").mockResolvedValue({ rules: [rule({ id: "a" })] });
    render(<RoutingSettings />);
    const item = await screen.findByRole("listitem", { name: "Rule 1" });
    const add = within(item).getByRole("button", { name: "Add fallback" });
    for (let i = 0; i < 5; i++) fireEvent.click(add);
    expect(add).toBeDisabled();
    expect(within(item).getByText("5 / 5")).toBeInTheDocument();
    fireEvent.click(within(item).getByRole("button", { name: "Remove Fallback 3" }));
    expect(add).toBeEnabled();
  });

  it("shows the hub's 400 and 403 refusals", async () => {
    vi.spyOn(api, "getRouting").mockResolvedValue({ rules: [rule({ id: "a" })] });
    const put = vi
      .spyOn(api, "putRouting")
      .mockRejectedValueOnce(new RequestError(400, "rule 1: unknown agent", "INVALID_ARGUMENT"))
      .mockRejectedValueOnce(new AuthError(403, "agents may not change routing"));
    render(<RoutingSettings />);
    const item = await screen.findByRole("listitem", { name: "Rule 1" });
    fireEvent.change(within(item).getByRole("combobox", { name: "Tag" }), {
      target: { value: "chore" },
    });

    fireEvent.click(screen.getByRole("button", { name: "Save rules" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "The hub refused the rules: rule 1: unknown agent",
    );
    fireEvent.click(screen.getByRole("button", { name: "Save rules" }));
    await waitFor(() => expect(put).toHaveBeenCalledTimes(2));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Not allowed: agents may not change routing",
    );

    // Discarding goes back to what the hub holds.
    fireEvent.click(screen.getByRole("button", { name: "Discard changes" }));
    expect(within(ruleItem(1)).getByRole("combobox", { name: "Tag" })).toHaveValue("review");
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });
});

describe("rule helpers", () => {
  it("flags missing parts per field", () => {
    const problems = ruleProblems([
      rule({ value: "" }),
      rule({ match: "project", project: "", value: undefined }),
      rule({ fallbacks: [{ agent: "claude", model: "" }] }),
      rule({
        value: "x",
        pipeline: { implement: { agent: "a", model: "b" }, review: { agent: "c", model: "d" }, maxRounds: 4 },
      }),
    ]);
    expect(problems[0]).toEqual({ value: "routing.err.value.tag" });
    expect(problems[1]).toEqual({ project: "routing.err.project" });
    expect(problems[2]).toEqual({ fallbacks: "routing.err.fallbacks" });
    expect(problems[3]).toEqual({ pipeline: "routing.err.pipeline" });
  });

  it("trims and drops empty optionals in the payload", () => {
    expect(
      rulesPayload([
        rule({
          value: " ops ",
          runnerId: "",
          target: { agent: " claude ", model: " opus " },
          fallbacks: [],
          pipeline: null,
        }),
      ]),
    ).toEqual([
      { id: "a", project: "proj", match: "tag", value: "ops", target: { agent: "claude", model: "opus" } },
    ]);
  });
});

describe("RoutingSettings: node kinds", () => {
  it("offers the types agents work, custom ones included, and keeps a stale value", async () => {
    useApp.setState({
      graph: {
        version: 1,
        nodes: [],
        nodeTypes: [
          {
            id: "custom-type-1",
            label: "Draft",
            agentMode: "dispatch",
            completion: "anyone",
            countsInProgress: true,
          },
          {
            id: "custom-type-2",
            label: "Character",
            agentMode: "none",
            completion: "none",
            countsInProgress: false,
          },
        ],
      },
    });
    vi.spyOn(api, "getRouting").mockResolvedValue({
      rules: [
        rule({ id: "a", match: "kind", value: "custom-type-1" }),
        rule({ id: "b", match: "kind", value: "custom-type-9" }),
      ],
    });
    render(<RoutingSettings />);
    await screen.findByRole("listitem", { name: "Rule 2" });

    const kind = combo(ruleItem(1), "Kind");
    expect(kind.value).toBe("custom-type-1");
    const options = within(kind).getAllByRole("option").map((option) => option.textContent);
    expect(options).toContain("Draft");
    expect(options).toContain("Goal");
    // Not agent work: not a route.
    expect(options).not.toContain("Character");
    expect(options).not.toContain("Review");
    expect(within(ruleItem(1)).getByText(/Node kind = Draft/)).toBeInTheDocument();

    // A type that is gone stays visible as written.
    const stale = combo(ruleItem(2), "Kind");
    expect(stale.value).toBe("custom-type-9");
    expect(within(stale).getByRole("option", { name: "custom-type-9" })).toBeInTheDocument();
  });
});
