// @vitest-environment jsdom
import { describe, expect, it } from "vitest";
import { renderMarkdown } from "./Markdown";

describe("renderMarkdown", () => {
  it("renders Markdown", () => {
    const html = renderMarkdown("# Weekly\n\n**Revenue** fell *12%*.\n\n- a\n- b\n\n| x | y |\n|---|---|\n| 1 | 2 |");
    expect(html).toContain("<h1>Weekly</h1>");
    expect(html).toContain("<strong>Revenue</strong>");
    expect(html).toContain("<li>a</li>");
    expect(html).toContain("<td>1</td>");
  });
  it("never lets HTML or scripts through", () => {
    const html = renderMarkdown('<img src=x onerror="alert(1)"> <script>alert(2)</script> [x](javascript:alert(3)) <b onclick="x()">b</b>');
    const div = document.createElement("div");
    div.innerHTML = html;
    expect(div.querySelectorAll("img, script, b").length).toBe(0);
    for (const el of div.querySelectorAll("*")) {
      for (const a of el.attributes) expect(a.name.startsWith("on") || /javascript:/i.test(a.value)).toBe(false);
    }
    expect(div.textContent).toContain("<script>"); // shown as text
  });
  it("keeps web links", () => {
    expect(renderMarkdown("[docs](https://example.com)")).toContain('href="https://example.com"');
  });
});
