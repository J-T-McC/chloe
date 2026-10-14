// Connects the completion engine to Monaco and keeps a per-database catalog cache.
import { DBType, Explorer, ObjectKind, arr } from "../api";
import { analyze } from "../sql/analyze";
import { CatFunction, CatTable, CompletionCatalog, Suggestion, complete, wantsSuggest } from "../sql/complete";
import { KeyNode, addPath, buildKeyTree, inString, jsonKeySuggestions, jsonPathContext, pathsInText, resolveColumn } from "../sql/jsonpath";
import { connDialect, databasesAsSchemas } from "../dialect";
import { connById } from "../store";
import { Monaco, monaco } from "./monaco";

export interface EditorContext {
  connId: string;
  database?: string;
  schema?: string;
}

const contexts = new Map<string, EditorContext>(); // model URI -> context
export const setEditorContext = (model: Monaco.editor.ITextModel, ctx: EditorContext) => contexts.set(model.uri.toString(), ctx);
export const clearEditorContext = (model: Monaco.editor.ITextModel) => contexts.delete(model.uri.toString());

// ---- catalog cache --------------------------------------------------------------

const catalogs = new Map<string, Promise<CompletionCatalog>>();
const builtins = new Map<string, Promise<string[]>>();
const databases = new Map<string, Promise<string[]>>();

export function invalidateCatalog(connId: string, database?: string) {
  for (const k of [...catalogs.keys()]) if (k.startsWith(connId + "\u0000") && (!database || k === key(connId, database))) catalogs.delete(k);
  if (!database) databases.delete(connId);
}

const key = (connId: string, db: string) => `${connId}\u0000${db}`;

function cached<T>(m: Map<string, Promise<T>>, k: string, load: () => Promise<T>): Promise<T> {
  let p = m.get(k);
  if (!p) {
    p = load();
    m.set(k, p);
    p.catch(() => m.delete(k)); // retry next time
  }
  return p;
}

async function loadCatalog(connId: string, database: string): Promise<CompletionCatalog> {
  const dialect = connDialect(connId);
  const [cat, fns, dbs] = await Promise.all([
    Explorer.Catalog(connId, database),
    cached(builtins, connId, () => Explorer.BuiltinFunctions(connId).then(arr)),
    databasesAsSchemas(dialect) ? cached(databases, connId, () => Explorer.ListDatabases(connId).then(arr)) : Promise.resolve([] as string[]),
  ]);

  // Group flattened columns by table.
  const cols = new Map<string, CatTable["columns"]>();
  for (const c of arr(cat.columns)) {
    const k = (c.schema ?? database) + "\u0000" + c.table;
    (cols.get(k) ?? cols.set(k, []).get(k)!).push({ name: c.name, type: c.type, pk: c.pk, enumValues: c.enumValues ?? undefined });
  }
  const tables: CatTable[] = [];
  const functions: CatFunction[] = fns.map((name) => ({ name, builtin: true }));
  for (const o of arr(cat.objects)) {
    const schema = dialect === "postgres" ? o.schema : database;
    if (o.kind === ObjectKind.KindTable || o.kind === ObjectKind.KindView || o.kind === ObjectKind.KindMatView) {
      tables.push({
        schema,
        name: o.name,
        kind: o.kind === ObjectKind.KindTable ? "table" : o.kind === ObjectKind.KindView ? "view" : "materialized_view",
        comment: o.comment,
        columns: cols.get((schema ?? database) + "\u0000" + o.name) ?? [],
      });
    } else if (o.kind === ObjectKind.KindFunction || o.kind === ObjectKind.KindProcedure) {
      functions.push({ name: o.name, schema, signature: o.signature, returnType: o.returnType, builtin: false });
    }
  }
  return {
    dialect,
    defaultSchema: dialect === "postgres" ? "public" : database,
    schemas: dialect === "postgres" ? arr(cat.schemas) : dbs,
    tables,
    fks: arr(cat.fks).map((f) => ({ ...f, columns: arr(f.columns), refColumns: arr(f.refColumns), schema: f.schema || (databasesAsSchemas(dialect) ? database : undefined), refSchema: f.refSchema || undefined })),
    functions,
  };
}

