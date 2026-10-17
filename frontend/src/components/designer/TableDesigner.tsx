import { Save } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { D, Design, ObjectKind, arr, errorText } from "../../api";
import { Dialect, Table, newTable, normalize, toWire } from "../../designer/model";
import { invalidateCatalog } from "../../editor/completion";
import { Tab, connById, useApp } from "../../store";
import { useTree } from "../../tree";
import { connDialect } from "../../dialect";
import { confirm } from "../Dialogs";
import { CodeBlock } from "../ObjectsPane";
import { ChecksTab, FieldToolbar, FieldsTab, ForeignKeysTab, connectionRefs, IndexesTab, OptionsTab, TriggersTab, UniquesTab } from "./DesignerTabs";
import { Toolbar } from "../Toolbar";
import { useAsync } from "../useAsync";
import { useSavableTab } from "../useSavableTab";

type Section = "fields" | "indexes" | "foreignKeys" | "uniques" | "checks" | "triggers" | "options" | "comment" | "sql";

export function TableDesigner({ tab }: { tab: Tab }) {
  const connId = tab.connId!;
  const dialect: Dialect = connDialect(connId);
  const isNew = !tab.ref?.name;
  const [original, setOriginal] = useState<Table | null>(null);
  const [draft, setDraft] = useState<Table | null>(null);
  const blank = useRef<Table | null>(null);
  const [meta, setMeta] = useState<D.Meta | null>(null);
  const [section, setSection] = useState<Section>("fields");
  const [selected, setSelected] = useState(0);
  const [error, setError] = useState<{ text: string; sql?: string } | null>(null);
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    let live = true;
    (async () => {
      try {
        const m = await Design.Meta(connId, tab.database!);
        if (!live) return;
        setMeta(m);
        if (isNew) {
          setOriginal(null);
          blank.current = newTable(dialect, tab.database!, tab.schema, m);
          setDraft(blank.current);
        } else {
          const t = normalize(await Design.Load(connId, tab.ref!));
          if (!live) return;
          setOriginal(t);
          setDraft(t);
        }
      } catch (e) {
        if (live) setError({ text: errorText(e) });
      }
    })();
    return () => {
      live = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [connId]);

  // Live SQL preview of pending changes.
  // An answer for an older draft is dropped.
  const { data: preview } = useAsync(
    draft &&
      (() =>
        Design.Preview(connId, original, toWire(draft)).then(
          (p) => ({ statements: arr(p.statements), warnings: arr(p.warnings) }),
          (e) => ({ statements: [] as string[], warnings: [errorText(e)] }),
        )),
    [draft, original, connId],
    null as { statements: string[]; warnings: string[] } | null,
    { delay: 150, keep: true },
  );

  const dirty = useMemo(() => (preview?.statements.length ?? 0) > 0 || (isNew && !!draft), [preview, isNew, draft]);
  // A new table only needs saving once it's been touched.
  const saveRef = useSavableTab(tab.id, isNew ? !!draft && draft !== blank.current : dirty);
  useEffect(() => {
    useApp.getState().updateTab(tab.id, { title: `${draft?.name || "Untitled"} (design)` });
  }, [draft?.name, tab.id]);

  if (!draft || !meta) return <div className="center-hint">{error ? <span className="err">{error.text}</span> : "Loading…"}</div>;

  const update = (fn: (t: Table) => Table) => setDraft((d) => (d ? fn(d) : d));

  const save = async (): Promise<boolean> => {
    if (!draft.name.trim()) {
      setSection("options");
      setError({ text: "Enter a table name before saving." });
      return false;
    }
    const p = await Design.Preview(connId, original, toWire(draft)).catch((e) => {
      setError({ text: errorText(e) });
      return null;
    });
    if (!p) return false;
    const statements = arr(p.statements);
    if (!statements.length) return false;
    const warnings = arr(p.warnings);
    if (warnings.length && !(await confirm("Save changes?", { message: warnings.join("\n") + "\n\n" + statements.join(";\n"), okLabel: "Save" }))) return false;
    setSaving(true);
    setError(null);
    try {
      const res = await Design.Apply(connId, original, toWire(draft));
      if (res.error) {
        setError({ text: res.error + (res.executed ? `\n(${res.executed} statement(s) before it were applied)` : ""), sql: res.failedStatement });
        return false;
      }
      const t = normalize(res.table!);
      setOriginal(t);
      setDraft(t);
      const ref = { database: t.database, schema: t.schema, name: t.name, kind: ObjectKind.KindTable };
      useApp.getState().updateTab(tab.id, { ref, title: `${t.name} (design)` });
      invalidateCatalog(connId, t.database);
      refreshTablesFolder(connId, t.database, t.schema);
      useApp.setState((s) => ({ objectsVersion: s.objectsVersion + 1 }));
      return true;
    } catch (e) {
      setError({ text: errorText(e) });
      return false;
    } finally {
      setSaving(false);
    }
  };
  saveRef.current = save;

  const sections: Array<[Section, string, boolean]> = [
    ["fields", "Fields", true],
    ["indexes", "Indexes", true],
    ["foreignKeys", "Foreign Keys", true],
    ["uniques", "Uniques", dialect !== "mysql"],
    ["checks", "Checks", true],
    ["triggers", "Triggers", true],
    ["options", "Options", true],
    ["comment", "Comment", dialect !== "sqlite"],
    ["sql", `SQL Preview${preview?.statements.length ? ` (${preview.statements.length})` : ""}`, true],
  ];

  return (
    <div
      className="designer"
      onKeyDown={(e) => {
        if ((e.metaKey || e.ctrlKey) && e.key === "s") {
          e.preventDefault();
          save();
        }
      }}
    >
      <Toolbar>
        <button className={dirty ? "accent" : ""} disabled={!dirty || saving} onClick={save} title="Save (⌘S)">
          <Save size={14} /> {saving ? "Saving…" : "Save"}
        </button>
        <span className="tb-sep" />
        {section === "fields" && <FieldToolbar table={draft} dialect={dialect} selected={selected} setSelected={setSelected} update={update} />}
        <span className="spacer" />
        <span className="crumbs">
          {[connById(connId)?.name, draft.database, draft.schema].filter(Boolean).join(" › ")} ›{" "}
          <input className="name-input" placeholder="table name" value={draft.name} onChange={(e) => update((t) => ({ ...t, name: e.target.value }))} />
        </span>
      </Toolbar>
      <div className="seg-tabs designer-tabs">
        {sections
          .filter(([, , on]) => on)
          .map(([k, label]) => (
            <button key={k} className={section === k ? "active" : ""} onClick={() => setSection(k)}>
              {label}
            </button>
          ))}
      </div>
      {error && (
        <div className="error-banner" onClick={() => setError(null)}>
          {error.text}
          {error.sql && <pre>{error.sql}</pre>}
        </div>
      )}
      <div className="designer-body">
        {section === "fields" && <FieldsTab table={draft} meta={meta} dialect={dialect} selected={selected} onSelect={setSelected} update={update} />}
        {section === "indexes" && <IndexesTab table={draft} meta={meta} dialect={dialect} update={update} />}
        {section === "foreignKeys" && <ForeignKeysTab table={draft} refs={connectionRefs(connId, draft)} dialect={dialect} update={update} />}
        {section === "uniques" && <UniquesTab table={draft} update={update} />}
        {section === "checks" && <ChecksTab table={draft} dialect={dialect} update={update} />}
        {section === "triggers" && <TriggersTab table={draft} meta={meta} dialect={dialect} update={update} />}
        {section === "options" && <OptionsTab table={draft} meta={meta} dialect={dialect} update={update} />}
        {section === "comment" && (
          <textarea className="designer-comment" placeholder="Table comment" value={draft.comment ?? ""} onChange={(e) => update((t) => ({ ...t, comment: e.target.value }))} />
        )}
        {section === "sql" && (
          <div className="designer-sql">
            {preview?.warnings?.map((w, i) => (
              <div key={i} className="badge warn block">
                {w}
              </div>
            ))}
            {preview?.statements.length ? <CodeBlock code={preview.statements.join(";\n\n") + ";"} /> : <div className="center-hint">No changes.</div>}
          </div>
        )}
      </div>
    </div>
  );
}

function refreshTablesFolder(connId: string, database: string, schema?: string) {
  const tree = useTree.getState();
  const key = [connId, database, schema, "#table"].filter(Boolean).join("/");
  if (tree.children[key]) tree.refresh({ key, type: "folder", label: "", connId, database, schema, kind: ObjectKind.KindTable, depth: 0, expandable: true });
}
