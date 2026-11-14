import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { api, RequestError, type Actor, type Run } from "../api";
import { translate } from "../i18n";
import { askedRun, localRunner } from "../local/localFixtures";
import { readPreferences } from "../preferences";
import { EditionProvider } from "../runners/edition";
import { useRunners } from "../runners/runnerStore";
import { useApp } from "../store";
import { waitingOnYou } from "../station/waiting";
import { RunStatusBadge, runStatusHint } from "./NodeRuns";
import { answerErrorText, OfferAskCard } from "./OfferAskCard";
import { askReasonText, declineFix, declineText, originText, shellAgentOf } from "./offerReasons";
import { runNotifyKind, runNotifyText } from "./runNotify";
import { useRuns } from "./runStore";

const viewer = vi.hoisted(() => ({ current: null as Actor | null }));
vi.mock("../components/SignIn", () => ({ useViewer: () => viewer.current }));

const t = (key: string, values?: Record<string, string | number>) => translate(key, "en", values);

beforeEach(() => {
  viewer.current = null;
  useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
  useRunners.setState({ loaded: true, loading: false, runners: [localRunner()], error: null, creating: false, revoking: {} });
  useRuns.getState().reset();
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe("OfferAskCard", () => {
  it("asks whether to start, says why, and starts with don't-ask-again", async () => {
    const answer = vi.spyOn(api, "answerOffer").mockResolvedValue(askedRun());
    render(<OfferAskCard run={askedRun({ global: true, title: "Fix the login typo" })} />);
    const card = screen.getByRole("group", { name: "Start this run?" });
    expect(card).toHaveTextContent("Codex in my-app: “Fix the login typo”");
    const why = within(card).getByText("Why am I asked?").closest("details")!;
    expect(why).not.toHaveAttribute("open");
    expect(why).toHaveTextContent(
      "Codex is set to run any command without asking, and this run started automatically (by Autopilot), not from your click.",
    );
    expect(card).toHaveTextContent(/It won't start if nobody answers by .+\./);

    fireEvent.click(within(card).getByRole("checkbox", { name: "Don't ask again for Codex's automatic runs" }));
    fireEvent.click(within(card).getByRole("button", { name: "Start" }));
    await waitFor(() => expect(answer).toHaveBeenCalledWith("run1", { start: true, alwaysAllowShell: true }));
  });

  it("declines without touching the agent's setting", async () => {
    const answer = vi.spyOn(api, "answerOffer").mockResolvedValue(null);
    const refresh = vi.spyOn(useRuns.getState(), "refreshRun").mockResolvedValue();
    render(<OfferAskCard run={askedRun()} />);
    fireEvent.click(screen.getByRole("checkbox"));
    fireEvent.click(screen.getByRole("button", { name: "Don't start" }));
    await waitFor(() => expect(answer).toHaveBeenCalledWith("run1", { start: false }));
    expect(refresh).toHaveBeenCalledWith("run1");
  });

  it("has no don't-ask-again box when only every-run asking is the reason", () => {
    render(
      <OfferAskCard
        run={askedRun({
          origin: "person",
          ask: { runId: "run1", where: "app", reasons: [{ code: "ask_every_run" }], expiresAt: "" },
        })}
      />,
    );
    const card = screen.getByRole("group", { name: "Start this run?" });
    expect(within(card).queryByRole("checkbox")).not.toBeInTheDocument();
    expect(card).toHaveTextContent("You asked to confirm every run before it starts.");
    expect(within(card).getByRole("button", { name: "Change when to ask" })).toBeInTheDocument();
  });

  it("says when the run is no longer waiting", async () => {
    vi.spyOn(api, "answerOffer").mockRejectedValue(new RequestError(409, "gone", "not_waiting"));
    vi.spyOn(useRuns.getState(), "refreshRun").mockResolvedValue();
    render(<OfferAskCard run={askedRun()} />);
    fireEvent.click(screen.getByRole("button", { name: "Start" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("It isn't waiting for an answer any more.");
  });

  it("shows a team runner's wait without buttons", () => {
    viewer.current = { id: "u1", name: "bob", role: "member" };
    useRunners.setState({
      runners: [{ ...localRunner(), id: "r9", builtIn: false, ownerId: "u2", ownerName: "Alice" }],
    });
    render(
      <OfferAskCard
        run={askedRun({
          runnerId: "r9",
          runnerName: "alice-pc",
          ask: {
            runId: "run1",
            where: "runner",
            machine: "Alice's PC",
            reasons: [{ code: "not_on_list", missing: "repo" }],
            expiresAt: "2026-11-14T14:32:00Z",
          },
        })}
      />,
    );
    const card = screen.getByTestId("offer-ask");
    expect(card).toHaveTextContent("Waiting for Alice to approve on Alice's PC");
    expect(card).toHaveTextContent("my-app isn't on the “start without asking” list on Alice's PC.");
    expect(within(card).queryByRole("button")).not.toBeInTheDocument();
    expect(within(card).queryByRole("checkbox")).not.toBeInTheDocument();
  });

  it("renders nothing for a run that asks nothing", () => {
    const { container } = render(<OfferAskCard run={askedRun({ ask: null })} />);
    expect(container).toBeEmptyDOMElement();
  });
});

describe("status words", () => {
  it("says Waiting for your OK, and Starting in the single-user edition", () => {
    const { rerender } = render(<RunStatusBadge status="offered" run={askedRun()} />);
    expect(screen.getByText("Waiting for your OK")).toBeInTheDocument();
    rerender(
      <EditionProvider edition="single">
        <RunStatusBadge status="offered" run={askedRun({ ask: null })} />
      </EditionProvider>,
    );
    expect(screen.getByText("Starting…")).toBeInTheDocument();
    rerender(<RunStatusBadge status="offered" run={askedRun({ ask: null })} />);
    expect(screen.getByText("Awaiting acceptance")).toBeInTheDocument();
  });

  it("explains an offered or declined run in the edition's words", () => {
    const offered = askedRun({ ask: null });
    expect(runStatusHint(t, offered, true)).toBe("Starting on this computer…");
    expect(runStatusHint(t, offered, false)).toBe("Waiting for acceptance at the runner.");
    expect(runStatusHint(t, askedRun(), true)).toBe("");
    const declined: Run = { ...askedRun({ ask: null }), status: "declined", declineCode: "timed_out" };
    expect(runStatusHint(t, declined, true)).toBe("Nobody answered in time, so it didn't start.");
    expect(runStatusHint(t, { ...declined, declineCode: "" }, true)).toBe("The runner declined this run.");
  });
});

describe("offer reasons", () => {
  it("names how an automatic run started", () => {
    expect(originText(t, "autopilot")).toBe("by Autopilot");
    expect(originText(t, "handoff")).toBe("continuing after a usage limit");
    expect(originText(t, "person")).toBe("");
    expect(askReasonText(t, { code: "shell_agent", agent: "claude" }, askedRun({ origin: "" }))).toBe(
      "Claude is set to run any command without asking, and this run started automatically, not from your click.",
    );
    expect(askReasonText(t, { code: "from_the_future" }, askedRun())).toBe("");
    expect(shellAgentOf([{ code: "ask_every_run" }])).toBe("");
    expect(shellAgentOf([{ code: "shell_agent" }], "codex")).toBe("codex");
  });

  it("says why a run did not start, with the fix in the single-user edition", () => {
    const run = { ...askedRun(), status: "declined", declineCode: "repo_not_added" };
    expect(declineText(t, run, true)).toBe("This repo isn't added on this computer.");
    expect(declineText(t, { ...run, declineCode: "agent_missing" }, true)).toBe(
      "Codex isn't installed on this computer.",
    );
    expect(declineText(t, { ...run, declineCode: "no_one_there", ask: { ...run.ask!, machine: "Alice's PC" } }, false)).toBe(
      "Nobody approved it on Alice's PC. Open Nodevas Runner there.",
    );
    expect(declineText(t, { ...run, declineCode: "something_new" }, true)).toBe("");
    expect(declineFix("repo_not_added")).toBe("addRepo");
    expect(declineFix("timed_out")).toBeNull();
  });

  it("puts a refused answer in plain words", () => {
    expect(answerErrorText(t, new RequestError(409, "x", "approve_on_runner"), "Alice's PC")).toBe(
      "Approve it on Alice's PC.",
    );
    expect(answerErrorText(t, new RequestError(503, "x", "RUNNER_OFFLINE"), "")).toBe(t("local.unavailable"));
  });
});

describe("ask notification", () => {
  it("tells once per ask that the app's runner waits for an OK", () => {
    const run = askedRun({ global: true, title: "Fix the login typo" });
    expect(runNotifyKind(run)).toEqual({ kind: "ask", key: "run1:ask:2026-11-14T14:32:00Z" });
    expect(runNotifyText(run, "ask")).toEqual({
      title: "Waiting for your OK: Codex in my-app",
      body: "Fix the login typo",
    });
    expect(runNotifyKind(askedRun({ ask: { ...askedRun().ask!, where: "runner" } }))).toBeNull();
  });
});

describe("waiting on you", () => {
  it("lists the built-in runner's offer only while it asks about it", () => {
    const base = { runs: [], asks: [], held: [], reviews: [], canAnswer: () => true, now: Date.now() };
    const starting = askedRun({ id: "run2", ask: null });
    const asked = askedRun();
    const classic = askedRun({ id: "run3", runnerId: "r9", ask: null });
    const single = waitingOnYou({ ...base, active: [starting, asked, classic], builtInRunnerId: "local1" });
    expect(single.map((item) => item.kind === "offer" && item.run.id)).toEqual(["run1", "run3"]);
    const team = waitingOnYou({ ...base, active: [starting, asked, classic] });
    expect(team).toHaveLength(3);
  });
});