/** Catalog for an editor, merging other MySQL databases referenced as "db.". */
async function catalogFor(ctx: EditorContext, extraDbs: string[] = []): Promise<CompletionCatalog | null> {
  const conn = connById(ctx.connId);
  if (!conn) return null;
  const db = ctx.database ?? (conn.type === DBType.PostgreSQL ? conn.initialDatabase || "postgres" : conn.type === DBType.SQLite ? "main" : "");
  if (!db) {
    // MySQL without a selected database: only databases are known.
    const dbs = await cached(databases, ctx.connId, () => Explorer.ListDatabases(ctx.connId).then(arr));
    const fns = await cached(builtins, ctx.connId, () => Explorer.BuiltinFunctions(ctx.connId).then(arr));
    return { dialect: "mysql", schemas: dbs, tables: [], fks: [], functions: fns.map((name) => ({ name, builtin: true })) };
  }
  const base = await cached(catalogs, key(ctx.connId, db), () => loadCatalog(ctx.connId, db));
  const cat = { ...base, defaultSchema: conn.type === DBType.PostgreSQL ? ctx.schema || "public" : db };
  if (!extraDbs.length) return cat;
  const extra = await Promise.all(extraDbs.map((d) => cached(catalogs, key(ctx.connId, d), () => loadCatalog(ctx.connId, d)).catch(() => null)));
  for (const e of extra) {
    if (!e) continue;
    cat.tables = [...cat.tables, ...e.tables];
    cat.fks = [...cat.fks, ...e.fks];
  }
  return cat;
}

/** Warm the cache when an editor opens so the first suggestion is instant. */
export function preloadCatalog(ctx: EditorContext) {
  catalogFor(ctx).catch(() => {});
}

// ---- JSON key samples ------------------------------------------------------------

const SAMPLE_TTL = 5 * 60_000;
const samples = new Map<string, { at: number; tree: Promise<KeyNode> }>();

function sampleTree(connId: string, database: string, schema: string | undefined, table: string, column: string): Promise<KeyNode> {
  const k = [connId, database, schema ?? "", table, column].join("\u0000");
  const hit = samples.get(k);
  if (hit && Date.now() - hit.at < SAMPLE_TTL) return hit.tree;
  const tree = Explorer.SampleColumn(connId, { database, schema, name: table, kind: ObjectKind.KindTable }, column, 200).then((v) => buildKeyTree(arr(v)));
  samples.set(k, { at: Date.now(), tree });
  tree.catch(() => samples.delete(k));
  return tree;
}

/** Keys for a JSON path under the cursor, or null when the cursor isn't in one. */
async function jsonCompletion(ctx: EditorContext, cat: CompletionCatalog, text: string, offset: number): Promise<Suggestion[] | null> {
  const jp = jsonPathContext(text, offset, cat.dialect);
  if (!jp) return null;
  const target = resolveColumn(jp, analyze(text, offset, cat.dialect), cat);
  if (!target) return [];
  const db = databasesAsSchemas(cat.dialect) ? (target.schema ?? ctx.database ?? "") : (ctx.database ?? connById(ctx.connId)?.initialDatabase ?? "postgres");
  const sampled = await sampleTree(ctx.connId, db, cat.dialect === "postgres" ? target.schema : undefined, target.table, target.column).catch(() => null);
  // Copy so paths from the text don't leak into the cached sample.
  const tree: KeyNode = sampled ? structuredClone(sampled) : buildKeyTree([]);
  // Paths written elsewhere in the editor (minus the one being typed).
  const here = text.slice(0, jp.replaceStart) + text.slice(offset);
  for (const p of pathsInText(here, cat.dialect).get(target.column.toLowerCase()) ?? []) addPath(tree, p);
  return jsonKeySuggestions(jp, tree);
}

// ---- Monaco provider ----------------------------------------------------------

const KIND: Record<Suggestion["kind"], Monaco.languages.CompletionItemKind> = {
  keyword: monaco.languages.CompletionItemKind.Keyword,
  schema: monaco.languages.CompletionItemKind.Module,
  table: monaco.languages.CompletionItemKind.Class,
  view: monaco.languages.CompletionItemKind.Interface,
  column: monaco.languages.CompletionItemKind.Field,
  alias: monaco.languages.CompletionItemKind.Variable,
  function: monaco.languages.CompletionItemKind.Function,
  join: monaco.languages.CompletionItemKind.Reference,
  snippet: monaco.languages.CompletionItemKind.Snippet,
  cte: monaco.languages.CompletionItemKind.Struct,
  property: monaco.languages.CompletionItemKind.Property,
  enum: monaco.languages.CompletionItemKind.EnumMember,
};

