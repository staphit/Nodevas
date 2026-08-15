/**
 * Reading a .veproj's manifest before it is uploaded [B-06].
 *
 * A single project and a whole-workspace bundle are the same file extension,
 * and the only question the import needs answered — does this archive hold the
 * workspace itself, or one project — decides where its contents land. The
 * server knows, but not until the upload has finished, which is exactly too
 * late to ask the user anything. So the head of the file is read here.
 *
 * A .veproj is a ZIP whose first entry is always manifest.json, so the answer
 * sits in the first few kilobytes and a workspace export of several hundred
 * megabytes never has to be read past them.
 *
 * Every path out of here that is not a confidently-parsed bundle manifest is
 * `null`. This runs in front of an import that has to work regardless: an
 * archive written by a version we do not understand, or one whose head we
 * cannot make sense of, is one we import the old way — never one we refuse.
 */

/** A .veproj that holds the whole workspace rather than a single project. */
export interface BundleManifest {
  /** The workspace's own name, which seeds the folder-name field. */
  name: string;
  /** How many projects the archive restores. */
  projects: number;
}

const MANIFEST_ENTRY = "manifest.json";

/** Local file header signature, "PK\x03\x04". */
const LOCAL_FILE_HEADER = 0x04034b50;

/** General purpose bit 3: the sizes live in a descriptor after the data. */
const FLAG_DATA_DESCRIPTOR = 0x08;

const STORED = 0;
const DEFLATED = 8;

/**
 * Generous enough that no realistic manifest — one line per project and per
 * folder in the workspace — is cut in half, small enough that this stays a
 * single read of the front of the file.
 */
const HEAD_BYTES = 256 * 1024;

/** The bundle the file describes, or null if it is not one we can vouch for. */
export async function readBundleManifest(file: Blob): Promise<BundleManifest | null> {
  try {
    const head = new Uint8Array(await file.slice(0, HEAD_BYTES).arrayBuffer());
    const bytes = await firstEntry(head);
    if (!bytes) return null;
    const manifest: unknown = JSON.parse(new TextDecoder().decode(bytes));
    if (!isBundle(manifest)) return null;
    return {
      name: typeof manifest.name === "string" ? manifest.name : "",
      projects: manifest.projects.length,
    };
  } catch {
    // Truncated archives, encrypted entries, corrupt deflate streams and files
    // that were never ZIPs all arrive here as the same answer: not a bundle.
    return null;
  }
}

function isBundle(
  value: unknown,
): value is { kind: string; name?: unknown; projects: unknown[] } {
  if (typeof value !== "object" || value === null) return false;
  const manifest = value as { kind?: unknown; projects?: unknown };
  return manifest.kind === "bundle" && Array.isArray(manifest.projects);
}

/**
 * The bytes of the archive's first entry, provided it is the manifest. The
 * writer puts manifest.json first precisely so this is possible; an archive
 * that starts with anything else was not written by us, and we stop rather
 * than walk an unknown file looking for a name we recognise.
 */
async function firstEntry(
  head: Uint8Array<ArrayBuffer>,
): Promise<Uint8Array | null> {
  if (head.byteLength < 30) return null;
  const view = new DataView(head.buffer, head.byteOffset, head.byteLength);
  if (view.getUint32(0, true) !== LOCAL_FILE_HEADER) return null;

  const flags = view.getUint16(6, true);
  const method = view.getUint16(8, true);
  const compressedSize = view.getUint32(18, true);
  const nameLength = view.getUint16(26, true);
  const extraLength = view.getUint16(28, true);

  // With bit 3 set the header's sizes are zero and the real ones follow the
  // data, so the entry's end can only be found by inflating until the stream
  // says it is done — and a stream that never says so would read the whole
  // file. A wrong guess here would misreport the archive; not knowing is the
  // better answer.
  if ((flags & FLAG_DATA_DESCRIPTOR) !== 0 || compressedSize === 0) return null;

  const nameEnd = 30 + nameLength;
  if (new TextDecoder().decode(head.subarray(30, nameEnd)) !== MANIFEST_ENTRY) {
    return null;
  }

  const start = nameEnd + extraLength;
  const end = start + compressedSize;
  // A manifest bigger than the slice we read is one we deliberately do not go
  // back to the file for: at that size it is not a manifest we understand.
  if (end > head.byteLength) return null;

  const bytes = head.subarray(start, end);
  if (method === STORED) return bytes;
  if (method !== DEFLATED) return null;
  return inflateRaw(bytes);
}

/**
 * The writer and reader are driven by hand rather than piped through a Blob:
 * the transform's own streams are the only ones involved, so nothing depends
 * on which realm a Blob or a Response happens to come from.
 */
async function inflateRaw(
  bytes: Uint8Array<ArrayBuffer>,
): Promise<Uint8Array | null> {
  if (typeof DecompressionStream === "undefined") return null;
  const stream = new DecompressionStream("deflate-raw");
  const writer = stream.writable.getWriter();
  // Not awaited: the writable only drains as the readable is consumed, so
  // waiting for the write to settle before reading would deadlock. A rejection
  // here (corrupt data) surfaces as a read error below.
  void writer
    .write(bytes)
    .then(() => writer.close())
    .catch(() => undefined);

  const reader = stream.readable.getReader();
  const chunks: Uint8Array[] = [];
  let total = 0;
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    chunks.push(value as Uint8Array);
    total += (value as Uint8Array).byteLength;
  }
  const inflated = new Uint8Array(total);
  let offset = 0;
  for (const chunk of chunks) {
    inflated.set(chunk, offset);
    offset += chunk.byteLength;
  }
  return inflated;
}
