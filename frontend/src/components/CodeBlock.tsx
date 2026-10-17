import { useEffect, useState } from "react";
import { editorTheme, monaco } from "../editor/monaco";

/** Read-only syntax-highlighted SQL using Monaco's colorizer (no editor instance). */
export function CodeBlock({ code, language = "sql" }: { code: string; language?: string }) {
  const [html, setHtml] = useState("");
  useEffect(() => {
    let live = true;
    monaco.editor.setTheme(editorTheme());
    monaco.editor.colorize(code, language, { tabSize: 2 }).then((h) => live && setHtml(h));
    return () => {
      live = false;
    };
  }, [code, language]);
  return <pre className="code-block" dangerouslySetInnerHTML={{ __html: html }} />;
}
