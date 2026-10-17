// The SQL editor the query tab, the view designer and BI data sources share: one set of
// options, the dialect's language, completion and the formatter.
import { SqlDialect, editorLanguage } from "../dialect";
import { EditorContext, autoTrigger, clearEditorContext, preloadCatalog, registerSqlCompletion, setEditorContext } from "./completion";
import { registerFormatter } from "./format";
import { Monaco, editorTheme, monaco } from "./monaco";

export const sqlEditorOptions: Monaco.editor.IStandaloneEditorConstructionOptions = {
  automaticLayout: true,
  minimap: { enabled: false },
  fontSize: 13,
  fontFamily: "SF Mono, Menlo, Consolas, monospace",
  lineNumbersMinChars: 3,
  scrollBeyondLastLine: false,
  renderLineHighlight: "line",
  tabSize: 2,
  wordBasedSuggestions: "off",
  quickSuggestions: { other: true, comments: false, strings: true },
  suggest: { showWords: false, snippetsPreventQuickSuggestions: false, preview: true, showStatusBar: true },
  suggestSelection: "first",
  fixedOverflowWidgets: true,
};

/**
 * Creates a SQL editor in host. With ctx, completion reads that database's catalog
 * (the query tab sets its own as the database changes). dispose() frees the editor,
 * its model and its completion context.
 */
export function createSqlEditor(host: HTMLElement, text: string, dialect: SqlDialect, ctx?: EditorContext) {
  registerSqlCompletion();
  registerFormatter();
  const model = monaco.editor.createModel(text, editorLanguage(dialect));
  const editor = monaco.editor.create(host, { ...sqlEditorOptions, model, theme: editorTheme() });
  if (ctx) {
    setEditorContext(model, ctx);
    preloadCatalog(ctx);
  }
  const trig = autoTrigger(editor);
  return {
    editor,
    model,
    dispose() {
      trig.dispose();
      clearEditorContext(model);
      editor.dispose();
      model.dispose();
    },
  };
}