// Characters that only open suggestions inside a JSON path.
const JSON_TRIGGERS = new Set(["'", "{", ","]);

let registered = false;

export function registerSqlCompletion() {
  if (registered) return;
  registered = true;
  for (const lang of ["mysql", "pgsql", "sql"]) {
    monaco.languages.registerCompletionItemProvider(lang, {
      triggerCharacters: [".", "`", '"', "*", "'", "{", ","],
      async provideCompletionItems(model, position, context) {
        const ctx = contexts.get(model.uri.toString());
        if (!ctx) return { suggestions: [] };
        const text = model.getValue();
        const offset = model.getOffsetAt(position);
        let cat = await catalogFor(ctx);
        if (!cat) return { suggestions: [] };
        const json = await jsonCompletion(ctx, cat, text, offset);
        if (json) return { suggestions: toItems(model, position, json, offset) };
        let res = complete(text, offset, cat);
        // Inside strings, and after quote/brace/comma triggers, only values make sense.
        if ((context.triggerCharacter && JSON_TRIGGERS.has(context.triggerCharacter)) || inString(text, offset, cat.dialect)) {
          const values = res.suggestions.filter((s) => s.kind === "enum");
          return { suggestions: toItems(model, position, values, res.replaceStart) };
        }

        // "otherdb." in MySQL: load that database's tables and retry.
        const q = res.analysis.qualifier;
        if (databasesAsSchemas(cat.dialect) && q.length >= 1) {
          const db = q[q.length === 1 ? 0 : q.length - 2];
          const known = cat.schemas.some((s) => s.toLowerCase() === db.toLowerCase());
          if (known && db.toLowerCase() !== (cat.defaultSchema ?? "").toLowerCase()) {
            cat = (await catalogFor(ctx, [db]))!;
            res = complete(text, offset, cat);
          }
        }

        return { suggestions: toItems(model, position, res.suggestions, res.replaceStart), incomplete: false };
      },
    });
  }
}

function toItems(model: Monaco.editor.ITextModel, position: Monaco.Position, list: Suggestion[], replaceStart: number): Monaco.languages.CompletionItem[] {
  const start = model.getPositionAt(replaceStart);
  const range = new monaco.Range(start.lineNumber, start.column, position.lineNumber, position.column);
  const width = String(list.length).length;
  return list.map((s, i): Monaco.languages.CompletionItem => {
    let r = range;
    if (s.rangeStart !== undefined) {
      const rs = model.getPositionAt(s.rangeStart);
      r = new monaco.Range(rs.lineNumber, rs.column, position.lineNumber, position.column);
    }
    return {
      label: { label: s.label, description: s.detail },
      kind: KIND[s.kind],
      insertText: s.insertText,
      insertTextRules: s.snippet ? monaco.languages.CompletionItemInsertTextRule.InsertAsSnippet : undefined,
      filterText: s.filterText ?? s.label,
      // Rank first, then the engine's order (keeps columns in table order).
      sortText: String(Math.round((s.rank + 10) * 10)).padStart(4, "0") + String(i).padStart(width, "0"),
      documentation: s.documentation,
      range: r,
      command: s.retrigger ? { id: "editor.action.triggerSuggest", title: "" } : undefined,
    };
  });
}

/** Open the suggest widget automatically where a name or the next clause is expected. */
export function autoTrigger(editor: Monaco.editor.IStandaloneCodeEditor) {
  return editor.onDidChangeModelContent((e) => {
    const change = e.changes[0];
    // Auto-closed brackets arrive as "()".
    const typed = change?.text === "()" ? "(" : change?.text;
    if (!change || e.changes.length !== 1 || (typed !== " " && typed !== "(")) return;
    const model = editor.getModel();
    const pos = editor.getPosition();
    if (!model || !pos) return;
    const ctx = contexts.get(model.uri.toString());
    if (!ctx) return;
    if (wantsSuggest(model.getValue(), model.getOffsetAt(pos), connDialect(ctx.connId), typed)) {
      // Defer: triggering inside the change event is cancelled by the edit in progress.
      // Close an open list first, or Monaco keeps filtering it ("UNION " still matching
      // "UNION ALL") instead of asking for what follows the keyword.
      setTimeout(() => {
        editor.trigger("chloe", "hideSuggestWidget", {});
        editor.trigger("chloe", "editor.action.triggerSuggest", {});
      }, 0);
    }
  });
}
