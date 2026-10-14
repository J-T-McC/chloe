// The "mongo" editor language: JavaScript-like highlighting for shell scripts and
// documents, and completion from completeMongo with the connection's collections and
// sampled field names.
import { Mongo, arr } from "../api";
import { MongoCatalog, completeMongo } from "../mongo/complete";
import { monaco, type Monaco } from "./monaco";

export interface MongoEditorContext {
  connId: string;
  /** The database "db" refers to right now. */
  database(): string;
}

const contexts = new Map<string, MongoEditorContext>();
export const setMongoContext = (model: Monaco.editor.ITextModel, ctx: MongoEditorContext) => contexts.set(model.uri.toString(), ctx);
export const clearMongoContext = (model: Monaco.editor.ITextModel) => contexts.delete(model.uri.toString());

// ---- catalog caches ------------------------------------------------------------------

const TTL = 60_000;
const collCache = new Map<string, { at: number; names: Promise<string[]> }>();
const dbCache = new Map<string, { at: number; names: Promise<string[]> }>();
const fieldCache = new Map<string, { at: number; fields: string[] }>();
const fieldLoads = new Map<string, Promise<string[]>>();

function cached<T>(m: Map<string, { at: number; names: Promise<T> }>, key: string, load: () => Promise<T>): Promise<T> {
  const hit = m.get(key);
  if (hit && Date.now() - hit.at < TTL) return hit.names;
  const names = load().catch(() => {
    m.delete(key);
    return [] as unknown as T;
  });
  m.set(key, { at: Date.now(), names });
  return names;
}

const collections = (connId: string, db: string) => cached(collCache, `${connId}/${db}`, async () => arr(await Mongo.Collections(connId, db, false)).map((c) => c.name));
const databases = (connId: string) => cached(dbCache, connId, async () => arr(await Mongo.Databases(connId)).map((d) => d.name));

function fields(connId: string, db: string, coll: string): Promise<string[]> {
  const key = `${connId}/${db}/${coll}`;
  const hit = fieldCache.get(key);
  if (hit && Date.now() - hit.at < TTL) return Promise.resolve(hit.fields);
  let p = fieldLoads.get(key);
  if (!p) {
    p = Mongo.SampleFields(connId, db, coll)
      .then((f) => arr(f).map((x) => x.name))
      .catch(() => [] as string[])
      .then((f) => {
        fieldCache.set(key, { at: Date.now(), fields: f });
        fieldLoads.delete(key);
        return f;
      });
    fieldLoads.set(key, p);
  }
  return p;
}

/** Forget cached names after collections change. */
export function invalidateMongoCatalog(connId: string) {
  for (const m of [collCache, fieldCache]) for (const k of [...m.keys()]) if (k.startsWith(connId + "/")) m.delete(k);
  dbCache.delete(connId);
}

// ---- language -----------------------------------------------------------------------

let registered = false;

