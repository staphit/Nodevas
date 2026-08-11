import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { Peer } from "../api";
import { useApp } from "../store";
import { FOLLOW_PEER_EVENT, PresenceAvatars, type FollowPeerDetail } from "./PresenceAvatars";

function peer(id: string, name: string, extra: Partial<Peer> = {}): Peer {
  return {
    id,
    actor: { id: `u-${id}`, name, role: "member" },
    nodeId: "doc-1",
    color: "#c06",
    ...extra,
  };
}

function seed(peers: Peer[], activeTab: string | null = "doc-1") {
  useApp.setState({ peers, activeTab, selfPeerID: "me", presences: {} });
}

describe("PresenceAvatars", () => {
  // Reset before rather than after: vitest runs afterEach hooks in reverse
  // registration order, so a store write there lands before testing-library
  // unmounts and re-renders a component nobody is watching any more.
  beforeEach(() => seed([]));
  afterEach(() => vi.restoreAllMocks());

  // A single-user install must not carry a permanent hole in its top bar.
  it("renders nothing when nobody else is in the document", () => {
    const { container } = render(<PresenceAvatars />);

    expect(container).toBeEmptyDOMElement();
  });

  // Everyone else in the project is real, but they are not who you are about
  // to collide with.
  it("leaves out peers on another document and this browser itself", () => {
    seed([
      peer("a", "Ada"),
      peer("b", "Bo", { nodeId: "doc-2" }),
      peer("me", "Me"),
    ]);
    render(<PresenceAvatars />);

    expect(screen.getByRole("button", { name: "1 人和你在同一份文件" })).toBeInTheDocument();
  });

  it("collapses the tail of a crowd into a count", () => {
    seed([peer("a", "Ada"), peer("b", "Bo"), peer("c", "Cy"), peer("d", "Di"), peer("e", "Ed")]);
    render(<PresenceAvatars />);

    expect(screen.getByText("+2")).toBeInTheDocument();
  });

  // The bar has room for fewer faces on a phone; the overflow count absorbs it.
  it("keeps fewer faces on a narrow bar", () => {
    seed([peer("a", "Ada"), peer("b", "Bo"), peer("c", "Cy")]);
    render(<PresenceAvatars narrow />);

    expect(screen.getByText("+1")).toBeInTheDocument();
  });

  it("lists everyone with their role and whether they are typing", async () => {
    const user = userEvent.setup();
    seed([peer("a", "Ada", { editing: true }), peer("b", "Bo")]);
    render(<PresenceAvatars />);

    await user.click(screen.getByRole("button", { name: "2 人和你在同一份文件" }));

    expect(screen.getByRole("menuitem", { name: /Ada/ })).toHaveTextContent("成員・編輯中");
    expect(screen.getByRole("menuitem", { name: /Bo/ })).toHaveTextContent("成員・檢視中");
  });

  it("asks the editor to follow the person that was chosen", async () => {
    const user = userEvent.setup();
    seed([peer("a", "Ada")]);
    useApp.setState({
      presences: { a: { nodeId: "doc-1", pageId: "notes", selection: { anchor: 12, head: 12 } } },
    });
    const followed: FollowPeerDetail[] = [];
    const listener = (event: Event) => followed.push((event as CustomEvent<FollowPeerDetail>).detail);
    window.addEventListener(FOLLOW_PEER_EVENT, listener);

    render(<PresenceAvatars />);
    await user.click(screen.getByRole("button", { name: "1 人和你在同一份文件" }));
    await user.click(screen.getByRole("menuitem", { name: /Ada/ }));

    window.removeEventListener(FOLLOW_PEER_EVENT, listener);
    expect(followed).toEqual([
      { peerId: "a", nodeId: "doc-1", pageId: "notes", selection: { anchor: 12, head: 12 } },
    ]);
    expect(screen.queryByRole("menu")).not.toBeInTheDocument();
  });

  it("closes on Escape and hands the keyboard back to the trigger", async () => {
    const user = userEvent.setup();
    seed([peer("a", "Ada")]);
    render(<PresenceAvatars />);

    const trigger = screen.getByRole("button", { name: "1 人和你在同一份文件" });
    await user.click(trigger);
    expect(screen.getByRole("menuitem", { name: /Ada/ })).toHaveFocus();

    await user.keyboard("{Escape}");

    expect(screen.queryByRole("menu")).not.toBeInTheDocument();
    expect(trigger).toHaveFocus();
  });

  it("closes when a press lands outside it", async () => {
    const user = userEvent.setup();
    seed([peer("a", "Ada")]);
    render(
      <div>
        <PresenceAvatars />
        <button type="button">畫布</button>
      </div>,
    );

    await user.click(screen.getByRole("button", { name: "1 人和你在同一份文件" }));
    expect(screen.getByRole("menu")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "畫布" }));

    expect(screen.queryByRole("menu")).not.toBeInTheDocument();
  });
});
