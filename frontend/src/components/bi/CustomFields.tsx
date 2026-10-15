// Custom fields of a data source (Navicat's Type-Changed, Concatenated, Mapped,
// Custom-Sorted and Calculated fields). Chloe works them out in each chart's
// query, so they filter, group and aggregate like the source's own fields.
import { Plus, X } from "lucide-react";
import type { B } from "../../api";
import { arr } from "../../api";

const KINDS: [string, string][] = [
  ["calculated", "Calculated"],
  ["mapped", "Mapped"],
  ["concatenated", "Concatenated"],
  ["type_changed", "Type-Changed"],
  ["sorted", "Custom-Sorted"],
];

const lines = (s: string) =>
  s
    .split("\n")
    .map((l) => l.trim())
    .filter(Boolean);

export function CustomFieldsPane({ fields, custom, onChange }: { fields: B.Field[]; custom: B.CustomField[]; onChange(c: B.CustomField[]): void }) {
  const set = (i: number, patch: Partial<B.CustomField>) => onChange(custom.map((c, j) => (j === i ? { ...c, ...patch } : c)));
  return (
    <div className="bi-custom">
      {custom.map((cf, i) => {
        // A custom field may use the source's fields and the custom fields before it.
        const usable = [...fields.map((f) => f.name), ...custom.slice(0, i).map((c) => c.name)];
        const fieldSelect = (label: string, value: string | undefined, onSel: (v: string) => void) => (
          <select aria-label={label} value={value ?? ""} onChange={(e) => onSel(e.target.value)}>
            <option value="">(field)</option>
            {usable.map((n) => (
              <option key={n}>{n}</option>
            ))}
          </select>
        );
        return (
          <div key={i} className="bi-custom-field">
            <div className="bi-row">
              <input aria-label="Custom field name" className="bi-filter-value" placeholder="Name" value={cf.name} onChange={(e) => set(i, { name: e.target.value })} />
              <select aria-label="Custom field kind" value={cf.kind} onChange={(e) => set(i, { kind: e.target.value })}>
                {KINDS.map(([k, l]) => (
                  <option key={k} value={k}>
                    {l}
                  </option>
                ))}
              </select>
              {cf.type && <span className="muted">{cf.type === "number" ? "Number" : cf.type === "datetime" ? "Date/Time" : "Text"}</span>}
              <span className="spacer" />
              <button className="icon-btn" title="Remove" onClick={() => onChange(custom.filter((_, j) => j !== i))}>
                <X size={13} />
              </button>
            </div>
            {cf.kind === "calculated" && (
              <>
                <input
                  aria-label="Expression"
                  className="bi-expr"
                  placeholder="e.g. ROUND([unit price] * qty, 2)"
                  value={cf.expression ?? ""}
                  onChange={(e) => set(i, { expression: e.target.value })}
                  list={`bi-expr-fields-${i}`}
                />
                <datalist id={`bi-expr-fields-${i}`}>
                  {usable.map((n) => (
                    <option key={n} value={`[${n}]`} />
                  ))}
                </datalist>
                <div className="hint">
                  Fields by name or in [brackets]; + − × ÷ % and &amp; (joins text); = &lt;&gt; &lt; &gt; AND OR NOT; ROUND ABS FLOOR CEIL COALESCE NULLIF IF UPPER LOWER TRIM LENGTH YEAR MONTH DAY; [$parameter].
                  Dividing by zero gives an empty value.
                </div>
              </>
            )}
            {cf.kind === "type_changed" && (
              <div className="bi-row">
                {fieldSelect("Field to change", cf.field, (v) => set(i, { field: v }))}
                <span className="muted">to</span>
                <select aria-label="New type" value={cf.to ?? ""} onChange={(e) => set(i, { to: e.target.value })}>
                  <option value="">(type)</option>
                  <option value="string">Text</option>
                  <option value="number">Number</option>
                  <option value="datetime">Date/Time</option>
                </select>
              </div>
            )}
            {cf.kind === "concatenated" && (
              <div className="bi-row">
                <input
                  aria-label="Fields to join"
                  className="bi-filter-value"
                  placeholder="first, last"
                  value={arr(cf.fields).join(", ")}
                  onChange={(e) => set(i, { fields: e.target.value.split(",").map((x) => x.trim()).filter(Boolean) })}
                  title={`Fields: ${usable.join(", ")}`}
                />
                <span className="muted">joined with</span>
                <input aria-label="Separator" className="bi-filter-value short" placeholder="(nothing)" value={cf.separator ?? ""} onChange={(e) => set(i, { separator: e.target.value })} />
              </div>
            )}
            {cf.kind === "mapped" && (
              <>
                <div className="bi-row">
                  {fieldSelect("Field to map", cf.field, (v) => set(i, { field: v }))}
                  <label className="check">
                    <input type="checkbox" checked={!!cf.caseSensitive} onChange={(e) => set(i, { caseSensitive: e.target.checked })} /> Match case
                  </label>
                </div>
                <textarea
                  aria-label="Mapping rules"
                  rows={3}
                  placeholder={"one rule per line:\nfrom = to"}
                  defaultValue={arr(cf.rules)
                    .map((r) => `${r.from} = ${r.to}`)
                    .join("\n")}
                  onBlur={(e) =>
                    set(i, {
                      rules: lines(e.target.value).map((l) => {
                        const [from, ...to] = l.split("=");
                        return { from: from.trim(), to: to.join("=").trim() };
                      }),
                    })
                  }
                />
                <div className="bi-row">
                  <span className="muted">Other values</span>
                  <select aria-label="Unmapped values" value={cf.unmapped || "keep"} onChange={(e) => set(i, { unmapped: e.target.value })}>
                    <option value="keep">stay as they are</option>
                    <option value="null">become empty</option>
                    <option value="other">become</option>
                  </select>
                  {cf.unmapped === "other" && <input aria-label="Other value" className="bi-filter-value short" value={cf.other ?? ""} onChange={(e) => set(i, { other: e.target.value })} />}
                </div>
              </>
            )}
            {cf.kind === "sorted" && (
              <>
                <div className="bi-row">{fieldSelect("Field to sort", cf.field, (v) => set(i, { field: v }))}</div>
                <textarea
                  aria-label="Sort order"
                  rows={3}
                  placeholder={"values in order, one per line\n(others follow alphabetically)"}
                  defaultValue={arr(cf.order).join("\n")}
                  onBlur={(e) => set(i, { order: lines(e.target.value) })}
                />
              </>
            )}
          </div>
        );
      })}
      <button className="small-btn" disabled={!fields.length} onClick={() => onChange([...custom, { name: `Field ${custom.length + 1}`, kind: "calculated", expression: "" } as B.CustomField])}>
        <Plus size={12} /> Add Custom Field
      </button>
      {!fields.length && <div className="muted">Apply and Refresh Data first: custom fields are made from the source's fields.</div>}
    </div>
  );
}
