import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { expect, it } from "vitest";

// jsdom applies no CSS, so this reads the contract from 16-agents.css.
// At 150% text in Station's 340px column the recipient list shrank to 137px
// to keep the notify and attach buttons on its line, and the chosen
// "所有 Agent (*)" read "所有 Agent (". It keeps room for that now (about 9em
// with its arrow), and the buttons go to the next line instead.
const css = readFileSync(resolve(process.cwd(), "src/styles/16-agents.css"), "utf8").replace(
  /\/\*[\s\S]*?\*\//g,
  "",
);

function rules(selector: string): string {
  const blocks: string[] = [];
  const pattern = /([^{}]+)\{([^}]*)\}/g;
  for (let match = pattern.exec(css); match; match = pattern.exec(css)) {
    if (match[1].split(",").map((part) => part.trim()).includes(selector)) blocks.push(match[2]);
  }
  return blocks.join("\n");
}

it("never squeezes the recipient list below its 'all agents' choice", () => {
  const select = rules(".agent-composer .agent-to-label select");
  const min = select.match(/min-width:\s*([\d.]+)em/);
  expect(min).not.toBeNull();
  expect(Number(min?.[1])).toBeGreaterThanOrEqual(8.5);
});

it("lets that minimum push the buttons to the next line rather than overflow", () => {
  expect(rules(".agent-composer .agent-to-label")).not.toMatch(/min-width:\s*0/);
  // "收件者" breaks between any two characters, which would hide the
  // minimum from the line-breaking: it stays one word.
  expect(rules(".agent-composer .agent-to-label > span")).toMatch(/white-space:\s*nowrap/);
});
