// Monaco bootstrap: slim build (SQL languages only), local web worker, Chloe themes.
import type * as Monaco from "monaco-editor";
import * as core from "./monaco-core.js";
import EditorWorker from "monaco-esm/editor/editor.worker.js?worker";
import { onThemeChange, resolvedTheme } from "../theme";
import { language as mysqlLanguage } from "monaco-esm/languages/definitions/mysql/mysql.js";
import { language as pgsqlLanguage } from "monaco-esm/languages/definitions/pgsql/pgsql.js";
import { language as sqlLanguage } from "monaco-esm/languages/definitions/sql/sql.js";

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

// Monaco's SQL grammars tag word operators (JOIN, UNION, AND, LIKE...) as operators,
// which the themes grey out; highlight them as keywords like the rest. Symbolic
// operators (=, <, +...) have their own rule and stay operators. LEFT and RIGHT are
// also string functions, but in a query they are mostly joins.
for (const [id, lang] of [["mysql", mysqlLanguage], ["pgsql", pgsqlLanguage], ["sql", sqlLanguage]] as const) {
  const builtinFunctions = (lang.builtinFunctions ?? []).filter((f) => f !== "LEFT" && f !== "RIGHT");
  monaco.languages.setMonarchTokensProvider(id, { ...lang, keywords: [...lang.keywords, ...lang.operators], operators: [], builtinFunctions } as unknown as Monaco.languages.IMonarchLanguage);
}

export const editorTheme = () => (resolvedTheme() === "dark" ? "chloe-dark" : "chloe-light");

// Every editor follows the app's appearance.
onThemeChange(() => monaco.editor.setTheme(editorTheme()));

// A light JSON tokenizer for value panels (the full JSON language service isn't bundled).
monaco.languages.register({ id: "json" });
monaco.languages.setMonarchTokensProvider("json", {
  tokenizer: {
    root: [
      [/"(?:[^"\\]|\\.)*"(?=\s*:)/, "type"], // keys
      [/"(?:[^"\\]|\\.)*"/, "string"],
      [/-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?/, "number"],
      [/\b(?:true|false|null)\b/, "keyword"],
      [/[{}[\],:]/, "delimiter"],
    ],
  },
});
monaco.languages.setLanguageConfiguration("json", {
  brackets: [
    ["{", "}"],
    ["[", "]"],
  ],
  autoClosingPairs: [
    { open: "{", close: "}" },
    { open: "[", close: "]" },
    { open: '"', close: '"', notIn: ["string"] },
  ],
});
