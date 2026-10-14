import { ArrowDownWideNarrow, ArrowUpNarrowWide, KeyRound, Plus, Trash2, X } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { D, Explorer, ObjectKind, arr } from "../../api";
import { Column, Dialect, ForeignKey, Index, Table, Trigger, Unique, renameColumn, takesLength, takesScale, isTextType, uniqueName } from "../../designer/model";

type Update = (fn: (t: Table) => Table) => void;

const FK_ACTIONS = ["NO ACTION", "RESTRICT", "CASCADE", "SET NULL", "SET DEFAULT"];

// ---- Fields ---------------------------------------------------------------------

export function FieldsTab(p: { table: Table; meta: D.Meta; dialect: Dialect; selected: number; onSelect(i: number): void; update: Update }) {
  const { table: t, meta, dialect, selected, update } = p;
  const pk = t.primaryKey?.columns ?? [];
  const setCol = (i: number, patch: Partial<Column>) => update((tb) => ({ ...tb, columns: tb.columns.map((c, j) => (j === i ? { ...c, ...patch } : c)) }));
  const col = t.columns[selected];

  return (
    <div className="fields-tab">
      <div className="fields-grid">
        <table>
          <thead>
            <tr>
              <th className="narrow-col" />
              <th>Name</th>
              <th>Type</th>
              <th>Length</th>
              <th>Decimals</th>
              <th>Not Null</th>
              <th>Key</th>
              <th>Default</th>
              <th>Comment</th>
            </tr>
          </thead>
          <tbody>
            {t.columns.map((c, i) => (
              <tr key={i} className={i === selected ? "selected" : ""} onMouseDown={() => p.onSelect(i)}>
                <td className="narrow-col muted">{c.origName ? "" : "＋"}</td>
                <td>
                  <input value={c.name} onChange={(e) => update((tb) => renameColumn(tb, i, e.target.value))} />
                </td>
                <td>
                  <input list={`types-${dialect}`} value={c.type} onChange={(e) => setCol(i, { type: e.target.value })} />
                </td>
                <td>
                  <input className="num-input" disabled={!takesLength(c.type) && !c.length} value={c.length ?? ""} onChange={(e) => setCol(i, { length: e.target.value })} />
                </td>
                <td>
                  <input className="num-input" disabled={!takesScale(c.type) && !c.scale} value={c.scale ?? ""} onChange={(e) => setCol(i, { scale: e.target.value })} />
                </td>
                <td className="center">
                  <input type="checkbox" checked={!c.nullable} onChange={(e) => setCol(i, { nullable: !e.target.checked })} />
                </td>
                <td className="center key-cell" onClick={() => update((tb) => togglePK(tb, c.name))} title="Toggle primary key">
                  {pk.includes(c.name) && (
                    <span className="pk">
                      <KeyRound size={12} />
                      {pk.length > 1 && <sub>{pk.indexOf(c.name) + 1}</sub>}
                    </span>
                  )}
                </td>
                <td>
                  <DefaultInput column={c} dialect={dialect} onChange={(v) => setCol(i, { default: v })} />
                </td>
                <td>
                  <input value={c.comment ?? ""} onChange={(e) => setCol(i, { comment: e.target.value })} />
                </td>
              </tr>
            ))}
          </tbody>
        </table>
        <datalist id={`types-${dialect}`}>
          {arr(meta.types).map((ty) => (
            <option key={ty} value={ty} />
          ))}
        </datalist>
      </div>
      {col && <ColumnProperties key={selected} column={col} meta={meta} dialect={dialect} table={t} onChange={(patch) => setCol(selected, patch)} />}
    </div>
  );
}

function togglePK(t: Table, name: string): Table {
  const cols = t.primaryKey?.columns ?? [];
  const next = cols.includes(name) ? cols.filter((c) => c !== name) : [...cols, name];
  const columns = next.includes(name) ? t.columns.map((c) => (c.name === name ? { ...c, nullable: false } : c)) : t.columns;
  return { ...t, columns, primaryKey: next.length ? { name: t.primaryKey?.name ?? "", columns: next } : undefined };
}

