// Edits one data model table with the table designer's tabs. Nothing touches a
// database: OK hands the edited table back to the model.
import { useEffect, useState } from "react";
import { D, DM, Models, arr, errorText } from "../../api";
import { Dialect, Table, toWire } from "../../designer/model";
import { CodeBlock } from "../CodeBlock";
import { ChecksTab, FieldToolbar, FieldsTab, ForeignKeysTab, IndexesTab, OptionsTab, RefSource, UniquesTab } from "../designer/DesignerTabs";
import { Toolbar } from "../Toolbar";
import { Modal, ModalFooter } from "../Modal";

type Section = "fields" | "indexes" | "foreignKeys" | "uniques" | "checks" | "options" | "comment" | "sql";

export function ModelTableDialog(p: {
  model: DM.Model;
  tables: Table[];
  table: Table;
  isNew: boolean;
  dialect: Dialect;
  meta: D.Meta;
  onOK(t: Table): void;
  onCancel(): void;
}) {
  const { dialect, meta } = p;
  const [draft, setDraft] = useState<Table>(p.table);
  const [section, setSection] = useState<Section>("fields");
  const [selected, setSelected] = useState(0);
  const [error, setError] = useState("");
  const [sql, setSql] = useState<string[]>([]);
  const update = (fn: (t: Table) => Table) => setDraft((d) => fn(d));
  const others = p.tables.filter((t) => t !== p.table);
  const schemaOf = (t: Table) => t.schema || p.model.schema;

  // Other tables of the model are the FK targets.
  const refs: RefSource = {
    tables: async () => others.filter((t) => schemaOf(t) === schemaOf(draft)).map((t) => t.name),
    columns: async (fk) => others.find((t) => t.name === fk.refTable && (!fk.refSchema || schemaOf(t) === fk.refSchema))?.columns.map((c) => c.name) ?? null,
  };

  useEffect(() => {
    if (section !== "sql") return;
    Models.TableSQL(p.model, toWire(draft))
      .then((s) => setSql(arr(s)))
      .catch((e) => setSql([`-- ${errorText(e)}`]));
  }, [section, draft, p.model]);

  const ok = () => {
    const name = draft.name.trim();
    if (!name) return setError("Enter a table name.");
    if (others.some((t) => t.name.toLowerCase() === name.toLowerCase() && schemaOf(t) === schemaOf(draft))) return setError(`A table named ${name} already exists in the model.`);
    if (!draft.columns.length) return setError("Add at least one field.");
    p.onOK({ ...draft, name });
  };

  const sections: Array<[Section, string, boolean]> = [
    ["fields", "Fields", true],
    ["indexes", "Indexes", true],
    ["foreignKeys", "Foreign Keys", true],
    ["uniques", "Uniques", dialect === "postgres"],
    ["checks", "Checks", true],
    ["options", "Options", true],
    ["comment", "Comment", true],
    ["sql", "SQL Preview", true],
  ];

  return (
    <Modal title={p.isNew ? "New Table" : `Design Table — ${p.table.name}`} className="model-table-dialog" onClose={p.onCancel} onKeyDown={(e) => (e.metaKey || e.ctrlKey) && e.key === "Enter" && ok()}>
      <div className="designer">
        <Toolbar>
          {section === "fields" && <FieldToolbar table={draft} dialect={dialect} selected={selected} setSelected={setSelected} update={update} />}
          <span className="spacer" />
          <span className="crumbs">
            {dialect === "postgres" && (
              <>
                <input className="name-input schema" placeholder={p.model.schema || "public"} value={draft.schema ?? ""} onChange={(e) => update((t) => ({ ...t, schema: e.target.value || undefined }))} /> ›{" "}
              </>
            )}
            <input className="name-input" autoFocus={p.isNew} placeholder="table name" value={draft.name} onChange={(e) => update((t) => ({ ...t, name: e.target.value }))} />
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
          <div className="error-banner" onClick={() => setError("")}>
            {error}
          </div>
        )}
        <div className="designer-body">
          {section === "fields" && <FieldsTab table={draft} meta={meta} dialect={dialect} selected={selected} onSelect={setSelected} update={update} />}
          {section === "indexes" && <IndexesTab table={draft} meta={meta} dialect={dialect} update={update} />}
          {section === "foreignKeys" && <ForeignKeysTab table={draft} refs={refs} dialect={dialect} update={update} />}
          {section === "uniques" && <UniquesTab table={draft} update={update} />}
          {section === "checks" && <ChecksTab table={draft} dialect={dialect} update={update} />}
          {section === "options" && <OptionsTab table={draft} meta={meta} dialect={dialect} update={update} />}
          {section === "comment" && (
            <textarea className="designer-comment" placeholder="Table comment" value={draft.comment ?? ""} onChange={(e) => update((t) => ({ ...t, comment: e.target.value }))} />
          )}
          {section === "sql" && (
            <div className="designer-sql">
              <CodeBlock code={sql.join(";\n\n") + (sql.length ? ";" : "")} />
            </div>
          )}
        </div>
      </div>
      <ModalFooter onCancel={p.onCancel} onOk={ok} />
    </Modal>
  );
}
