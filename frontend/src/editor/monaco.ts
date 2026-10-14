// Monaco bootstrap: slim build (SQL languages only), local web worker, Chloe themes.
import type * as Monaco from "monaco-editor";
import * as core from "./monaco-core.js";
import EditorWorker from "monaco-esm/editor/editor.worker.js?worker";
import { onThemeChange, resolvedTheme } from "../theme";

export const monaco = core as unknown as typeof Monaco;
export type { Monaco };

(self as unknown as { MonacoEnvironment: Monaco.Environment }).MonacoEnvironment = {
  getWorker: () => new EditorWorker(),
};

monaco.editor.defineTheme("chloe-light", {
  base: "vs",
  inherit: true,
  rules: [
    { token: "keyword", foreground: "0b57d0", fontStyle: "bold" },
    { token: "string", foreground: "b3261e" },
    { token: "number", foreground: "137333" },
    { token: "comment", foreground: "7a7f87", fontStyle: "italic" },
    { token: "operator", foreground: "5f6368" },
    { token: "predefined", foreground: "8430ce" },
  ],
  colors: { "editor.background": "#ffffff", "editorLineNumber.foreground": "#a0a4ab" },
});

monaco.editor.defineTheme("chloe-dark", {
  base: "vs-dark",
  inherit: true,
  rules: [
    { token: "keyword", foreground: "7cacf8", fontStyle: "bold" },
    { token: "string", foreground: "f28b82" },
    { token: "number", foreground: "81c995" },
    { token: "comment", foreground: "8a8f98", fontStyle: "italic" },
    { token: "predefined", foreground: "c58af9" },
  ],
  colors: { "editor.background": "#1e1f22", "editorLineNumber.foreground": "#5c6068" },
});

export const editorTheme = () => (resolvedTheme() === "dark" ? "chloe-dark" : "chloe-light");

// Every editor follows the app's appearance.
onThemeChange(() => monaco.editor.setTheme(editorTheme()));