/** Default value editor: distinguishes "no default", NULL, empty string and expressions. */
function DefaultInput({ column, dialect, onChange }: { column: Column; dialect: Dialect; onChange(v: string | undefined): void }) {
  const v = column.default;
  const disabled = !!column.identity || !!column.generatedExpr || !!column.autoIncrement;
  const suggestions = useMemo(() => {
    const base = ["NULL", "''"];
    if (/time|date/i.test(column.type)) base.push("CURRENT_TIMESTAMP", dialect === "postgres" ? "now()" : "(CURDATE())");
    if (/uuid/i.test(column.type)) base.push("gen_random_uuid()");
    if (/bool/i.test(column.type)) base.push("true", "false");
    if (/int|dec|num|float|double|real/i.test(column.type)) base.push("0");
    return base;
  }, [column.type, dialect]);
  return (
    <>
      <input
        list={`defaults-${column.type}`}
        disabled={disabled}
        placeholder={disabled ? "(auto)" : "(none)"}
        value={v ?? ""}
        onChange={(e) => onChange(e.target.value === "" ? undefined : e.target.value)}
        title="SQL expression: quote string literals, e.g. 'abc'"
      />
      <datalist id={`defaults-${column.type}`}>
        {suggestions.map((s) => (
          <option key={s} value={s} />
        ))}
      </datalist>
    </>
  );
}

function ColumnProperties({ column: c, meta, dialect, table, onChange }: { column: Column; meta: D.Meta; dialect: Dialect; table: Table; onChange(p: Partial<Column>): void }) {
  const mysql = dialect === "mysql";
  const numeric = /int|dec|num|float|double|real/i.test(c.type);
  const text = isTextType(c.type);
  const collations = arr(meta.collations).filter((co) => !mysql || !c.charset || co.charset === c.charset);
  const generated = !!c.generatedExpr || c.generatedExpr === " ";
  return (
    <div className="column-props">
      <div className="props-title">Properties of “{c.name}”</div>
      <div className="props-grid">
        {mysql && numeric && (
          <>
            <label className="check">
              <input type="checkbox" checked={!!c.autoIncrement} onChange={(e) => onChange({ autoIncrement: e.target.checked, default: e.target.checked ? undefined : c.default })} /> Auto Increment
            </label>
            <label className="check">
              <input type="checkbox" checked={!!c.unsigned} onChange={(e) => onChange({ unsigned: e.target.checked })} /> Unsigned
            </label>
            <label className="check">
              <input type="checkbox" checked={!!c.zerofill} onChange={(e) => onChange({ zerofill: e.target.checked })} /> Zerofill
            </label>
          </>
        )}
        {mysql && /timestamp|datetime/i.test(c.type) && (
          <label className="check">
            <input type="checkbox" checked={!!c.onUpdateNow} onChange={(e) => onChange({ onUpdateNow: e.target.checked })} /> On Update CURRENT_TIMESTAMP
          </label>
        )}
        {mysql && text && (
          <div className="prop">
            <span>Character Set</span>
            <select value={c.charset ?? ""} onChange={(e) => onChange({ charset: e.target.value || undefined, collation: undefined })}>
              <option value="">(table default: {table.options?.charset || "server"})</option>
              {arr(meta.charsets).map((cs) => (
                <option key={cs}>{cs}</option>
              ))}
            </select>
          </div>
        )}
        {text && (
          <div className="prop">
            <span>Collation</span>
            <select value={c.collation ?? ""} onChange={(e) => onChange({ collation: e.target.value || undefined })}>
              <option value="">(default)</option>
              {collations.map((co) => (
                <option key={co.name}>{co.name}</option>
              ))}
            </select>
          </div>
        )}
        {!mysql && (
          <div className="prop">
            <span>Identity</span>
            <select value={c.identity ?? ""} onChange={(e) => onChange({ identity: e.target.value || undefined, default: e.target.value ? undefined : c.default })}>
              <option value="">(none)</option>
              <option value="by default">GENERATED BY DEFAULT</option>
              <option value="always">GENERATED ALWAYS</option>
            </select>
          </div>
        )}
        {!mysql && c.identity && (
          <div className="identity-opts">
            {(["start", "increment", "min", "max", "cache"] as const).map((k) => (
              <div className="prop" key={k}>
                <span>{k === "min" ? "Minimum" : k === "max" ? "Maximum" : k[0].toUpperCase() + k.slice(1)}</span>
                <input className="num-input" value={c.identityOpts?.[k] ?? ""} onChange={(e) => onChange({ identityOpts: { ...(c.identityOpts ?? {}), [k]: e.target.value } as D.IdentityOpts })} />
              </div>
            ))}
            <label className="check">
              <input type="checkbox" checked={!!c.identityOpts?.cycle} onChange={(e) => onChange({ identityOpts: { ...(c.identityOpts ?? {}), cycle: e.target.checked } as D.IdentityOpts })} /> Cycle
            </label>
          </div>
        )}
        <label className="check">
          <input type="checkbox" checked={generated} disabled={!!c.identity} onChange={(e) => onChange({ generatedExpr: e.target.checked ? " " : undefined, generatedStored: e.target.checked ? !mysql || c.generatedStored : undefined })} /> Generated
          column
        </label>
        {generated && (
          <>
            <div className="prop wide">
              <span>Expression</span>
              <input className="mono" value={c.generatedExpr?.trim() ?? ""} placeholder="price * qty" onChange={(e) => onChange({ generatedExpr: e.target.value || " " })} />
            </div>
            <div className="prop">
              <span>Storage</span>
              <select value={c.generatedStored ? "stored" : "virtual"} onChange={(e) => onChange({ generatedStored: e.target.value === "stored" })}>
                <option value="virtual">Virtual</option>
                <option value="stored">Stored</option>
              </select>
            </div>
          </>
        )}
      </div>
    </div>
  );
}

