import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { readPreferences } from "../preferences";
import { useApp } from "../store";
import type { PermissionRequest } from "./copilotStore";
import { keyToDecision, parseVoiceDecision } from "./decisions";
import { PermissionCard } from "./PermissionCard";

const request: PermissionRequest = {
  requestId: "r1",
  callId: "c1",
  turnId: "t1",
  name: "set_node_status",
  title: "Set node status",
  kind: "write",
  input: '{\n  "id": "n1"\n}',
  summary: "Mark n1 as in progress",
};

beforeEach(() => {
  useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
});

describe("keyToDecision", () => {
  it("maps 1/2/3", () => {
    expect(keyToDecision("1")).toBe("allow");
    expect(keyToDecision("2")).toBe("allow_session");
    expect(keyToDecision("3")).toBe("deny");
    expect(keyToDecision("4")).toBeNull();
    expect(keyToDecision("Enter")).toBeNull();
  });
});

describe("parseVoiceDecision", () => {
  it.each([
    ["確認", "allow"],
    ["好，允許", "allow"],
    ["是的", "allow"],
    ["Yes please", "allow"],
    ["ok", "allow"],
    ["Allow it", "allow"],
    ["取消", "deny"],
    ["不要", "deny"],
    ["拒絕吧", "deny"],
    ["不好", "deny"],
    ["否", "deny"],
    ["no", "deny"],
    ["Deny.", "deny"],
  ])("%s → %s", (text, expected) => {
    expect(parseVoiceDecision(text)).toBe(expected);
  });

  it("ignores what is neither", () => {
    expect(parseVoiceDecision("I know what to do")).toBeNull();
    expect(parseVoiceDecision("把標題改一下")).toBeNull();
    expect(parseVoiceDecision("")).toBeNull();
  });
});

describe("PermissionCard", () => {
  it("shows the tool, kind, summary and collapsible input", () => {
    render(<PermissionCard request={request} onDecide={vi.fn()} />);
    expect(screen.getByRole("group", { name: "Allow Set node status?" })).toBeInTheDocument();
    expect(screen.getByText("write")).toBeInTheDocument();
    expect(screen.getByText("Mark n1 as in progress")).toBeInTheDocument();
    expect(screen.getByText("Input").closest("details")).not.toHaveAttribute("open");
  });

  it("answers with buttons", async () => {
    const user = userEvent.setup();
    const onDecide = vi.fn();
    render(<PermissionCard request={request} onDecide={onDecide} />);
    await user.click(screen.getByRole("button", { name: /Allow for this conversation/ }));
    await user.click(screen.getByRole("button", { name: /^Deny/ }));
    await user.click(screen.getByRole("button", { name: /^Allow 1/ }));
    expect(onDecide.mock.calls).toEqual([["allow_session"], ["deny"], ["allow"]]);
  });

  it("answers with 1/2/3 while focused, and takes focus when asked", () => {
    const onDecide = vi.fn();
    render(<PermissionCard request={request} autoFocus onDecide={onDecide} />);
    const card = screen.getByRole("group", { name: /^Allow Set/ });
    expect(card).toHaveFocus();
    fireEvent.keyDown(card, { key: "2" });
    fireEvent.keyDown(card, { key: "3" });
    fireEvent.keyDown(card, { key: "1" });
    fireEvent.keyDown(card, { key: "1", ctrlKey: true });
    fireEvent.keyDown(card, { key: "x" });
    expect(onDecide.mock.calls).toEqual([["allow_session"], ["deny"], ["allow"]]);
  });

  it("shows a dispatching tool's full input and asks every time", () => {
    const onDecide = vi.fn();
    const input = JSON.stringify({ goalNodeId: "n1", reviewMode: "agents", autoMerge: true, note: "x".repeat(400) }, null, 2);
    render(
      <PermissionCard
        request={{ ...request, name: "start_autopilot", title: "Start autopilot", kind: "risky", input, everyTime: true }}
        autoFocus
        onDecide={onDecide}
      />,
    );
    const details = screen.getByText("Input").closest("details");
    expect(details).toHaveAttribute("open");
    expect(details?.querySelector("pre")?.textContent).toBe(input);
    expect(screen.queryByRole("button", { name: /Allow for this conversation/ })).toBeNull();
    const card = screen.getByRole("group", { name: /^Allow Start autopilot/ });
    fireEvent.keyDown(card, { key: "2" });
    fireEvent.keyDown(card, { key: "1" });
    expect(onDecide.mock.calls).toEqual([["allow"]]);
  });

  it("does not answer twice while a decision is in flight", () => {
    const onDecide = vi.fn();
    render(<PermissionCard request={{ ...request, submitting: true }} onDecide={onDecide} />);
    fireEvent.keyDown(screen.getByRole("group", { name: /^Allow Set/ }), { key: "1" });
    expect(onDecide).not.toHaveBeenCalled();
    expect(screen.getByRole("button", { name: /^Allow 1/ })).toBeDisabled();
  });
});
