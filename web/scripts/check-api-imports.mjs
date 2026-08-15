// domain/registry.ts names, for every persisted concept, the store entry points
// allowed to write it — and then nothing enforced it, so components kept
// reaching past the store and calling endpoints directly. Thirty-odd of them do
// today; banning the import outright would fail the build on all of them, so
// this only holds the line: the recorded set may shrink, never grow.
//
// Removing a file from the baseline is the point. Adding one needs a reason.
//
// Tests are exempt: they import the module to mock it, which is not a write
// path. Run with `npm run check:api-imports` in web/.

import { readFileSync, writeFileSync } from "node:fs";
import { readdir } from "node:fs/promises";
import { join, relative, sep } from "node:path";

const root = new URL("..", import.meta.url).pathname.replace(/^\/([A-Za-z]:)/, "$1");
const componentsDir = join(root, "src", "components");
const baselineFile = join(root, "scripts", "api-import-baseline.txt");
const importsAPI = /from\s+"(?:\.\.\/)+api"/;

async function sourceFiles(dir) {
  const found = [];
  for (const entry of await readdir(dir, { withFileTypes: true })) {
    const path = join(dir, entry.name);
    if (entry.isDirectory()) {
      found.push(...(await sourceFiles(path)));
    } else if (/\.tsx?$/.test(entry.name) && !/\.test\.tsx?$/.test(entry.name)) {
      found.push(path);
    }
  }
  return found;
}

const offenders = (await sourceFiles(componentsDir))
  .filter((path) => importsAPI.test(readFileSync(path, "utf8")))
  .map((path) => relative(componentsDir, path).split(sep).join("/"))
  .sort();

if (process.argv.includes("--write")) {
  writeFileSync(baselineFile, offenders.join("\n") + "\n");
  console.log(`recorded ${offenders.length} files`);
  process.exit(0);
}

const baseline = readFileSync(baselineFile, "utf8").split("\n").filter(Boolean);
const added = offenders.filter((path) => !baseline.includes(path));
const fixed = baseline.filter((path) => !offenders.includes(path));

if (added.length) {
  console.error(
    "這些元件直接呼叫 api，繞過 domain/registry.ts 指定的寫入路徑：\n" +
      added.map((path) => `  components/${path}`).join("\n") +
      "\n\n改走 state/ 的 slice command。真的沒有對應概念(例如登入)," +
      "\n就執行 npm run check:api-imports -- --write 把它記進基準線並說明理由。",
  );
  process.exit(1);
}

if (fixed.length) {
  console.error(
    "基準線可以縮短了,這些檔案已經不再直接呼叫 api：\n" +
      fixed.map((path) => `  components/${path}`).join("\n") +
      "\n\n執行 npm run check:api-imports -- --write 更新。",
  );
  process.exit(1);
}

console.log(`api import baseline holding at ${offenders.length} files`);
