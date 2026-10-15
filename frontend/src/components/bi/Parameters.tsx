// Workspace parameters: named values that data source queries, filter values,
// chart titles and dashboard text refer to as [$name]. The dialog creates and
// edits them; the bar changes their values on a dashboard for this session.
import { useState } from "react";
import { B, BI, arr, errorText } from "../../api";

const REF = /\[\$([A-Za-z_][A-Za-z0-9_]*)\]/g;

export type ParamValues = Record<string, string>;

/** Parameter values by lower-case name: the saved values, with overrides on top. */
export function paramValues(params: B.Parameter[] | null | undefined, overrides: ParamValues = {}): ParamValues {
  const out: ParamValues = {};
  for (const p of arr(params)) out[p.name.toLowerCase()] = p.value;
  for (const [k, v] of Object.entries(overrides)) out[k.toLowerCase()] = v;
  return out;
}

/** Replace [$name] with its value; unknown names stay as written. */
export function expandParams(text: string, values: Record<string, string | undefined> | null | undefined): string {
  if (!values || !text.includes("[$")) return text;
  return text.replace(REF, (m, name: string) => values[name.toLowerCase()] ?? m);
}

/** The parameter names text refers to. */
export function paramRefs(text: string | null | undefined): string[] {
  return [...(text ?? "").matchAll(REF)].map((m) => m[1].toLowerCase());
}

/** Parameters a dashboard depends on: in its text, its charts' titles and filters,
 *  and their data sources' queries and filters. */
export function dashboardParams(w: B.Workspace | null, d: B.Dashboard | null): B.Parameter[] {
  if (!w || !d) return [];
  const names = new Set<string>();
  const add = (s?: string | null) => paramRefs(s).forEach((n) => names.add(n));
  const filters = (fs?: B.Filter[] | null) => arr(fs).forEach((f) => arr(f.values).forEach(add));
  for (const p of arr(d.pages))
    for (const it of arr(p.items)) {
      add(it.text);
      const c = arr(w.charts).find((x) => x.id === it.chart);
      if (!c) continue;
      add(c.props?.title);
      filters(c.filters);
      const s = arr(w.dataSources).find((x) => x.id === c.dataSource);
      add(s?.sql);
      filters(s?.filters);
    }
  return arr(w.parameters).filter((p) => names.has(p.name.toLowerCase()));
}

/** Controls for a dashboard's parameters: a list, a slider or a text box each. */
export function ParamBar({ params, values, onChange }: { params: B.Parameter[]; values: ParamValues; onChange(name: string, v: string): void }) {
  if (!params.length) return null;
  return (
    <div className="bi-param-bar" data-testid="bi-param-bar">
      {params.map((p) => {
        const v = values[p.name.toLowerCase()] ?? p.value;
        return (
          <label key={p.id} className="bi-param" title={p.remarks || `[$${p.name}]`}>
            <span className="bi-param-name">{p.name}</span>
            {p.allowed === "list" ? (
              <select aria-label={`Parameter ${p.name}`} value={v} onChange={(e) => onChange(p.name, e.target.value)}>
                {arr(p.list).map((o) => (
                  <option key={o.value} value={o.value}>
                    {o.label || o.value}
                  </option>
                ))}
              </select>
            ) : p.allowed === "range" ? (
              <>
                <input aria-label={`Parameter ${p.name}`} type="range" min={p.min} max={p.max} step={p.step || "any"} value={v} onChange={(e) => onChange(p.name, e.target.value)} />
                <span className="bi-param-value">{v}</span>
              </>
            ) : (
              <input
                aria-label={`Parameter ${p.name}`}
                type={p.type === "number" ? "number" : p.type === "datetime" ? "date" : "text"}
                defaultValue={v}
                // A typed value applies when it's complete, not on every key.
                onBlur={(e) => e.target.value !== v && onChange(p.name, e.target.value)}
                onKeyDown={(e) => e.key === "Enter" && (e.target as HTMLInputElement).blur()}
              />
            )}
          </label>
        );
      })}
    </div>
  );
}

