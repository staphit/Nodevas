import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  browserModelAvailability,
  downloadBrowserModel,
  parseConstrainedOutput,
  resetBrowserModelCache,
  runBrowserModel,
  schemaToolNames,
  type BrowserModelRequest,
} from "./browserModel";

type Prompt = { role: string; content: string };

/** A stand-in for Chrome's LanguageModel: records sessions, answers from a script. */
function fakeLanguageModel(opts: {
  availability?: string | (() => Promise<string>);
  answers?: string[];
  quota?: { inputQuota: number; tokensPer: number };
  progress?: number[];
}) {
  const sessions: { initialPrompts: Prompt[]; prompts: { input: string; options: unknown }[]; destroyed: boolean }[] = [];
  const answers = [...(opts.answers ?? [])];
  const api = {
    availability: vi.fn(async () => {
      const value = opts.availability ?? "available";
      return typeof value === "function" ? value() : value;
    }),
    create: vi.fn(async (options: { initialPrompts?: Prompt[]; monitor?: (m: unknown) => void } = {}) => {
      if (options.monitor) {
        const listeners: ((event: { loaded: number }) => void)[] = [];
        options.monitor({ addEventListener: (_: string, listener: (event: { loaded: number }) => void) => listeners.push(listener) });
        for (const loaded of opts.progress ?? []) listeners.forEach((listener) => listener({ loaded }));
      }
      const record = { initialPrompts: options.initialPrompts ?? [], prompts: [] as { input: string; options: unknown }[], destroyed: false };
      sessions.push(record);
      const session: Record<string, unknown> = {
        prompt: vi.fn(async (input: string, promptOptions: unknown) => {
          record.prompts.push({ input, options: promptOptions });
          return answers.shift() ?? '{"type":"text","text":"ok"}';
        }),
        destroy: () => {
          record.destroyed = true;
        },
      };
      if (opts.quota) {
        const per = opts.quota.tokensPer;
        session.inputQuota = opts.quota.inputQuota;
        session.inputUsage = record.initialPrompts.reduce((sum, prompt) => sum + prompt.content.length * per, 0);
        session.measureInputUsage = async (input: string) => input.length * per;
      }
      return session;
    }),
  };
  vi.stubGlobal("LanguageModel", api);
  return { api, sessions };
}

const schema = {
  type: "object",
  properties: {
    type: { type: "string", enum: ["text", "tool_call"] },
    text: { type: "string" },
    name: { type: "string", enum: ["get_node", "set_node_status"] },
    input: { type: "object" },
  },
  required: ["type"],
};

function request(messages: BrowserModelRequest["messages"]): BrowserModelRequest {
  return { system: "You are the copilot.", messages, schema };
}

beforeEach(() => {
  resetBrowserModelCache();
});

afterEach(() => {
  vi.unstubAllGlobals();
  resetBrowserModelCache();
});

describe("browserModelAvailability", () => {
  it("is unsupported without LanguageModel", async () => {
    expect("LanguageModel" in self).toBe(false);
    expect(await browserModelAvailability()).toBe("unsupported");
  });

  it.each(["available", "downloadable", "downloading", "unavailable"])("passes %s through", async (state) => {
    const { api } = fakeLanguageModel({ availability: state });
    expect(await browserModelAvailability()).toBe(state);
    expect(api.availability).toHaveBeenCalledWith(
      expect.objectContaining({ expectedInputs: [{ type: "text", languages: ["en"] }] }),
    );
  });

  it("treats an unknown answer or an error as unavailable", async () => {
    fakeLanguageModel({ availability: "maybe" });
    expect(await browserModelAvailability()).toBe("unavailable");
    resetBrowserModelCache();
    fakeLanguageModel({ availability: async () => Promise.reject(new Error("blocked")) });
    expect(await browserModelAvailability()).toBe("unavailable");
  });

  it("caches the answer until asked to refresh", async () => {
    const { api } = fakeLanguageModel({ availability: "downloadable" });
    await browserModelAvailability();
    await browserModelAvailability();
    expect(api.availability).toHaveBeenCalledTimes(1);
    await browserModelAvailability(true);
    expect(api.availability).toHaveBeenCalledTimes(2);
  });
});

describe("downloadBrowserModel", () => {
  it("reports progress as fractions and closes the session", async () => {
    const { sessions } = fakeLanguageModel({ progress: [0, 0.25, 1.5, 1] });
    const seen: number[] = [];
    await downloadBrowserModel((fraction) => seen.push(fraction));
    expect(seen).toEqual([0, 0.25, 1, 1]);
    expect(sessions).toHaveLength(1);
    expect(sessions[0].destroyed).toBe(true);
  });

  it("fails without the API", async () => {
    await expect(downloadBrowserModel(() => undefined)).rejects.toThrow();
  });
});

