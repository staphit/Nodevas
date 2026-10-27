/**
 * The browser's built-in model (Chrome's Prompt API, Gemini Nano on the
 * device) as the copilot's free default. The server keeps the turn loop, the
 * tools and the permission gate; this only turns one `model_request` into the
 * model's raw answer, which the server parses and validates itself.
 *
 * The Prompt API has no tool calling, so the server asks for JSON shaped by a
 * `responseConstraint` schema: `{"type":"text","text":…}` to reply, or
 * `{"type":"tool_call","name":…,"input":{…}}` to use one of the offered tools.
 */

export type BrowserModelAvailability =
  | "available"
  | "downloadable"
  | "downloading"
  | "unavailable"
  /** No `LanguageModel` at all: not Chrome, too old, or not a desktop. */
  | "unsupported";

export interface BrowserModelMessage {
  role: "user" | "assistant";
  content: string;
}

/** What the server sends in a `model_request` event. */
export interface BrowserModelRequest {
  system: string;
  messages: BrowserModelMessage[];
  schema: Record<string, unknown>;
  attempt?: number;
}

export type ConstrainedOutput =
  | { type: "text"; text: string }
  | { type: "tool_call"; name: string; input: Record<string, unknown> };

// Minimal typing of the Prompt API: only what this file uses. Newer Chrome
// renamed the quota members (contextWindow / measureContextUsage); both are
// tolerated.
interface PromptMessage {
  role: "system" | "user" | "assistant";
  content: string;
}

interface DownloadMonitor {
  addEventListener(type: "downloadprogress", listener: (event: { loaded: number }) => void): void;
}

interface LanguageModelSession {
  prompt(
    input: string,
    options?: { responseConstraint?: Record<string, unknown>; signal?: AbortSignal },
  ): Promise<string>;
  destroy(): void;
  inputQuota?: number;
  inputUsage?: number;
  contextWindow?: number;
  contextUsage?: number;
  measureInputUsage?(input: string): Promise<number>;
  measureContextUsage?(input: string): Promise<number>;
}

interface LanguageModelCreateOptions extends LanguageModelOptions {
  initialPrompts?: PromptMessage[];
  monitor?: (monitor: DownloadMonitor) => void;
  signal?: AbortSignal;
}

interface LanguageModelOptions {
  expectedInputs?: { type: "text"; languages: string[] }[];
  expectedOutputs?: { type: "text"; languages: string[] }[];
}

interface LanguageModelStatic {
  availability(options?: LanguageModelOptions): Promise<string>;
  create(options?: LanguageModelCreateOptions): Promise<LanguageModelSession>;
}

/** Gemini Nano is offered for English text in and out; asking for more makes it unavailable. */
const OPTIONS: LanguageModelOptions = {
  expectedInputs: [{ type: "text", languages: ["en"] }],
  expectedOutputs: [{ type: "text", languages: ["en"] }],
};

/** Without a way to measure tokens, a conservative character budget for the whole input. */
const CHAR_BUDGET = 6000;
/** Share of the quota left free for the answer and the schema the API adds to the input. */
const QUOTA_RESERVE = 0.25;
const AVAILABILITY_TTL_MS = 30_000;

function languageModel(): LanguageModelStatic | null {
  if (typeof self === "undefined" || !("LanguageModel" in self)) return null;
  return (self as unknown as { LanguageModel: LanguageModelStatic }).LanguageModel ?? null;
}

let cached: { value: BrowserModelAvailability; at: number } | null = null;

/** Forgets the cached availability (after a download, or in tests). */
export function resetBrowserModelCache(): void {
  cached = null;
}

/**
 * Whether this browser can run the model now, after a download, or not at
 * all. Cached briefly: the status is fetched often and the answer rarely
 * changes.
 */
export async function browserModelAvailability(refresh = false): Promise<BrowserModelAvailability> {
  const api = languageModel();
  if (!api) return "unsupported";
  if (!refresh && cached && Date.now() - cached.at < AVAILABILITY_TTL_MS) return cached.value;
  let value: BrowserModelAvailability;
  try {
    const answer = await api.availability(OPTIONS);
    value =
      answer === "available" || answer === "downloadable" || answer === "downloading"
        ? answer
        : "unavailable";
  } catch {
    value = "unavailable";
  }
  cached = { value, at: Date.now() };
  return value;
}

/**
 * Downloads the model. The Prompt API only starts a download from a user
 * gesture, so call this straight from a click handler. `onProgress` gets the
 * fraction done, 0 to 1.
 */
export async function downloadBrowserModel(
  onProgress: (fraction: number) => void,
  signal?: AbortSignal,
): Promise<void> {
  const api = languageModel();
  if (!api) throw new Error("This browser has no built-in model.");
  const session = await api.create({
    ...OPTIONS,
    signal,
    monitor(monitor) {
      monitor.addEventListener("downloadprogress", (event) => {
        onProgress(Math.max(0, Math.min(1, Number(event.loaded) || 0)));
      });
    },
  });
  session.destroy();
  resetBrowserModelCache();
}