// ---- field picker used by indexes, uniques and foreign keys ---------------------------

function FieldPicker({ columns, value, onChange, withOrder, withLength }: { columns: string[]; value: D.IndexColumn[]; onChange(v: D.IndexColumn[]): void; withOrder?: boolean; withLength?: boolean }) {
  const available = columns.filter((c) => !value.some((v) => v.name === c && !v.expr));
  return (
    <div className="field-picker">
      {value.map((v, i) => (
        <span key={i} className="chip">
          {v.expr ? <span className="mono">({v.expr})</span> : v.name}
          {withLength && !v.expr && (
            <input className="chip-len" placeholder="len" value={v.length ?? ""} onChange={(e) => onChange(value.map((x, j) => (j === i ? { ...x, length: e.target.value } : x)))} />
          )}
          {withOrder && (
            <button className="icon" title={v.desc ? "Descending" : "Ascending"} onClick={() => onChange(value.map((x, j) => (j === i ? { ...x, desc: !x.desc } : x)))}>
              {v.desc ? <ArrowDownWideNarrow size={11} /> : <ArrowUpNarrowWide size={11} />}
            </button>
          )}
          <button className="icon" onClick={() => onChange(value.filter((_, j) => j !== i))}>
            <X size={11} />
          </button>
        </span>
      ))}
      {available.length > 0 && (
        <select value="" onChange={(e) => e.target.value && onChange([...value, { name: e.target.value }])}>
          <option value="">+ field</option>
          {available.map((c) => (
            <option key={c}>{c}</option>
          ))}
        </select>
      )}
    </div>
  );
}

const names = (cols: string[]): D.IndexColumn[] => cols.map((name) => ({ name }));
const plain = (cols: D.IndexColumn[]) => cols.map((c) => c.name);

function ListToolbar({ onAdd, label }: { onAdd(): void; label: string }) {
  return (
    <div className="list-toolbar">
      <button onClick={onAdd}>
        <Plus size={13} /> Add {label}
      </button>
    </div>
  );
}

function Del({ onClick }: { onClick(): void }) {
  return (
    <button className="icon" title="Delete" onClick={onClick}>
      <Trash2 size={13} />
    </button>
  );
}

// ---- Indexes -------------------------------------------------------------------------

