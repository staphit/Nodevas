// @vitest-environment node
import { describe, expect, it } from "vitest";
import config from "../vite.config";

// The entry chunk imports React from whichever chunk Rollup put it in, and
// index.html modulepreloads every chunk the entry imports statically. React in
// a CodeMirror chunk therefore meant the editor was downloaded on first paint.
type ManualChunks = (id: string) => string | undefined;

function manualChunks(): ManualChunks {
  const output = config.build?.rollupOptions?.output;
  const single = Array.isArray(output) ? output[0] : output;
  const fn = single?.manualChunks;
  if (typeof fn !== "function") throw new Error("manualChunks is not a function");
  return (id) => (fn as unknown as ManualChunks)(id) ?? undefined;
}

const nm = "/repo/web/node_modules";

describe("build chunks", () => {
  it("puts React in a chunk of its own, apart from the editor", () => {
    const chunkOf = manualChunks();
    expect(chunkOf(`${nm}/react/index.js`)).toBe("react");
    expect(chunkOf(`${nm}/react/cjs/react.production.min.js`)).toBe("react");
    expect(chunkOf(`${nm}/react/jsx-runtime.js`)).toBe("react");
    expect(chunkOf(`${nm}/react-dom/client.js`)).toBe("react");
    expect(chunkOf(`${nm}/react-dom/cjs/react-dom.production.min.js`)).toBe("react");
    expect(chunkOf(`${nm}/scheduler/index.js`)).toBe("react");
  });

  it("leaves look-alike packages and the editor chunks where they were", () => {
    const chunkOf = manualChunks();
    expect(chunkOf(`${nm}/@uiw/react-codemirror/esm/index.js`)).toBe("editor-react");
    expect(chunkOf(`${nm}/@codemirror/view/dist/index.js`)).toBe("editor-core");
    expect(chunkOf(`${nm}/@lezer/common/dist/index.js`)).toBe("editor-parser");
    expect(chunkOf(`${nm}/react-is/index.js`)).toBeUndefined();
    expect(chunkOf("/repo/web/src/components/react/Thing.tsx")).toBeUndefined();
  });
});
