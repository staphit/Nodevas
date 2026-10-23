import type { AgentFileUpload } from "../api";

/** The hub's limits on a message's files (W1.5 contract). */
export const MAX_AGENT_FILES = 10;
export const MAX_AGENT_FILE_BYTES = 256 * 1024;
export const MAX_AGENT_FILE_NAME_BYTES = 128;

export type AttachmentErrorCode =
  | "tooMany"
  | "tooLarge"
  | "notText"
  | "badName"
  | "duplicate"
  | "unreadable";

export interface AttachmentError {
  name: string;
  code: AttachmentErrorCode;
}

const encoder = new TextEncoder();

/**
 * The hub's rule for a file name: a base name only — no path separators, no
 * leading dot, at most 128 bytes. Checked here so the user hears about it at
 * the file picker instead of as a 400 when the message is sent.
 */
export function validAttachmentName(name: string): boolean {
  if (!name || name.startsWith(".")) return false;
  if (name.includes("/") || name.includes("\\")) return false;
  // Control characters (a NUL above all) cannot be part of a base name.
  if (/[\u0000-\u001f\u007f]/.test(name)) return false;
  return encoder.encode(name).length <= MAX_AGENT_FILE_NAME_BYTES;
}

function readBytes(file: File): Promise<ArrayBuffer> {
  if (typeof file.arrayBuffer === "function") return file.arrayBuffer();
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(reader.result as ArrayBuffer);
    reader.onerror = () => reject(reader.error);
    reader.readAsArrayBuffer(file);
  });
}

/** UTF-8 text, or null for anything else: invalid UTF-8 or a NUL byte (binary). */
export function decodeText(bytes: ArrayBuffer): string | null {
  const view = new Uint8Array(bytes);
  if (view.includes(0)) return null;
  try {
    let text = new TextDecoder("utf-8", { fatal: true }).decode(view);
    if (text.charCodeAt(0) === 0xfeff) text = text.slice(1);
    return text;
  } catch {
    return null;
  }
}

/**
 * Reads picked files as text and adds them to what is already attached,
 * keeping every file that fits the hub's limits and naming each one that does
 * not. Nothing is thrown: a bad file is an error to show, not a failure.
 */
export async function addAttachments(
  current: AgentFileUpload[],
  picked: File[],
): Promise<{ files: AgentFileUpload[]; errors: AttachmentError[] }> {
  const files = [...current];
  const errors: AttachmentError[] = [];
  for (const file of picked) {
    if (!validAttachmentName(file.name)) {
      errors.push({ name: file.name, code: "badName" });
      continue;
    }
    if (files.some((existing) => existing.name === file.name)) {
      errors.push({ name: file.name, code: "duplicate" });
      continue;
    }
    if (file.size > MAX_AGENT_FILE_BYTES) {
      errors.push({ name: file.name, code: "tooLarge" });
      continue;
    }
    if (files.length >= MAX_AGENT_FILES) {
      errors.push({ name: file.name, code: "tooMany" });
      continue;
    }
    let bytes: ArrayBuffer;
    try {
      bytes = await readBytes(file);
    } catch {
      errors.push({ name: file.name, code: "unreadable" });
      continue;
    }
    // file.size can be stale for a file changed after it was picked.
    if (bytes.byteLength > MAX_AGENT_FILE_BYTES) {
      errors.push({ name: file.name, code: "tooLarge" });
      continue;
    }
    const content = decodeText(bytes);
    if (content === null) {
      errors.push({ name: file.name, code: "notText" });
      continue;
    }
    files.push({ name: file.name, content });
  }
  return { files, errors };
}

/** "12 B", "3.4 KB", "256 KB". */
export function formatFileSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  const kb = bytes / 1024;
  return `${kb < 10 ? kb.toFixed(1) : Math.round(kb)} KB`;
}