export function IndexesTab({ table: t, meta, dialect, update }: { table: Table; meta: D.Meta; dialect: Dialect; update: Update }) {
  const cols = t.columns.map((c) => c.name);
  const mysql = dialect === "mysql";
  const set = (i: number, patch: Partial<Index>) => update((tb) => ({ ...tb, indexes: tb.indexes.map((x, j) => (j === i ? { ...x, ...patch } : x)) }));
  return (
    <div className="list-tab">
      <ListToolbar label="Index" onAdd={() => update((tb) => ({ ...tb, indexes: [...tb.indexes, { name: uniqueName(`${tb.name || "idx"}_idx`, tb.indexes.map((x) => x.name)), columns: [] }] }))} />
      <table>
        <thead>
          <tr>
            <th>Name</th>
            <th>Fields</th>
            <th>Type</th>
            <th>Method</th>
            {mysql ? <th>Comment</th> : <th>Where (partial)</th>}
            {mysql && <th>Invisible</th>}
            <th />
          </tr>
        </thead>
        <tbody>
          {t.indexes.map((ix, i) => (
            <tr key={i}>
              <td>
                <input value={ix.name} onChange={(e) => set(i, { name: e.target.value })} />
              </td>
              <td>
                <FieldPicker columns={cols} value={ix.columns} withOrder withLength={mysql} onChange={(v) => set(i, { columns: v })} />
              </td>
              <td>
                <select value={ix.kind ?? ""} onChange={(e) => set(i, { kind: e.target.value || undefined })}>
                  <option value="">Normal</option>
                  <option value="unique">Unique</option>
                  {mysql && <option value="fulltext">Fulltext</option>}
                  {mysql && <option value="spatial">Spatial</option>}
                </select>
              </td>
              <td>
                <select value={ix.method ?? ""} onChange={(e) => set(i, { method: e.target.value || undefined })} disabled={ix.kind === "fulltext" || ix.kind === "spatial"}>
                  <option value="">(default)</option>
                  {arr(meta.indexMethods).map((m) => (
                    <option key={m}>{m}</option>
                  ))}
                </select>
              </td>
              <td>
                {mysql ? (
                  <input value={ix.comment ?? ""} onChange={(e) => set(i, { comment: e.target.value })} />
                ) : (
                  <input className="mono" placeholder="e.g. deleted_at IS NULL" value={ix.where ?? ""} onChange={(e) => set(i, { where: e.target.value })} />
                )}
              </td>
              {mysql && (
                <td className="center">
                  <input type="checkbox" checked={!!ix.invisible} onChange={(e) => set(i, { invisible: e.target.checked })} />
                </td>
              )}
              <td>
                <Del onClick={() => update((tb) => ({ ...tb, indexes: tb.indexes.filter((_, j) => j !== i) }))} />
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      {!t.indexes.length && <div className="center-hint">No indexes.</div>}
    </div>
  );
}

// ---- Uniques (PostgreSQL constraints) ---------------------------------------------------

export function UniquesTab({ table: t, update }: { table: Table; update: Update }) {
  const cols = t.columns.map((c) => c.name);
  const set = (i: number, patch: Partial<Unique>) => update((tb) => ({ ...tb, uniques: tb.uniques.map((x, j) => (j === i ? { ...x, ...patch } : x)) }));
  return (
    <div className="list-tab">
      <ListToolbar label="Unique" onAdd={() => update((tb) => ({ ...tb, uniques: [...tb.uniques, { name: uniqueName(`${tb.name || "t"}_key`, tb.uniques.map((x) => x.name)), columns: [] }] }))} />
      <table>
        <thead>
          <tr>
            <th>Name</th>
            <th>Fields</th>
            <th />
          </tr>
        </thead>
        <tbody>
          {t.uniques.map((u, i) => (
            <tr key={i}>
              <td>
                <input value={u.name} onChange={(e) => set(i, { name: e.target.value })} />
              </td>
              <td>
                <FieldPicker columns={cols} value={names(u.columns)} onChange={(v) => set(i, { columns: plain(v) })} />
              </td>
              <td>
                <Del onClick={() => update((tb) => ({ ...tb, uniques: tb.uniques.filter((_, j) => j !== i) }))} />
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      {!t.uniques.length && <div className="center-hint">No unique constraints. (Unique indexes are on the Indexes tab.)</div>}
    </div>
  );
}

// ---- Foreign keys ---------------------------------------------------------------------

export function ForeignKeysTab({ table: t, connId, dialect, update }: { table: Table; connId: string; dialect: Dialect; update: Update }) {
  const cols = t.columns.map((c) => c.name);
  const pg = dialect === "postgres";
  const [tables, setTables] = useState<string[]>([]);
  const [refCols, setRefCols] = useState<Record<string, string[]>>({});
  useEffect(() => {
    Explorer.ListObjects(connId, t.database, t.schema ?? "", ObjectKind.KindTable).then((o) => setTables(arr(o).map((x) => x.name)));
  }, [connId, t.database, t.schema]);

  const loadRefCols = async (fk: ForeignKey) => {
    const key = `${fk.refSchema ?? ""}.${fk.refTable}`;
    if (!fk.refTable || refCols[key]) return;
    if (fk.refTable === t.name) {
      setRefCols((r) => ({ ...r, [key]: cols }));
      return;
    }
    const d = await Explorer.TableDetail(connId, { database: fk.refDatabase || t.database, schema: fk.refSchema || t.schema, name: fk.refTable, kind: ObjectKind.KindTable }).catch(() => null);
    if (d) setRefCols((r) => ({ ...r, [key]: arr(d.columns).map((c) => c.name) }));
  };
  useEffect(() => {
    t.foreignKeys.forEach(loadRefCols);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [t.foreignKeys.map((f) => f.refTable).join(",")]);

  const set = (i: number, patch: Partial<ForeignKey>) => update((tb) => ({ ...tb, foreignKeys: tb.foreignKeys.map((x, j) => (j === i ? { ...x, ...patch } : x)) }));
  return (
    <div className="list-tab">
      <ListToolbar label="Foreign Key" onAdd={() => update((tb) => ({ ...tb, foreignKeys: [...tb.foreignKeys, { name: uniqueName(`${tb.name || "t"}_fk`, tb.foreignKeys.map((x) => x.name)), columns: [], refTable: "", refColumns: [] }] }))} />
      <table>
        <thead>
          <tr>
            <th>Name</th>
            <th>Fields</th>
            <th>{pg ? "Referenced Schema" : "Referenced Database"}</th>
            <th>Referenced Table</th>
            <th>Referenced Fields</th>
            <th>On Delete</th>
            <th>On Update</th>
            <th />
          </tr>
        </thead>
        <tbody>
          {t.foreignKeys.map((fk, i) => {
            const rc = refCols[`${fk.refSchema ?? ""}.${fk.refTable}`] ?? [];
            return (
              <tr key={i}>
                <td>
                  <input value={fk.name} onChange={(e) => set(i, { name: e.target.value })} />
                </td>
                <td>
                  <FieldPicker columns={cols} value={names(fk.columns)} onChange={(v) => set(i, { columns: plain(v) })} />
                </td>
                <td>
                  <input
                    placeholder={pg ? t.schema : t.database}
                    value={(pg ? fk.refSchema : fk.refDatabase) ?? ""}
                    onChange={(e) => set(i, pg ? { refSchema: e.target.value || undefined } : { refDatabase: e.target.value || undefined })}
                  />
                </td>
                <td>
                  <input list="fk-tables" value={fk.refTable} onChange={(e) => set(i, { refTable: e.target.value, refColumns: [] })} onBlur={() => loadRefCols(fk)} />
                </td>
                <td>
                  <FieldPicker columns={rc} value={names(fk.refColumns)} onChange={(v) => set(i, { refColumns: plain(v) })} />
                </td>
                <td>
                  <select value={fk.onDelete || "NO ACTION"} onChange={(e) => set(i, { onDelete: e.target.value })}>
                    {FK_ACTIONS.map((a) => (
                      <option key={a}>{a}</option>
                    ))}
                  </select>
                </td>
                <td>
                  <select value={fk.onUpdate || "NO ACTION"} onChange={(e) => set(i, { onUpdate: e.target.value })}>
                    {FK_ACTIONS.map((a) => (
                      <option key={a}>{a}</option>
                    ))}
                  </select>
                </td>
                <td>
                  <Del onClick={() => update((tb) => ({ ...tb, foreignKeys: tb.foreignKeys.filter((_, j) => j !== i) }))} />
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
      <datalist id="fk-tables">
        {tables.map((x) => (
          <option key={x} value={x} />
        ))}
      </datalist>
      {!t.foreignKeys.length && <div className="center-hint">No foreign keys.</div>}
    </div>
  );
}

// ---- Checks ----------------------------------------------------------------------------

export function ChecksTab({ table: t, dialect, update }: { table: Table; dialect: Dialect; update: Update }) {
  const set = (i: number, patch: Partial<D.Check>) => update((tb) => ({ ...tb, checks: tb.checks.map((x, j) => (j === i ? { ...x, ...patch } : x)) }));
  return (
    <div className="list-tab">
      <ListToolbar label="Check" onAdd={() => update((tb) => ({ ...tb, checks: [...tb.checks, { name: uniqueName(`${tb.name || "t"}_check`, tb.checks.map((x) => x.name)), expr: "" }] }))} />
      <table>
        <thead>
          <tr>
            <th>Name</th>
            <th>Expression</th>
            {dialect === "mysql" && <th>Not Enforced</th>}
            <th />
          </tr>
        </thead>
        <tbody>
          {t.checks.map((c, i) => (
            <tr key={i}>
              <td>
                <input value={c.name} onChange={(e) => set(i, { name: e.target.value })} />
              </td>
              <td className="wide">
                <input className="mono" placeholder="price >= 0" value={c.expr} onChange={(e) => set(i, { expr: e.target.value })} />
              </td>
              {dialect === "mysql" && (
                <td className="center">
                  <input type="checkbox" checked={!!c.notEnforced} onChange={(e) => set(i, { notEnforced: e.target.checked })} />
                </td>
              )}
              <td>
                <Del onClick={() => update((tb) => ({ ...tb, checks: tb.checks.filter((_, j) => j !== i) }))} />
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      {!t.checks.length && <div className="center-hint">No check constraints.</div>}
    </div>
  );
}

// ---- Triggers --------------------------------------------------------------------------

export function TriggersTab({ table: t, meta, dialect, update }: { table: Table; meta: D.Meta; dialect: Dialect; update: Update }) {
  const [sel, setSel] = useState(0);
  const mysql = dialect === "mysql";
  const tr = t.triggers[sel];
  const set = (patch: Partial<Trigger>) => update((tb) => ({ ...tb, triggers: tb.triggers.map((x, j) => (j === sel ? { ...x, ...patch } : x)) }));
  const add = () => {
    update((tb) => ({
      ...tb,
      triggers: [
        ...tb.triggers,
        { name: uniqueName(`${tb.name || "t"}_trigger`, tb.triggers.map((x) => x.name)), timing: "BEFORE", events: ["INSERT"], forEachRow: true, body: mysql ? "BEGIN\n  \nEND" : "", function: "" },
      ],
    }));
    setSel(t.triggers.length);
  };
  const toggleEvent = (ev: string) => {
    if (!tr) return;
    if (mysql) return set({ events: [ev] }); // MySQL triggers have exactly one event
    set({ events: tr.events.includes(ev) ? tr.events.filter((e) => e !== ev) : [...tr.events, ev] });
  };
  return (
    <div className="triggers-tab">
      <div className="trigger-list">
        <ListToolbar label="Trigger" onAdd={add} />
        {t.triggers.map((x, i) => (
          <div key={i} className={"trigger-item" + (i === sel ? " selected" : "")} onClick={() => setSel(i)}>
            <span>{x.name}</span>
            <span className="muted">
              {x.timing} {x.events.join("/")}
            </span>
          </div>
        ))}
        {!t.triggers.length && <div className="center-hint">No triggers.</div>}
      </div>
      {tr && (
        <div className="trigger-edit form">
          <div className="row">
            <label>Name</label>
            <input value={tr.name} onChange={(e) => set({ name: e.target.value })} />
            <button
              className="icon"
              title="Delete trigger"
              onClick={() => {
                update((tb) => ({ ...tb, triggers: tb.triggers.filter((_, j) => j !== sel) }));
                setSel(0);
              }}
            >
              <Trash2 size={13} />
            </button>
          </div>
          <div className="row">
            <label>Fires</label>
            <select value={tr.timing} onChange={(e) => set({ timing: e.target.value })}>
              <option>BEFORE</option>
              <option>AFTER</option>
              {!mysql && <option>INSTEAD OF</option>}
            </select>
            {["INSERT", "UPDATE", "DELETE", ...(mysql ? [] : ["TRUNCATE"])].map((ev) => (
              <label key={ev} className="check">
                <input type={mysql ? "radio" : "checkbox"} checked={tr.events.includes(ev)} onChange={() => toggleEvent(ev)} /> {ev}
              </label>
            ))}
          </div>
          {!mysql && (
            <>
              <div className="row">
                <label>For Each</label>
                <select value={tr.forEachRow ? "row" : "statement"} onChange={(e) => set({ forEachRow: e.target.value === "row" })}>
                  <option value="row">Row</option>
                  <option value="statement">Statement</option>
                </select>
              </div>
              <div className="row">
                <label>Function</label>
                <input list="trigger-fns" className="mono" placeholder="schema.function()" value={tr.function ?? ""} onChange={(e) => set({ function: e.target.value })} />
                <datalist id="trigger-fns">
                  {arr(meta.triggerFunctions).map((f) => (
                    <option key={f} value={f + "()"} />
                  ))}
                </datalist>
              </div>
              <div className="row">
                <label>When</label>
                <input className="mono" placeholder="(optional) NEW.status IS DISTINCT FROM OLD.status" value={tr.when ?? ""} onChange={(e) => set({ when: e.target.value })} />
              </div>
            </>
          )}
          {mysql && (
            <>
              <label>Definition</label>
              <textarea className="mono trigger-body" value={tr.body ?? ""} onChange={(e) => set({ body: e.target.value })} spellCheck={false} />
            </>
          )}
        </div>
      )}
    </div>
  );
}

// ---- Options ---------------------------------------------------------------------------

export function OptionsTab({ table: t, meta, dialect, update }: { table: Table; meta: D.Meta; dialect: Dialect; update: Update }) {
  const o = t.options ?? {};
  const set = (patch: Partial<D.Options>) => update((tb) => ({ ...tb, options: { ...(tb.options ?? {}), ...patch } }));
  const collations = arr(meta.collations).filter((c) => !o.charset || c.charset === o.charset);
  return (
    <div className="options-tab form">
      <div className="field">
        <label>Table Name</label>
        <div className="field-input">
          <input value={t.name} onChange={(e) => update((tb) => ({ ...tb, name: e.target.value }))} />
        </div>
      </div>
      {dialect === "mysql" ? (
        <>
          <Opt label="Engine">
            <select value={o.engine ?? ""} onChange={(e) => set({ engine: e.target.value })}>
              {arr(meta.engines).map((x) => (
                <option key={x}>{x}</option>
              ))}
            </select>
          </Opt>
          <Opt label="Default Character Set">
            <select value={o.charset ?? ""} onChange={(e) => set({ charset: e.target.value, collation: arr(meta.collations).find((c) => c.charset === e.target.value && c.default)?.name })}>
              {arr(meta.charsets).map((x) => (
                <option key={x}>{x}</option>
              ))}
            </select>
          </Opt>
          <Opt label="Default Collation">
            <select value={o.collation ?? ""} onChange={(e) => set({ collation: e.target.value })}>
              {collations.map((x) => (
                <option key={x.name}>{x.name}</option>
              ))}
            </select>
          </Opt>
          <Opt label="Auto Increment">
            <input className="num-input" value={o.autoIncrement ?? ""} onChange={(e) => set({ autoIncrement: e.target.value })} />
          </Opt>
          <Opt label="Row Format">
            <select value={o.rowFormat ?? ""} onChange={(e) => set({ rowFormat: e.target.value || undefined })}>
              {["", "DYNAMIC", "COMPACT", "REDUNDANT", "COMPRESSED"].map((x) => (
                <option key={x} value={x}>
                  {x || "(default)"}
                </option>
              ))}
            </select>
          </Opt>
        </>
      ) : (
        <>
          <Opt label="">
            <label className="check">
              <input type="checkbox" checked={!!o.unlogged} onChange={(e) => set({ unlogged: e.target.checked })} /> Unlogged
            </label>
          </Opt>
          <Opt label="Tablespace">
            <input value={o.tablespace ?? ""} placeholder="(default)" onChange={(e) => set({ tablespace: e.target.value || undefined })} />
          </Opt>
        </>
      )}
    </div>
  );
}

function Opt({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="field">
      <label>{label}</label>
      <div className="field-input">{children}</div>
    </div>
  );
}