describe("parseConstrainedOutput", () => {
  const tools = ["get_node"];
  it("reads text and tool calls", () => {
    expect(parseConstrainedOutput('{"type":"text","text":"hi"}', tools)).toEqual({ type: "text", text: "hi" });
    expect(parseConstrainedOutput(' {"type":"tool_call","name":"get_node","input":{"id":"n1"}} ', tools)).toEqual({
      type: "tool_call",
      name: "get_node",
      input: { id: "n1" },
    });
    expect(parseConstrainedOutput('{"type":"tool_call","name":"get_node"}', tools)).toEqual({
      type: "tool_call",
      name: "get_node",
      input: {},
    });
  });

  it.each([
    "not json",
    '{"type":"text"}',
    '{"type":"shout","text":"x"}',
    '{"type":"tool_call","name":"delete_everything","input":{}}',
    '{"type":"tool_call","name":"get_node","input":[1]}',
    '["text"]',
  ])("rejects %s", (raw) => {
    expect(parseConstrainedOutput(raw, tools)).toBeNull();
  });

  it("finds the offered tool names in the schema", () => {
    expect(schemaToolNames(schema)).toEqual(["get_node", "set_node_status"]);
    expect(schemaToolNames({ type: "object" })).toEqual([]);
  });
});

describe("runBrowserModel", () => {
  it("sends the history as initial prompts and the last message with the response constraint", async () => {
    const { sessions } = fakeLanguageModel({ answers: ['{"type":"tool_call","name":"get_node","input":{"id":"n1"}}'] });
    const raw = await runBrowserModel(
      request([
        { role: "user", content: "hello" },
        { role: "assistant", content: '{"type":"text","text":"hi"}' },
        { role: "user", content: "open n1" },
      ]),
    );
    expect(raw).toBe('{"type":"tool_call","name":"get_node","input":{"id":"n1"}}');
    const used = sessions[sessions.length - 1];
    expect(used.initialPrompts).toEqual([
      { role: "system", content: "You are the copilot." },
      { role: "user", content: "hello" },
      { role: "assistant", content: '{"type":"text","text":"hi"}' },
    ]);
    expect(used.prompts).toEqual([{ input: "open n1", options: expect.objectContaining({ responseConstraint: schema }) }]);
    expect(sessions.every((session) => session.destroyed)).toBe(true);
  });

  it("drops the oldest history to fit the quota", async () => {
    const { sessions } = fakeLanguageModel({ quota: { inputQuota: 1000, tokensPer: 1 } });
    const old = "o".repeat(300);
    const recent = "r".repeat(150);
    await runBrowserModel(
      request([
        { role: "user", content: old },
        { role: "assistant", content: old },
        { role: "user", content: recent },
        { role: "assistant", content: recent },
        { role: "user", content: "now" },
      ]),
    );
    const used = sessions[sessions.length - 1];
    // 1000 * 0.75 - system(20) - "now"(3) - schema - …: the two 150s fit, the 300s do not.
    expect(used.initialPrompts.map((prompt) => prompt.content)).toEqual(["You are the copilot.", recent, recent]);
  });

  it("drops the oldest history to fit a character budget without a quota API", async () => {
    const { sessions } = fakeLanguageModel({});
    const big = "x".repeat(5990);
    await runBrowserModel(
      request([
        { role: "user", content: big },
        { role: "assistant", content: big },
        { role: "user", content: "short" },
        { role: "assistant", content: "fine" },
        { role: "user", content: "now" },
      ]),
    );
    const used = sessions[sessions.length - 1];
    // 6000 characters less the system prompt and the question: a 5990 no longer fits beside the rest.
    expect(used.initialPrompts.map((prompt) => prompt.content)).toEqual(["You are the copilot.", "short", "fine"]);
  });

  it("retries a malformed answer once, in a fresh session", async () => {
    const { sessions } = fakeLanguageModel({ answers: ["{oops", '{"type":"text","text":"fixed"}'] });
    expect(await runBrowserModel(request([{ role: "user", content: "hi" }]))).toBe('{"type":"text","text":"fixed"}');
    const prompted = sessions.filter((session) => session.prompts.length > 0);
    expect(prompted).toHaveLength(2);
    expect(prompted.every((session) => session.prompts.length === 1)).toBe(true);
  });

  it("returns the second malformed answer for the server to judge", async () => {
    fakeLanguageModel({ answers: ["{oops", "still bad"] });
    expect(await runBrowserModel(request([{ role: "user", content: "hi" }]))).toBe("still bad");
  });

  it("refuses a request that does not end with the person", async () => {
    fakeLanguageModel({});
    await expect(runBrowserModel(request([{ role: "assistant", content: "x" }]))).rejects.toThrow();
  });
});
