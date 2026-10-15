// Filter conditions on a data source or chart: field, operator, value(s), with
// each condition switchable off or negated. Conditions are joined with AND.
import { Plus, X } from "lucide-react";
import type { B } from "../../api";

export const OPS: [string, string][] = [
  ["=", "="],
  ["!=", "!="],
  ["<", "<"],
  ["<=", "<="],
  [">", ">"],
  [">=", ">="],
  ["contains", "contains"],
  ["not_contains", "does not contain"],
  ["begins_with", "begins with"],
  ["not_begins_with", "does not begin with"],
  ["ends_with", "ends with"],
  ["not_ends_with", "does not end with"],
  ["is_null", "is null"],
  ["is_not_null", "is not null"],
  ["is_empty", "is empty"],
  ["is_not_empty", "is not empty"],
  ["between", "is between"],
  ["not_between", "is not between"],
  ["in", "is in list"],
  ["not_in", "is not in list"],
  ["in_last", "is in the last"],
  ["in_period", "is in period"],
  ["to_date", "is period to date"],
];

const UNITS = ["day", "week", "month", "quarter", "year"];
const WHICH = ["this", "last", "next"];
const TO_DATE = ["week", "month", "quarter", "year"];
/** Operators whose values are picked from lists rather than typed. */
const DYNAMIC = new Set(["in_last", "in_period", "to_date"]);
const DEFAULTS: Record<string, string[]> = { in_last: ["30", "day"], in_period: ["this", "month"], to_date: ["year"] };

const arity = (op: string) => (["is_null", "is_not_null", "is_empty", "is_not_empty"].includes(op) ? 0 : op.endsWith("between") || op === "in_last" || op === "in_period" ? 2 : op === "in" || op === "not_in" ? -1 : 1);

export function FilterList({ fields, filters, onChange }: { fields: B.Field[]; filters: B.Filter[]; onChange(f: B.Filter[]): void }) {
  const set = (i: number, patch: Partial<B.Filter>) => onChange(filters.map((f, j) => (j === i ? { ...f, ...patch } : f)));
  return (
    <div className="bi-filters">
      {filters.map((f, i) => {
        const n = arity(f.op);
        const vals = f.values ?? [];
        return (
          <div key={i} className={"bi-filter" + (f.disabled ? " off" : "")}>
            <input type="checkbox" title="Use this condition" checked={!f.disabled} onChange={(e) => set(i, { disabled: !e.target.checked })} />
            {i > 0 && <span className="muted">and</span>}
            <select aria-label="Filter field" value={f.field} onChange={(e) => set(i, { field: e.target.value })}>
              {!fields.some((x) => x.name === f.field) && <option value={f.field}>{f.field || "(field)"}</option>}
              {fields.map((x) => (
                <option key={x.name} value={x.name}>
                  {x.name}
                </option>
              ))}
            </select>
            <button className={"small-btn" + (f.negate ? " on" : "")} title="Toggle Negator" onClick={() => set(i, { negate: !f.negate })}>
              not
            </button>
            <select
              aria-label="Filter operator"
              value={f.op}
              onChange={(e) => {
                const op = e.target.value;
                const k = arity(op);
                set(i, { op, values: DEFAULTS[op] ?? (k === 0 ? [] : k === 2 ? [vals[0] ?? "", vals[1] ?? ""] : k === 1 ? [vals[0] ?? ""] : vals.length ? vals : [""]) });
              }}
            >
              {OPS.map(([k, label]) => (
                <option key={k} value={k}>
                  {label}
                </option>
              ))}
            </select>
            {n === 1 && !DYNAMIC.has(f.op) && <input aria-label="Filter value" className="bi-filter-value" value={vals[0] ?? ""} onChange={(e) => set(i, { values: [e.target.value] })} />}
            {f.op === "in_period" && (
              <>
                <select aria-label="Filter period" value={vals[0] ?? "this"} onChange={(e) => set(i, { values: [e.target.value, vals[1] ?? "month"] })}>
                  {WHICH.map((w) => (
                    <option key={w} value={w}>
                      {w}
                    </option>
                  ))}
                </select>
                <select aria-label="Filter unit" value={vals[1] ?? "month"} onChange={(e) => set(i, { values: [vals[0] ?? "this", e.target.value] })}>
                  {UNITS.map((u) => (
                    <option key={u} value={u}>
                      {u}
                    </option>
                  ))}
                </select>
              </>
            )}
            {f.op === "to_date" && (
              <select aria-label="Filter unit" value={vals[0] ?? "year"} onChange={(e) => set(i, { values: [e.target.value] })}>
                {TO_DATE.map((u) => (
                  <option key={u} value={u}>
                    {u} to date
                  </option>
                ))}
              </select>
            )}
            {f.op === "in_last" && (
              <>
                <input aria-label="Filter count" type="number" min={1} className="bi-filter-value short" value={vals[0] ?? ""} onChange={(e) => set(i, { values: [e.target.value, vals[1] ?? "day"] })} />
                <select aria-label="Filter unit" value={vals[1] ?? "day"} onChange={(e) => set(i, { values: [vals[0] ?? "1", e.target.value] })}>
                  {UNITS.map((u) => (
                    <option key={u} value={u}>
                      {u}s
                    </option>
                  ))}
                </select>
              </>
            )}
            {n === 2 && !DYNAMIC.has(f.op) && (
              <>
                <input aria-label="Filter value" className="bi-filter-value short" value={vals[0] ?? ""} onChange={(e) => set(i, { values: [e.target.value, vals[1] ?? ""] })} />
                <span className="muted">and</span>
                <input aria-label="Filter second value" className="bi-filter-value short" value={vals[1] ?? ""} onChange={(e) => set(i, { values: [vals[0] ?? "", e.target.value] })} />
              </>
            )}
            {n === -1 && (
              <input
                aria-label="Filter values"
                className="bi-filter-value"
                placeholder="a, b, c"
                value={vals.join(", ")}
                onChange={(e) => set(i, { values: e.target.value.split(",").map((v) => v.trim()) })}
              />
            )}
            <button className="icon-btn" title="Remove condition" onClick={() => onChange(filters.filter((_, j) => j !== i))}>
              <X size={13} />
            </button>
          </div>
        );
      })}
      <button className="small-btn" disabled={!fields.length} onClick={() => onChange([...filters, { field: fields[0]?.name ?? "", op: "=", values: [""] }])}>
        <Plus size={12} /> Add Condition
      </button>
    </div>
  );
}

/** Conditions complete enough to send: a list's empty entries dropped. */
export function cleanFilters(fs: B.Filter[]): B.Filter[] {
  return fs.map((f) => (f.op === "in" || f.op === "not_in" ? { ...f, values: (f.values ?? []).filter((v) => v !== "") } : f)).filter((f) => !((f.op === "in" || f.op === "not_in") && !(f.values ?? []).length));
}
