import { beforeAll, describe, expect, it } from "vitest";
import { readBundleManifest } from "./projectArchive";

// jsdom 26 still ships the pre-2019 Blob, without arrayBuffer(); FileReader is
// the same bytes through the API jsdom does implement. Same idea as the
// ResizeObserver stand-in in vitest.setup.ts.
beforeAll(() => {
  if (typeof Blob.prototype.arrayBuffer !== "function") {
    Blob.prototype.arrayBuffer = function readAll(this: Blob) {
      return new Promise<ArrayBuffer>((resolve, reject) => {
        const reader = new FileReader();
        reader.onload = () => resolve(reader.result as ArrayBuffer);
        reader.onerror = () => reject(reader.error);
        reader.readAsArrayBuffer(this);
      });
    };
  }
});

const BUNDLE = JSON.stringify({
  format: "nodevas-project",
  version: 3,
  kind: "bundle",
  name: "workspace",
  projects: [".", "Stellaris", "GameDev"],
  folders: [],
});

const SINGLE = JSON.stringify({
  format: "nodevas-project",
  version: 3,
  name: "Stellaris",
});

/**
 * A local file header written by hand, because the point of the sniffer is
 * that it reads the bytes a real export produces — a zip library in the test
 * would only prove the two agree with each other.
 */
function localEntry({
  name = "manifest.json",
  data,
  method = 0,
  flags = 0,
  compressedSize = data.byteLength,
}: {
  name?: string;
  data: Uint8Array;
  method?: number;
  flags?: number;
  compressedSize?: number;
}): Uint8Array {
  const nameBytes = new TextEncoder().encode(name);
  const entry = new Uint8Array(30 + nameBytes.byteLength + data.byteLength);
  const view = new DataView(entry.buffer);
  view.setUint32(0, 0x04034b50, true);
  view.setUint16(4, 20, true);
  view.setUint16(6, flags, true);
  view.setUint16(8, method, true);
  view.setUint32(14, 0, true);
  view.setUint32(18, compressedSize, true);
  view.setUint32(22, data.byteLength, true);
  view.setUint16(26, nameBytes.byteLength, true);
  view.setUint16(28, 0, true);
  entry.set(nameBytes, 30);
  entry.set(data, 30 + nameBytes.byteLength);
  return entry;
}

async function deflateRaw(bytes: Uint8Array<ArrayBuffer>): Promise<Uint8Array> {
  const stream = new CompressionStream("deflate-raw");
  const writer = stream.writable.getWriter();
  void writer.write(bytes).then(() => writer.close());
  const reader = stream.readable.getReader();
  const chunks: Uint8Array[] = [];
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    chunks.push(value as Uint8Array);
  }
  const total = chunks.reduce((sum, chunk) => sum + chunk.byteLength, 0);
  const out = new Uint8Array(total);
  let offset = 0;
  for (const chunk of chunks) {
    out.set(chunk, offset);
    offset += chunk.byteLength;
  }
  return out;
}

function veproj(...parts: Uint8Array[]): File {
  return new File(parts as BlobPart[], "workspace.veproj");
}

describe("readBundleManifest", () => {
  it("reads a stored manifest and counts the projects it restores", async () => {
    const manifest = await readBundleManifest(
      veproj(localEntry({ data: new TextEncoder().encode(BUNDLE) })),
    );

    expect(manifest).toEqual({ name: "workspace", projects: 3 });
  });

  // Real exports deflate: a sniffer that only understood stored entries would
  // silently answer "not a bundle" for every archive the app actually writes.
  it("inflates a deflated manifest", async () => {
    const data = await deflateRaw(new TextEncoder().encode(BUNDLE));
    const file = veproj(
      localEntry({ data, method: 8 }),
      new Uint8Array([0x50, 0x4b, 0x03, 0x04]),
    );

    expect(await readBundleManifest(file)).toEqual({
      name: "workspace",
      projects: 3,
    });
  });

  it("says nothing about a single-project archive", async () => {
    const manifest = await readBundleManifest(
      veproj(localEntry({ data: new TextEncoder().encode(SINGLE) })),
    );

    expect(manifest).toBeNull();
  });

  // The three ways an archive can be unreadable, each of which must leave the
  // import running exactly as it did before the dialog existed.
  it("gives up on a data-descriptor header rather than guessing the size", async () => {
    const data = new TextEncoder().encode(BUNDLE);
    const file = veproj(
      localEntry({ data, flags: 0x08, compressedSize: 0 }),
      data,
    );

    expect(await readBundleManifest(file)).toBeNull();
  });

  it("gives up when the first entry is not the manifest", async () => {
    const file = veproj(
      localEntry({
        name: "projects/Stellaris/graph.yaml",
        data: new TextEncoder().encode("nodes: []\n"),
      }),
    );

    expect(await readBundleManifest(file)).toBeNull();
  });

  it("returns null instead of throwing on bytes that are not a ZIP", async () => {
    const garbage = new Uint8Array(512);
    garbage.fill(0x41);

    await expect(readBundleManifest(veproj(garbage))).resolves.toBeNull();
    await expect(readBundleManifest(veproj(new Uint8Array(0)))).resolves.toBeNull();
    await expect(
      readBundleManifest(
        veproj(localEntry({ data: new TextEncoder().encode("{not json") })),
      ),
    ).resolves.toBeNull();
    // A header promising more bytes than the file holds — a truncated download.
    await expect(
      readBundleManifest(
        veproj(
          localEntry({
            data: new TextEncoder().encode(BUNDLE),
            compressedSize: 1_000_000,
          }),
        ),
      ),
    ).resolves.toBeNull();
  });
});
