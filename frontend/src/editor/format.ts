import { format } from "sql-formatter";
import { Monaco, monaco } from "./monaco";

const LANG = { mysql: "mysql", pgsql: "postgresql" } as const;

/** Pretty-print SQL in the given Monaco language ("mysql" or "pgsql"). */
export function beautify(sql: string, language: string): string {
  return format(sql, {
    language: LANG[language as keyof typeof LANG] ?? "sql",
    keywordCase: "upper",
    tabWidth: 2,
    linesBetweenQueries: 1,
  });
}

/** Beautify the selection, or the whole document when nothing is selected. */
export function beautifyEditor(editor: Monaco.editor.IStandaloneCodeEditor): string | null {
  const model = editor.getModel();
  if (!model) return null;
  const sel = editor.getSelection();
  const range = sel && !sel.isEmpty() ? sel : model.getFullModelRange();
  try {
    const out = beautify(model.getValueInRange(range), model.getLanguageId());
    editor.executeEdits("beautify", [{ range, text: out }]);
    editor.pushUndoStop();
    return null;
  } catch (e) {
    return e instanceof Error ? e.message : String(e);
  }
}

let registered = false;

/** Make "Format Document" / "Format Selection" use the SQL formatter. */
export function registerFormatter() {
  if (registered) return;
  registered = true;
  for (const lang of ["mysql", "pgsql"]) {
    monaco.languages.registerDocumentRangeFormattingEditProvider(lang, {
      provideDocumentRangeFormattingEdits(model, range) {
        return [{ range, text: beautify(model.getValueInRange(range), lang) }];
      },
    });
  }
}
