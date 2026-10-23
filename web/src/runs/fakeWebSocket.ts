import { vi } from "vitest";

/** A WebSocket stand-in for tests: records what is sent, and lets a test play
 * the server (open, frames, close). Install with vi.stubGlobal. */
export class FakeWebSocket {
  static readonly CONNECTING = 0;
  static readonly OPEN = 1;
  static readonly CLOSING = 2;
  static readonly CLOSED = 3;
  static instances: FakeWebSocket[] = [];

  static reset(): void {
    FakeWebSocket.instances = [];
  }

  static last(): FakeWebSocket {
    const socket = FakeWebSocket.instances[FakeWebSocket.instances.length - 1];
    if (!socket) throw new Error("no socket opened");
    return socket;
  }

  readonly url: string;
  readyState = FakeWebSocket.CONNECTING;
  binaryType = "blob";
  sent: (string | Uint8Array)[] = [];
  onopen: ((event: Event) => void) | null = null;
  onmessage: ((event: MessageEvent) => void) | null = null;
  onclose: ((event: CloseEvent) => void) | null = null;
  onerror: ((event: Event) => void) | null = null;
  close = vi.fn(() => {
    this.readyState = FakeWebSocket.CLOSED;
  });

  constructor(url: string) {
    this.url = url;
    FakeWebSocket.instances.push(this);
  }

  send(data: string | Uint8Array): void {
    // Copy: the sender may reuse the buffer a view points into.
    this.sent.push(typeof data === "string" ? data : new Uint8Array(data));
  }

  /** Text frames the client sent, parsed. */
  controls(): Record<string, unknown>[] {
    return this.sent.filter((item): item is string => typeof item === "string").map((item) => JSON.parse(item));
  }

  /** Binary frames the client sent, decoded as UTF-8. */
  input(): string[] {
    const decoder = new TextDecoder();
    return this.sent.filter((item): item is Uint8Array => typeof item !== "string").map((item) => decoder.decode(item));
  }

  serverOpen(): void {
    this.readyState = FakeWebSocket.OPEN;
    this.onopen?.(new Event("open"));
  }

  serverText(message: unknown): void {
    this.onmessage?.({ data: JSON.stringify(message) } as MessageEvent);
  }

  serverBinary(text: string): void {
    const bytes = new TextEncoder().encode(text);
    this.onmessage?.({ data: bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.byteLength) } as MessageEvent);
  }

  serverClose(code = 1006, reason = ""): void {
    this.readyState = FakeWebSocket.CLOSED;
    this.onclose?.({ code, reason } as CloseEvent);
  }
}
