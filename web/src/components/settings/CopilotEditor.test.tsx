import { fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { readPreferences } from "../../preferences";
import { useApp } from "../../store";
import { CopilotEditor } from "./CopilotEditor";

const notify = { onError: vi.fn(), onNotice: vi.fn() };

function stub(opts: { admin: boolean; settings?: object; status?: object }) {
  const calls: { url: string; method: string; body?: string }[] = [];
  let settings = {
    provider: "anthropic",
    model: "m",
    baseUrl: "",
    effort: "low",
    allowBypass: false,
    hasApiKey: true,
    apiKeySource: "stored",
    keySource: "auto",
    effectiveKeySource: "server",
    ...opts.settings,
  };
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string, init?: RequestInit) => {
      calls.push({ url, method: init?.method ?? "GET", body: init?.body as string | undefined });
      if (url === "/api/copilot/status") {
        return new Response(
          JSON.stringify({ enabled: true, provider: "anthropic", model: "m", modes: [], allowBypass: false,
            keySource: settings.effectiveKeySource, ...opts.status }),
        );
      }
      if (url === "/api/copilot/settings") {
        if (!opts.admin) return new Response(JSON.stringify({ error: "forbidden" }), { status: 403 });
        if (init?.method === "PUT") {
          const update = JSON.parse(init.body as string);
          settings = { ...settings, keySource: update.keySource, effectiveKeySource: update.keySource === "server" ? "server" : "runner" };
        }
        return new Response(JSON.stringify(settings));
      }
      return new Response("{}");
    }),
  );
  return calls;
}

beforeEach(() => {
  useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
  notify.onError.mockReset();
  notify.onNotice.mockReset();
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("CopilotEditor", () => {
  it("warns about a key on the server and moves it to runners", async () => {
    const calls = stub({ admin: true });
    render(<CopilotEditor notify={notify} />);
    expect(await screen.findByText(/Anyone who breaks into the server can take it/)).toBeInTheDocument();
    expect(screen.getByTestId("copilot-key-status")).toHaveTextContent("Run on the server (m)");
    fireEvent.change(screen.getByLabelText("API key lives on"), { target: { value: "runner" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await vi.waitFor(() => expect(notify.onNotice).toHaveBeenCalledWith("Copilot settings saved."));
    const put = calls.find((call) => call.method === "PUT");
    expect(JSON.parse(put?.body ?? "{}")).toEqual({ keySource: "runner" });
    expect(screen.queryByText(/Anyone who breaks into the server/)).toBeNull();
  });

  it("shows a member where their calls run, and how to set up the runner", async () => {
    stub({
      admin: false,
      status: { enabled: false, keySource: "runner", needsRunner: true, reason: "offline" },
      settings: { effectiveKeySource: "runner" },
    });
    render(<CopilotEditor notify={notify} />);
    expect(await screen.findByText("Only an administrator can change where the key lives.")).toBeInTheDocument();
    expect(screen.getByTestId("copilot-key-status")).toHaveTextContent("Wait for your runner");
    expect(screen.getByText("nodevas runner set copilotProvider anthropic")).toBeInTheDocument();
    expect(screen.queryByLabelText("API key lives on")).toBeNull();
  });

  it("names the runner that makes the calls", async () => {
    stub({ admin: false, status: { keySource: "runner", runner: "desk PC", model: "claude-x" } });
    render(<CopilotEditor notify={notify} />);
    await vi.waitFor(() =>
      expect(screen.getByTestId("copilot-key-status")).toHaveTextContent("Run on your runner “desk PC” (claude-x)"),
    );
  });
});
