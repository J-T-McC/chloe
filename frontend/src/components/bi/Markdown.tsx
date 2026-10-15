// Markdown for dashboard text and agents' notes. Agents write it, so the HTML is
// sanitized (no raw HTML, scripts or event handlers) and links open in the
// system browser instead of navigating Chloe's window.
import { Browser } from "@wailsio/runtime";
import DOMPurify from "dompurify";
import { marked } from "marked";
import { useMemo } from "react";

const escapeHtml = (s: string) => s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");

/** Markdown to safe HTML: inline HTML in the source is shown as text. */
export function renderMarkdown(md: string): string {
  const html = marked.parse(md, {
    async: false,
    gfm: true,
    breaks: true,
    walkTokens(token) {
      if (token.type === "html") {
        const t = token as { type: string; text: string; raw: string };
        Object.assign(token, { type: "text", text: escapeHtml(t.raw), escaped: true });
      }
    },
  }) as string;
  return DOMPurify.sanitize(html, {
    ALLOWED_TAGS: ["h1", "h2", "h3", "h4", "h5", "h6", "p", "br", "hr", "strong", "em", "del", "code", "pre", "blockquote", "ul", "ol", "li", "a", "table", "thead", "tbody", "tr", "th", "td", "span"],
    ALLOWED_ATTR: ["href", "title", "align"],
    ALLOWED_URI_REGEXP: /^(https?:|mailto:)/i,
  });
}

export function Markdown({ text, className }: { text: string; className?: string }) {
  const html = useMemo(() => renderMarkdown(text), [text]);
  return (
    <div
      className={"bi-md " + (className ?? "")}
      dangerouslySetInnerHTML={{ __html: html }}
      onClick={(e) => {
        const a = (e.target as HTMLElement).closest("a");
        if (!a) return;
        e.preventDefault();
        const href = a.getAttribute("href");
        if (href) Browser.OpenURL(href).catch(() => window.open(href, "_blank", "noopener"));
      }}
    />
  );
}