export function registerMongoLanguage() {
  if (registered) return;
  registered = true;
  monaco.languages.register({ id: "mongo" });
  monaco.languages.setMonarchTokensProvider("mongo", {
    keywords: ["var", "let", "const", "function", "return", "if", "else", "for", "while", "do", "break", "continue", "new", "of", "in", "true", "false", "null", "undefined", "this", "typeof", "instanceof", "try", "catch", "finally", "throw", "use", "show"],
    builtins: ["db", "ObjectId", "ISODate", "NumberLong", "NumberInt", "NumberDecimal", "Long", "Int32", "Double", "Decimal128", "UUID", "BinData", "HexData", "MD5", "Timestamp", "MinKey", "MaxKey", "Code", "BSONRegExp", "Date", "print", "printjson", "tojson", "sleep", "Math", "JSON"],
    tokenizer: {
      root: [
        [/\/\/.*$/, "comment"],
        [/\/\*/, "comment", "@comment"],
        [/\$[\w$]*/, "predefined"],
        [/[A-Za-z_][\w$]*/, { cases: { "@keywords": "keyword", "@builtins": "type", "@default": "identifier" } }],
        [/"([^"\\]|\\.)*"/, "string"],
        [/'([^'\\]|\\.)*'/, "string"],
        [/`([^`\\]|\\.)*`/, "string"],
        [/"([^"\\]|\\.)*$/, "string"],
        [/'([^'\\]|\\.)*$/, "string"],
        [/-?\d+(\.\d+)?([eE][-+]?\d+)?/, "number"],
        [/[{}()[\]]/, "@brackets"],
        [/[;,.:]/, "delimiter"],
        [/[=<>!+\-*/%&|^~?]+/, "operator"],
      ],
      comment: [
        [/[^/*]+/, "comment"],
        [/\*\//, "comment", "@pop"],
        [/[/*]/, "comment"],
      ],
    },
  } as Monaco.languages.IMonarchLanguage);
  monaco.languages.setLanguageConfiguration("mongo", {
    comments: { lineComment: "//", blockComment: ["/*", "*/"] },
    brackets: [["{", "}"], ["[", "]"], ["(", ")"]],
    autoClosingPairs: [
      { open: "{", close: "}" },
      { open: "[", close: "]" },
      { open: "(", close: ")" },
      { open: '"', close: '"', notIn: ["string"] },
      { open: "'", close: "'", notIn: ["string"] },
    ],
    surroundingPairs: [
      { open: "{", close: "}" },
      { open: "[", close: "]" },
      { open: "(", close: ")" },
      { open: '"', close: '"' },
      { open: "'", close: "'" },
    ],
  });

  const KIND = {
    collection: monaco.languages.CompletionItemKind.Struct,
    method: monaco.languages.CompletionItemKind.Method,
    operator: monaco.languages.CompletionItemKind.Operator,
    field: monaco.languages.CompletionItemKind.Field,
    keyword: monaco.languages.CompletionItemKind.Keyword,
    database: monaco.languages.CompletionItemKind.Module,
    function: monaco.languages.CompletionItemKind.Function,
  };

  monaco.languages.registerCompletionItemProvider("mongo", {
    triggerCharacters: [".", "$", "{", ",", '"', "'", " "],
    async provideCompletionItems(model, position, context) {
      const ctx = contexts.get(model.uri.toString());
      if (!ctx) return { suggestions: [] };
      const text = model.getValue();
      const offset = model.getOffsetAt(position);
      // A space only triggers after "use" / "show".
      if (context.triggerCharacter === " " && !/(^|\n)\s*(use|show)\s$/.test(text.slice(0, offset))) return { suggestions: [] };
      const db = ctx.database();
      const [colls, dbs] = await Promise.all([collections(ctx.connId, db), databases(ctx.connId)]);
      const known: Record<string, string[]> = {};
      const cat: MongoCatalog = { collections: colls, databases: dbs, fields: (c) => known[c] };
      let res = completeMongo(text, offset, cat);
      if (res.collection && colls.includes(res.collection)) {
        known[res.collection] = await fields(ctx.connId, db, res.collection);
        res = completeMongo(text, offset, cat);
      }
      const start = model.getPositionAt(res.replaceStart);
      const range = new monaco.Range(start.lineNumber, start.column, position.lineNumber, position.column);
      return {
        suggestions: res.suggestions.map((s, i) => ({
          label: { label: s.label, description: s.detail },
          kind: KIND[s.kind],
          insertText: s.insertText,
          insertTextRules: s.snippet ? monaco.languages.CompletionItemInsertTextRule.InsertAsSnippet : undefined,
          sortText: String(s.rank).padStart(3, "0") + String(i).padStart(4, "0"),
          range,
        })),
      };
    },
  });
}

/** Editor options shared by the Mongo query editor and document editors. */
export const mongoEditorOptions: Monaco.editor.IStandaloneEditorConstructionOptions = {
  automaticLayout: true,
  minimap: { enabled: false },
  fontSize: 13,
  fontFamily: "SF Mono, Menlo, Consolas, monospace",
  lineNumbersMinChars: 3,
  scrollBeyondLastLine: false,
  tabSize: 2,
  wordBasedSuggestions: "off",
  quickSuggestions: { other: true, comments: false, strings: true },
  suggest: { showWords: false, snippetsPreventQuickSuggestions: false, preview: true },
  suggestSelection: "first",
  fixedOverflowWidgets: true,
};
