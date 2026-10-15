// Joined tables of a table data source (Navicat's joined tables): more tables of
// the same database, each joined on pairs of matching fields. Joined columns
// come through as "table.column".
import { Plus, X } from "lucide-react";
import { useEffect, useState } from "react";
import { B, Explorer, ObjectKind, arr } from "../../api";

export function JoinsPane({
  src,
  tables,
  fields,
  onChange,
}: {
  src: B.DataSource;
  /** The database's tables and views, to join. */
  tables: string[];
  /** The first table's fields. */
  fields: string[];
  onChange(j: B.Join[]): void;
}) {
  const joins = arr(src.joins);
  const [cols, setCols] = useState<Record<string, string[]>>({});
  // Each joined table's columns, for its side of the match.
  useEffect(() => {
    for (const j of joins) {
      if (!j.table || cols[j.table]) continue;
      Explorer.TableDetail(src.connId, { database: src.database, schema: src.schema ?? "", name: j.table, kind: ObjectKind.KindTable } as never)
        .then((t) => setCols((c) => ({ ...c, [j.table]: arr(t.columns).map((x) => x.name) })))
        .catch(() => {});
    }
  }, [joins.map((j) => j.table).join(",")]); // eslint-disable-line react-hooks/exhaustive-deps
  const set = (i: number, patch: Partial<B.Join>) => onChange(joins.map((j, k) => (k === i ? { ...j, ...patch } : j)));
  return (
    <div className="bi-joins">
      {joins.map((j, i) => {
        // The left side may be a first-table field, or table.column of an earlier join.
        const left = [...fields, ...joins.slice(0, i).flatMap((p) => arr(cols[p.table]).map((c) => `${p.table}.${c}`))];
        return (
          <div key={i} className="bi-custom-field">
            <div className="bi-row">
              <select aria-label="Join type" value={j.type} onChange={(e) => set(i, { type: e.target.value })}>
                <option value="inner">Inner join</option>
                <option value="left">Left join</option>
                <option value="right">Right join</option>
                <option value="full">Full join (PostgreSQL)</option>
              </select>
              <select aria-label="Joined table" value={j.table} onChange={(e) => set(i, { table: e.target.value, on: [{ left: "", right: "" }] })}>
                <option value="">(table)</option>
                {tables
                  .filter((t) => t !== src.table)
                  .map((t) => (
                    <option key={t}>{t}</option>
                  ))}
              </select>
              <span className="spacer" />
              <button className="icon-btn" title="Remove join" onClick={() => onChange(joins.filter((_, k) => k !== i))}>
                <X size={13} />
              </button>
            </div>
            {arr(j.on).map((on, k) => (
              <div key={k} className="bi-row">
                <span className="muted">{k ? "and" : "on"}</span>
                <select aria-label="Join left field" value={on.left} onChange={(e) => set(i, { on: arr(j.on).map((x, n) => (n === k ? { ...x, left: e.target.value } : x)) })}>
                  <option value="">({src.table} field)</option>
                  {left.map((f) => (
                    <option key={f}>{f}</option>
                  ))}
                </select>
                <span className="muted">=</span>
                <select aria-label="Join right field" value={on.right} onChange={(e) => set(i, { on: arr(j.on).map((x, n) => (n === k ? { ...x, right: e.target.value } : x)) })}>
                  <option value="">({j.table || "joined"} column)</option>
                  {arr(cols[j.table]).map((c) => (
                    <option key={c}>{c}</option>
                  ))}
                </select>
                {k > 0 && (
                  <button className="icon-btn" title="Remove" onClick={() => set(i, { on: arr(j.on).filter((_, n) => n !== k) })}>
                    <X size={12} />
                  </button>
                )}
              </div>
            ))}
            <button className="small-btn" onClick={() => set(i, { on: [...arr(j.on), { left: "", right: "" }] })}>
              <Plus size={12} /> Match another field
            </button>
          </div>
        );
      })}
      <button className="small-btn" disabled={!src.table} onClick={() => onChange([...joins, { table: "", type: "left", on: [{ left: "", right: "" }] } as B.Join])}>
        <Plus size={12} /> Add Join
      </button>
      <div className="hint">Joined columns appear as table.column. Joins stay within this database; to join across connections, copy the data with Data Transfer first.</div>
    </div>
  );
}