function isPlainObject(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

/**
 * Reads the model's constrained answer. Null when it is not one the server
 * would accept as such: not JSON, an unknown type, a tool that was not
 * offered, or an input that is not an object.
 */
export function parseConstrainedOutput(raw: string, toolNames: readonly string[]): ConstrainedOutput | null {
  let value: unknown;
  try {
    value = JSON.parse(raw.trim());
  } catch {
    return null;
  }
  if (!isPlainObject(value)) return null;
  if (value.type === "text") {
    return typeof value.text === "string" ? { type: "text", text: value.text } : null;
  }
  if (value.type === "tool_call") {
    if (typeof value.name !== "string" || !toolNames.includes(value.name)) return null;
    const input = value.input === undefined ? {} : value.input;
    if (!isPlainObject(input)) return null;
    return { type: "tool_call", name: value.name, input };
  }
  return null;
}

/** The tool names the server's schema allows, from `properties.name.enum`. */
export function schemaToolNames(schema: Record<string, unknown>): string[] {
  const properties = schema.properties;
  if (!isPlainObject(properties)) return [];
  const name = properties.name;
  if (!isPlainObject(name) || !Array.isArray(name.enum)) return [];
  return name.enum.filter((item): item is string => typeof item === "string");
}

function quotaOf(session: LanguageModelSession): { quota: number; used: number } | null {
  const quota = session.inputQuota ?? session.contextWindow;
  if (typeof quota !== "number" || !Number.isFinite(quota) || quota <= 0) return null;
  const used = session.inputUsage ?? session.contextUsage ?? 0;
  return { quota, used: typeof used === "number" ? used : 0 };
}

function measurer(session: LanguageModelSession): ((input: string) => Promise<number>) | null {
  if (typeof session.measureInputUsage === "function") return (input) => session.measureInputUsage!(input);
  if (typeof session.measureContextUsage === "function") return (input) => session.measureContextUsage!(input);
  return null;
}

/** Keeps the newest messages whose sizes fit `budget`. */
function keepNewest<T>(items: T[], sizes: number[], budget: number): T[] {
  let total = 0;
  let start = items.length;
  for (let index = items.length - 1; index >= 0; index--) {
    if (total + sizes[index] > budget) break;
    total += sizes[index];
    start = index;
  }
  return items.slice(start);
}

/**
 * The history that fits Nano's small context: the oldest messages go first.
 * Measured in the model's own tokens when the API can, else in characters.
 */
async function fitHistory(
  api: LanguageModelStatic,
  request: BrowserModelRequest,
  history: BrowserModelMessage[],
  last: string,
  signal?: AbortSignal,
): Promise<BrowserModelMessage[]> {
  const probe = await api.create({
    ...OPTIONS,
    signal,
    initialPrompts: [{ role: "system", content: request.system }],
  });
  try {
    const quota = quotaOf(probe);
    const measure = measurer(probe);
    if (quota && measure) {
      const lastSize = await measure(last);
      const schemaSize = await measure(JSON.stringify(request.schema));
      const budget = quota.quota * (1 - QUOTA_RESERVE) - quota.used - lastSize - schemaSize;
      const sizes = await Promise.all(history.map((message) => measure(message.content)));
      return keepNewest(history, sizes, Math.max(0, budget));
    }
  } finally {
    probe.destroy();
  }
  const budget = CHAR_BUDGET - request.system.length - last.length;
  return keepNewest(
    history,
    history.map((message) => message.content.length),
    Math.max(0, budget),
  );
}

/**
 * Runs one model call on the device and returns the model's raw answer. A
 * malformed answer is retried once here, where it is cheap; whatever comes
 * back the second time goes to the server, which decides what it is.
 */
export async function runBrowserModel(request: BrowserModelRequest, signal?: AbortSignal): Promise<string> {
  const api = languageModel();
  if (!api) throw new Error("This browser has no built-in model.");
  const messages = request.messages.filter(
    (message) => (message.role === "user" || message.role === "assistant") && typeof message.content === "string",
  );
  const lastMessage = messages[messages.length - 1];
  if (!lastMessage || lastMessage.role !== "user") throw new Error("The model request has no question.");
  const history = await fitHistory(api, request, messages.slice(0, -1), lastMessage.content, signal);
  const toolNames = schemaToolNames(request.schema);
  let raw = "";
  for (let attempt = 0; attempt < 2; attempt++) {
    // A fresh session each attempt: a retry in the same one would see the
    // malformed answer as part of the conversation.
    const session = await api.create({
      ...OPTIONS,
      signal,
      initialPrompts: [{ role: "system", content: request.system }, ...history],
    });
    try {
      raw = await session.prompt(lastMessage.content, { responseConstraint: request.schema, signal });
    } finally {
      session.destroy();
    }
    if (parseConstrainedOutput(raw, toolNames)) break;
    if (signal?.aborted) break;
  }
  return raw;
}
