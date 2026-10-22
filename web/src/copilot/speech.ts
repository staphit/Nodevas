/**
 * Browser speech: Web Speech API recognition (push-to-talk) and synthesis
 * (spoken replies). Chrome and Edge have recognition; elsewhere the hook
 * reports `supported: false` and the panel falls back to typing.
 */

import { useCallback, useEffect, useRef, useState } from "react";

// Minimal Web Speech typings: lib.dom does not ship SpeechRecognition.
interface SpeechRecognitionAlternativeLike {
  transcript: string;
}
interface SpeechRecognitionResultLike {
  readonly isFinal: boolean;
  readonly length: number;
  [index: number]: SpeechRecognitionAlternativeLike;
}
interface SpeechRecognitionEventLike {
  readonly resultIndex: number;
  readonly results: { readonly length: number; [index: number]: SpeechRecognitionResultLike };
}
interface SpeechRecognitionErrorEventLike {
  readonly error: string;
}
export interface SpeechRecognitionLike {
  lang: string;
  interimResults: boolean;
  continuous: boolean;
  maxAlternatives: number;
  onresult: ((event: SpeechRecognitionEventLike) => void) | null;
  onerror: ((event: SpeechRecognitionErrorEventLike) => void) | null;
  onend: (() => void) | null;
  onstart: (() => void) | null;
  start(): void;
  stop(): void;
  abort(): void;
}
export type SpeechRecognitionCtor = new () => SpeechRecognitionLike;

declare global {
  interface Window {
    SpeechRecognition?: SpeechRecognitionCtor;
    webkitSpeechRecognition?: SpeechRecognitionCtor;
  }
}

export function speechRecognitionCtor(): SpeechRecognitionCtor | null {
  if (typeof window === "undefined") return null;
  return window.SpeechRecognition ?? window.webkitSpeechRecognition ?? null;
}

/** i18n key for a recognition error, or null for one that is not worth saying. */
export function speechErrorKey(error: string): string | null {
  switch (error) {
    case "not-allowed":
    case "service-not-allowed":
      return "copilot.speech.notAllowed";
    case "no-speech":
      return "copilot.speech.noSpeech";
    case "audio-capture":
      return "copilot.speech.noMic";
    case "network":
      return "copilot.speech.network";
    case "aborted":
      return null;
    default:
      return "copilot.speech.failed";
  }
}

export interface SpeechRecognitionState {
  supported: boolean;
  listening: boolean;
  /** What has been heard so far in this utterance. */
  interim: string;
  /** i18n key of the last error, cleared on the next start. */
  errorKey: string | null;
  start: () => void;
  stop: () => void;
  abort: () => void;
}

/**
 * One utterance per start(): `continuous` is off, so recognition ends on its
 * own after a pause, or when stop() is called (push-to-talk release). The
 * final transcript goes to `onFinal` once, when recognition ends.
 */
export function useSpeechRecognition({
  lang,
  onFinal,
}: {
  lang: string;
  onFinal: (text: string) => void;
}): SpeechRecognitionState {
  const ctor = speechRecognitionCtor();
  const supported = ctor !== null;
  const [listening, setListening] = useState(false);
  const [interim, setInterim] = useState("");
  const [errorKey, setErrorKey] = useState<string | null>(null);
  const recognitionRef = useRef<SpeechRecognitionLike | null>(null);
  const onFinalRef = useRef(onFinal);
  onFinalRef.current = onFinal;

  const start = useCallback(() => {
    const Ctor = speechRecognitionCtor();
    if (!Ctor || recognitionRef.current) return;
    const recognition = new Ctor();
    recognition.lang = lang;
    recognition.interimResults = true;
    recognition.continuous = false;
    recognition.maxAlternatives = 1;
    let finalText = "";
    let heard = "";
    recognition.onresult = (event) => {
      let interimText = "";
      for (let i = event.resultIndex; i < event.results.length; i += 1) {
        const result = event.results[i];
        const transcript = result[0]?.transcript ?? "";
        if (result.isFinal) finalText += transcript;
        else interimText += transcript;
      }
      heard = (finalText + interimText).trim();
      setInterim(heard);
    };
    recognition.onerror = (event) => {
      setErrorKey(speechErrorKey(event.error));
    };
    recognition.onend = () => {
      recognitionRef.current = null;
      setListening(false);
      setInterim("");
      // Some engines drop the last final result when stopped early; what was
      // heard is still what the person said.
      const text = (finalText.trim() || heard).trim();
      if (text) onFinalRef.current(text);
    };
    recognitionRef.current = recognition;
    setErrorKey(null);
    setInterim("");
    setListening(true);
    try {
      recognition.start();
    } catch {
      recognitionRef.current = null;
      setListening(false);
      setErrorKey("copilot.speech.failed");
    }
  }, [lang]);

  const stop = useCallback(() => {
    recognitionRef.current?.stop();
  }, []);

  const abort = useCallback(() => {
    const recognition = recognitionRef.current;
    if (!recognition) return;
    recognition.onend = null;
    recognition.abort();
    recognitionRef.current = null;
    setListening(false);
    setInterim("");
  }, []);

  useEffect(
    () => () => {
      const recognition = recognitionRef.current;
      if (recognition) {
        recognition.onend = null;
        recognition.abort();
      }
    },
    [],
  );

  return { supported, listening, interim, errorKey, start, stop, abort };
}

/** Markdown → plain text worth reading aloud. */
export function stripMarkdown(source: string): string {
  return source
    .replace(/```[\s\S]*?```/g, " ")
    .replace(/`([^`]*)`/g, "$1")
    .replace(/!\[([^\]]*)\]\([^)]*\)/g, "$1")
    .replace(/\[\[([^\]|]+)\|([^\]]*)\]\]/g, "$2")
    .replace(/\[\[([^\]]+)\]\]/g, "$1")
    .replace(/\[([^\]]*)\]\([^)]*\)/g, "$1")
    .replace(/^\s{0,3}#{1,6}\s+/gm, "")
    .replace(/^\s{0,3}>\s?/gm, "")
    .replace(/^\s*[-*+]\s+\[[ xX]\]\s+/gm, "")
    .replace(/^\s*[-*+]\s+/gm, "")
    .replace(/^\s*\d+[.)]\s+/gm, "")
    .replace(/^\s*(?:-{3,}|\*{3,}|_{3,})\s*$/gm, "")
    .replace(/\|/g, " ")
    .replace(/(\*\*|__)(.*?)\1/g, "$2")
    .replace(/(\*|_)(.*?)\1/g, "$2")
    .replace(/~~(.*?)~~/g, "$1")
    .replace(/<[^>]+>/g, "")
    .replace(/[ \t]+/g, " ")
    .replace(/\n{2,}/g, "\n")
    .trim();
}

export function speechSynthesisSupported(): boolean {
  return typeof window !== "undefined" && "speechSynthesis" in window;
}

export function cancelSpeech(): void {
  if (speechSynthesisSupported()) window.speechSynthesis.cancel();
}

export function speakText(markdown: string, lang: string): void {
  if (!speechSynthesisSupported()) return;
  const text = stripMarkdown(markdown);
  window.speechSynthesis.cancel();
  if (!text) return;
  const utterance = new SpeechSynthesisUtterance(text);
  utterance.lang = lang;
  window.speechSynthesis.speak(utterance);
}

export function useSpeechSynthesis(lang: string) {
  const supported = speechSynthesisSupported();
  const speak = useCallback((markdown: string) => speakText(markdown, lang), [lang]);
  useEffect(() => () => cancelSpeech(), []);
  return { supported, speak, cancel: cancelSpeech };
}