/** Create or edit a parameter. */
export function ParameterDialog({ path, param, onClose }: { path: string; param: B.Parameter | null; onClose(saved: boolean): void }) {
  const [p, setP] = useState<B.Parameter>(param ?? ({ id: "", name: "", type: "string", allowed: "", value: "" } as B.Parameter));
  const [listText, setListText] = useState(() =>
    arr(param?.list)
      .map((o) => (o.label ? `${o.value} = ${o.label}` : o.value))
      .join("\n"),
  );
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const set = (patch: Partial<B.Parameter>) => setP((x) => ({ ...x, ...patch }));
  const save = async () => {
    setBusy(true);
    setError("");
    const list =
      p.allowed === "list"
        ? listText
            .split("\n")
            .map((l) => l.trim())
            .filter(Boolean)
            .map((l) => {
              const [value, ...label] = l.split("=");
              return { value: value.trim(), label: label.join("=").trim() };
            })
        : [];
    try {
      await BI.SaveParameter(path, { ...p, list });
      onClose(true);
    } catch (e) {
      setError(errorText(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <div className="modal-backdrop">
      <div className="modal bi-param-dialog" role="dialog" aria-label={param ? "Edit Parameter" : "New Parameter"} onKeyDown={(e) => e.key === "Escape" && onClose(false)}>
        <div className="modal-title">{param ? "Edit Parameter" : "New Parameter"}</div>
        <div className="modal-body">
          <label className="bi-prop">
            Name
            <input aria-label="Parameter name" autoFocus value={p.name} onChange={(e) => set({ name: e.target.value })} placeholder="e.g. region" />
          </label>
          <p className="hint">
            Refer to it as <code>[${p.name || "name"}]</code> in a data source query (where a value goes; it's bound, never pasted), as a filter value, in a chart title or in dashboard text.
          </p>
          <div className="bi-prop-row">
            <label className="bi-prop">
              Data type
              <select aria-label="Parameter type" value={p.type} onChange={(e) => set({ type: e.target.value, allowed: e.target.value !== "number" && p.allowed === "range" ? "" : p.allowed })}>
                <option value="string">Text</option>
                <option value="number">Number</option>
                <option value="datetime">Date</option>
              </select>
            </label>
            <label className="bi-prop">
              Allowed values
              <select aria-label="Allowed values" value={p.allowed ?? ""} onChange={(e) => set({ allowed: e.target.value })}>
                <option value="">Any</option>
                <option value="list">List</option>
                {p.type === "number" && <option value="range">Range</option>}
              </select>
            </label>
          </div>
          {p.allowed === "list" && (
            <label className="bi-prop">
              Values, one per line (value = description)
              <textarea aria-label="Parameter list" rows={4} value={listText} onChange={(e) => setListText(e.target.value)} />
            </label>
          )}
          {p.allowed === "range" && (
            <div className="bi-prop-row">
              <label className="bi-prop">
                Minimum
                <input aria-label="Parameter minimum" type="number" value={p.min ?? ""} onChange={(e) => set({ min: e.target.value })} />
              </label>
              <label className="bi-prop">
                Maximum
                <input aria-label="Parameter maximum" type="number" value={p.max ?? ""} onChange={(e) => set({ max: e.target.value })} />
              </label>
              <label className="bi-prop">
                Step
                <input aria-label="Parameter step" type="number" value={p.step ?? ""} onChange={(e) => set({ step: e.target.value })} />
              </label>
            </div>
          )}
          <label className="bi-prop">
            Current value
            <input aria-label="Parameter value" value={p.value} onChange={(e) => set({ value: e.target.value })} />
          </label>
          <label className="bi-prop">
            Remarks
            <input aria-label="Parameter remarks" value={p.remarks ?? ""} onChange={(e) => set({ remarks: e.target.value })} />
          </label>
          {error && <div className="error-banner">{error}</div>}
        </div>
        <div className="modal-footer">
          <span className="spacer" />
          <button onClick={() => onClose(false)}>Cancel</button>
          <button className="primary" disabled={busy || !p.name.trim()} onClick={save}>
            Save
          </button>
        </div>
      </div>
    </div>
  );
}
