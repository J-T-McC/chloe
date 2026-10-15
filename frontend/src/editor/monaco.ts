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

// Where the browser has EditContext, Monaco types into a div rather than a textarea, so
// React Flow (diagram tabs stay mounted) takes Space for panning and swallows it. "nokey"
// tells React Flow to leave keys typed in an editor alone.
monaco.editor.onDidCreateEditor((ed) => ed.getContainerDomNode().classList.add("nokey"));

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
//
// The PostgreSQL grammar lists only reserved words, so UPDATE, SET, INSERT... came out
// plain; every grammar also misses a few statement words. Words that are common column
// names (TYPE, COMMENT, FIRST, LAST, RANGE, ROWS) stay unlisted.
const COMMON_KEYWORDS =
  "SELECT INSERT INTO UPDATE SET DELETE VALUES ALTER ADD DROP CREATE INDEX VIEW TABLE TRUNCATE BEGIN COMMIT ROLLBACK " +
  "EXPLAIN SCHEMA FUNCTION PROCEDURE TRIGGER RENAME EXISTS IF REPLACE CASCADE RESTRICT KEY RECURSIVE BY TRANSACTION " +
  "SAVEPOINT RELEASE END OVER PARTITION INTERVAL ESCAPE TEMPORARY TEMP OFFSET";
const EXTRA_KEYWORDS: Record<string, string> = {
  mysql: COMMON_KEYWORDS,
  pgsql:
    COMMON_KEYWORDS +
    " RETURNING VACUUM ANALYZE COPY GRANT REVOKE CONFLICT DO NOTHING SEQUENCE OWNER MATERIALIZED REFRESH NULLS LANGUAGE" +
    " RETURNS EXTENSION LISTEN NOTIFY UNLOGGED CALL ILIKE SIMILAR LATERAL",
  sql: COMMON_KEYWORDS + " RETURNING PRAGMA ATTACH DETACH REINDEX VACUUM ANALYZE CONFLICT DO NOTHING NULLS",
};
for (const [id, lang] of [["mysql", mysqlLanguage], ["pgsql", pgsqlLanguage], ["sql", sqlLanguage]] as const) {
  const builtinFunctions = (lang.builtinFunctions ?? []).filter((f) => f !== "LEFT" && f !== "RIGHT");
  const keywords = [...new Set([...lang.keywords, ...lang.operators, ...EXTRA_KEYWORDS[id].split(" ")])];
  monaco.languages.setMonarchTokensProvider(id, { ...lang, keywords, operators: [], builtinFunctions } as unknown as Monaco.languages.IMonarchLanguage);
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
