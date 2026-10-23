import { act, render, renderHook, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { readPreferences } from "../preferences";
import { useApp } from "../store";
import { Composer } from "./Composer";
import {
  speechErrorKey,
  stripMarkdown,
  useSpeechRecognition,
  type SpeechRecognitionLike,
} from "./speech";

beforeEach(() => {
  useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
});

afterEach(() => {
  delete window.SpeechRecognition;
  delete window.webkitSpeechRecognition;
});

describe("useSpeechRecognition without browser support", () => {
  it("reports unsupported and start is a no-op", () => {
    const onFinal = vi.fn();
    const { result } = renderHook(() => useSpeechRecognition({ lang: "en-US", onFinal }));
    expect(result.current.supported).toBe(false);
    act(() => result.current.start());
    expect(result.current.listening).toBe(false);
    expect(onFinal).not.toHaveBeenCalled();
  });

  it("disables the mic with a reason, and typing still works", async () => {
    const user = userEvent.setup();
    const { result } = renderHook(() => useSpeechRecognition({ lang: "en-US", onFinal: vi.fn() }));
    const onSend = vi.fn();
    render(<Composer running={false} speech={result.current} onSend={onSend} onStop={vi.fn()} />);
    const mic = screen.getByRole("button", { name: /no speech recognition/ });
    expect(mic).toBeDisabled();
    expect(mic.parentElement).toHaveAttribute("title", expect.stringMatching(/type instead/));
    const input = screen.getByRole("textbox", { name: "Message to the copilot" });
    await user.type(input, "first line{Shift>}{Enter}{/Shift}second{Enter}");
    expect(onSend).toHaveBeenCalledWith("first line\nsecond");
    expect(input).toHaveValue("");
  });
});

class FakeRecognition implements SpeechRecognitionLike {
  static last: FakeRecognition | null = null;
  lang = "";
  interimResults = false;
  continuous = true;
  maxAlternatives = 0;
  onresult: SpeechRecognitionLike["onresult"] = null;
  onerror: SpeechRecognitionLike["onerror"] = null;
  onend: SpeechRecognitionLike["onend"] = null;
  onstart: SpeechRecognitionLike["onstart"] = null;
  started = false;
  constructor() {
    FakeRecognition.last = this;
  }
  start() {
    this.started = true;
  }
  stop() {
    this.onend?.();
  }
  abort() {
    this.onend?.();
  }
  emit(results: { transcript: string; isFinal: boolean }[]) {
    const list = results.map((item) =>
      Object.assign([{ transcript: item.transcript }], { isFinal: item.isFinal }),
    );
    this.onresult?.({ resultIndex: 0, results: list as never });
  }
}

describe("useSpeechRecognition with browser support", () => {
  it("configures one utterance with interim results and hands over the final text", () => {
    window.webkitSpeechRecognition = FakeRecognition;
    const onFinal = vi.fn();
    const { result } = renderHook(() => useSpeechRecognition({ lang: "zh-TW", onFinal }));
    expect(result.current.supported).toBe(true);
    act(() => result.current.start());
    const recognition = FakeRecognition.last!;
    expect(recognition).toMatchObject({ lang: "zh-TW", interimResults: true, continuous: false, started: true });
    expect(result.current.listening).toBe(true);
    act(() => recognition.emit([{ transcript: "把任務", isFinal: false }]));
    expect(result.current.interim).toBe("把任務");
    act(() => recognition.emit([{ transcript: "把任務標成完成", isFinal: true }]));
    act(() => result.current.stop());
    expect(result.current.listening).toBe(false);
    expect(onFinal).toHaveBeenCalledWith("把任務標成完成");
  });

  it("maps not-allowed and no-speech to messages", () => {
    window.SpeechRecognition = FakeRecognition;
    const { result } = renderHook(() => useSpeechRecognition({ lang: "en-US", onFinal: vi.fn() }));
    act(() => result.current.start());
    act(() => FakeRecognition.last!.onerror?.({ error: "not-allowed" }));
    expect(result.current.errorKey).toBe("copilot.speech.notAllowed");
    expect(speechErrorKey("no-speech")).toBe("copilot.speech.noSpeech");
    expect(speechErrorKey("aborted")).toBeNull();
  });
});

describe("stripMarkdown", () => {
  it("keeps the words and drops the markup", () => {
    expect(
      stripMarkdown("## Done\n\n- **Marked** `n1` as [in progress](http://x)\n- See [[proj/n2|the spec]]\n\n```js\ncode()\n```"),
    ).toBe("Done\nMarked n1 as in progress\nSee the spec");
  });
});
