import { fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { readPreferences } from "../../preferences";
import { useApp } from "../../store";
import { CopilotEditor } from "./CopilotEditor";

const notify = { onError: vi.fn(), onNotice: vi.fn() };

function stub(opts: { admin: boolean; settings?: object; status?: object }) {
  const calls: { url: string; method: string; body?: string }[] = [];
  let settings: Record<string, unknown> = {
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
          if ("keySource" in update) {
            settings = { ...settings, keySource: update.keySource, effectiveKeySource: update.keySource === "server" ? "server" : "runner" };
          }
          if ("provider" in update) {
            settings = { ...settings, provider: update.provider, automatic: update.provider === "auto" };
          }
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
    const keyRow = screen.getByLabelText("API key lives on").closest(".settings-row") as HTMLElement;
    fireEvent.click(within(keyRow).getByRole("button", { name: "Save" }));
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

  it("offers the automatic free model as the default provider and saves a choice", async () => {
    const calls = stub({ admin: true, settings: { provider: "anthropic", automatic: true } });
    render(<CopilotEditor notify={notify} />);
    const select = (await screen.findByLabelText("Model")) as HTMLSelectElement;
    // A key resolved automatic mode to Anthropic; the choice is still automatic.
    expect(select.value).toBe("auto");
    expect(screen.getByRole("option", { name: "Automatic (free: browser or local model)" })).toBeInTheDocument();
    fireEvent.change(select, { target: { value: "openai" } });
    fireEvent.click(within(select.closest(".settings-row") as HTMLElement).getByRole("button", { name: "Save" }));
    await vi.waitFor(() => expect(notify.onNotice).toHaveBeenCalledWith("Copilot settings saved."));
    const put = calls.find((call) => call.method === "PUT");
    expect(JSON.parse(put?.body ?? "{}")).toEqual({ provider: "openai" });
  });

  it("says when calls run on the browser's model", async () => {
    stub({ admin: false, status: { provider: "browser", model: "gemini-nano", keySource: "browser", modelHost: "browser", automatic: true } });
    render(<CopilotEditor notify={notify} />);
    await vi.waitFor(() =>
      expect(screen.getByTestId("copilot-key-status")).toHaveTextContent("Run in your browser (Gemini Nano, free)"),
    );
  });

  it("names a local model and the machine it runs on", async () => {
    stub({
      admin: false,
      status: { provider: "openai", model: "qwen3:8b", keySource: "server", modelHost: "server", localServer: "ollama", serverIsLocal: false, automatic: true },
    });
    render(<CopilotEditor notify={notify} />);
    await vi.waitFor(() =>
      expect(screen.getByTestId("copilot-key-status")).toHaveTextContent("Run on Ollama · qwen3:8b on the server (free)"),
    );
  });

  it("tells what to set up when no free model is available", async () => {
    stub({
      admin: false,
      status: { enabled: false, needsSetup: true, reasonCode: "no_model", reason: "no free model", keySource: "runner" },
    });
    render(<CopilotEditor notify={notify} />);
    expect(await screen.findByText("ollama pull qwen3:8b")).toBeInTheDocument();
    expect(screen.getByTestId("copilot-key-status")).toHaveTextContent("Wait for a free model");
  });
});
