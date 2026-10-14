// Design a collection (indexes, validation, creation options) or a view (source and pipeline).
import { Minus, Plus, Save } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { Mongo, ObjectKind, arr, errorText, type MG } from "../../api";
import { field, formatIndexKeys, formatVal, parseIndexKeys } from "../../mongo/docs";
import { useApp, type Tab } from "../../store";
import { prompt } from "../Dialogs";
import { CodeBlock } from "../ObjectsPane";
import { collectionsChanged } from "./actions";

interface IndexRow {
  /** name on the server; empty for a new index */
  orig: string;
  /** the server's spec of an existing index (keeps options the grid doesn't show) */
  spec?: MG.Val;
  name: string;
  keys: string;
  unique: boolean;
  sparse: boolean;
  hidden: boolean;
  ttl: string;
  partial: string;
}

interface Validation {
  validator: string;
  level: string;
  action: string;
}

const emptyOptions = (): MG.CollOptions => ({
  capped: false, size: 0, max: 0, validator: "", validationLevel: "", validationAction: "", collation: "",
  viewOn: "", pipeline: "", timeField: "", metaField: "", granularity: "", expireAfterSeconds: 0,
});

const rowOf = (ix: MG.IndexInfo): IndexRow => ({
  orig: ix.name,
  spec: ix.spec,
  name: ix.name,
  keys: formatIndexKeys(ix.keys),
  unique: ix.unique,
  sparse: ix.sparse,
  hidden: ix.hidden,
  ttl: ix.ttl >= 0 ? String(ix.ttl) : "",
  partial: ix.partial ?? "",
});

const specOf = (r: IndexRow): MG.IndexSpec => ({
  name: r.name.trim(),
  keys: parseIndexKeys(r.keys),
  unique: r.unique,
  sparse: r.sparse,
  hidden: r.hidden,
  ttl: r.ttl.trim() === "" ? -1 : Number(r.ttl),
  ttlSet: r.ttl.trim() !== "",
  partial: r.partial,
  collation: "",
  base: r.spec,
});

const editable = (r: IndexRow) => ({ name: r.name, keys: r.keys, unique: r.unique, sparse: r.sparse, hidden: r.hidden, ttl: r.ttl, partial: r.partial });
const sameIndex = (a: IndexRow, b: IndexRow) => JSON.stringify(editable(a)) === JSON.stringify(editable(b));
/** Only hidden or an existing TTL changed: collMod can do it without rebuilding the index. */
const inPlace = (old: IndexRow, cur: IndexRow) =>
  JSON.stringify({ ...editable(old), hidden: false, ttl: "" }) === JSON.stringify({ ...editable(cur), hidden: false, ttl: "" }) && (old.ttl === cur.ttl || (old.ttl !== "" && cur.ttl !== ""));

export function CollectionDesigner({ tab }: { tab: Tab }) {
  const connId = tab.connId!;
  const db = tab.ref!.database;
  const isView = tab.ref!.kind === ObjectKind.KindView;
  const [name, setName] = useState(tab.ref!.name);
  const isNew = !name;
  const [section, setSection] = useState(isView ? "definition" : "indexes");
  const [info, setInfo] = useState<MG.CollInfo | null>(null);
  const [orig, setOrig] = useState<IndexRow[]>([]);
  const [rows, setRows] = useState<IndexRow[]>([]);
  const [sel, setSel] = useState(-1);
  const [origVal, setOrigVal] = useState<Validation>({ validator: "", level: "", action: "" });
  const [val, setVal] = useState<Validation>({ validator: "", level: "", action: "" });
  const [opts, setOpts] = useState<MG.CollOptions>(emptyOptions);
  const [view, setView] = useState({ viewOn: "", pipeline: "[\n  \n]" });
  const [origView, setOrigView] = useState({ viewOn: "", pipeline: "" });
  const [sources, setSources] = useState<string[]>([]);
  const [error, setError] = useState("");
  const [script, setScript] = useState("");
  const [busy, setBusy] = useState(false);

  const load = async (n: string) => {
    try {
      const colls = arr(await Mongo.Collections(connId, db, !!n));
      setSources(colls.filter((c) => c.type !== "view").map((c) => c.name));
      if (!n) return;
      const ci = colls.find((c) => c.name === n) ?? null;
      setInfo(ci);
      const o = ci?.options;
      const str = (k: string) => field(o ?? { t: "object" }, k);
      const v: Validation = {
        validator: str("validator") ? formatVal(str("validator")!, "  ") : "",
        level: str("validationLevel")?.v ?? "",
        action: str("validationAction")?.v ?? "",
      };
      setOrigVal(v);
      setVal(v);
      if (isView) {
        const vw = { viewOn: ci?.viewOn ?? "", pipeline: str("pipeline") ? formatVal(str("pipeline")!, "  ") : "[]" };
        setView(vw);
        setOrigView(vw);
      } else {
        const ix = arr(await Mongo.Indexes(connId, db, n)).map(rowOf);
        setOrig(ix);
        setRows(ix);
      }
    } catch (e) {
      setError(errorText(e));
    }
  };

  useEffect(() => {
    load(name);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [connId, db]);

  // ---- what saving does -----------------------------------------------------------------
  const plan = useMemo(() => {
    const drop = orig.filter((o) => o.orig !== "_id_" && !rows.some((r) => r.orig === o.orig)).map((o) => o.orig);
    const changed = rows.filter((r) => r.orig && r.orig !== "_id_" && !sameIndex(r, orig.find((o) => o.orig === r.orig)!));
    const modify = changed.filter((r) => inPlace(orig.find((o) => o.orig === r.orig)!, r));
    const replace = changed.filter((r) => !modify.includes(r));
    const create = rows.filter((r) => !r.orig);
    const validation = val.validator !== origVal.validator || val.level !== origVal.level || val.action !== origVal.action;
    const viewChanged = view.viewOn !== origView.viewOn || view.pipeline !== origView.pipeline;
    return { drop, modify, replace, create, validation, viewChanged };
  }, [orig, rows, val, origVal, view, origView]);
  const indexChanges = plan.drop.length + plan.modify.length + plan.replace.length + plan.create.length;
  const dirty = isNew || indexChanges > 0 || plan.validation || (isView && plan.viewChanged);
  const modifyArgs = (r: IndexRow) => {
    const o = orig.find((x) => x.orig === r.orig)!;
    return { hidden: o.hidden !== r.hidden ? r.hidden : null, ttl: o.ttl !== r.ttl ? Number(r.ttl) : null };
  };

  const fullOptions = (): MG.CollOptions =>
    isView ? { ...emptyOptions(), viewOn: view.viewOn, pipeline: view.pipeline } : { ...opts, validator: val.validator, validationLevel: val.level, validationAction: val.action };

  // Script preview
  useEffect(() => {
    if (section !== "script") return;
    (async () => {
      const coll = name || "<name>";
      const q = JSON.stringify(coll);
      const lines: string[] = [];
      try {
        if (isNew) lines.push(await Mongo.CreateScript(coll, fullOptions()));
        else if (isView && plan.viewChanged) lines.push(`db.runCommand({ collMod: ${q}, viewOn: ${JSON.stringify(view.viewOn)}, pipeline: ${view.pipeline} })`);
        for (const d of isNew ? [] : plan.drop) lines.push(`db.getCollection(${q}).dropIndex(${JSON.stringify(d)})`);
        for (const r of isView || isNew ? [] : plan.modify) {
          const m = modifyArgs(r);
          const parts = [`name: ${JSON.stringify(r.orig)}`, ...(m.hidden !== null ? [`hidden: ${m.hidden}`] : []), ...(m.ttl !== null ? [`expireAfterSeconds: ${m.ttl}`] : [])];
          lines.push(`db.runCommand({ collMod: ${q}, index: { ${parts.join(", ")} } })`);
        }
        for (const r of isView || isNew ? [] : plan.replace) {
          lines.push(`db.getCollection(${q}).dropIndex(${JSON.stringify(r.orig)})`);
          lines.push(await Mongo.IndexScript(coll, specOf(r)));
        }
        for (const r of isView ? [] : plan.create) lines.push(await Mongo.IndexScript(coll, specOf(r)));
        if (!isNew && !isView && plan.validation) {
          const parts = [`collMod: ${q}`, `validator: ${val.validator.trim() || "{}"}`];
          if (val.level) parts.push(`validationLevel: ${JSON.stringify(val.level)}`);
          if (val.action) parts.push(`validationAction: ${JSON.stringify(val.action)}`);
          lines.push(`db.runCommand({ ${parts.join(", ")} })`);
        }
        setScript(lines.join(";\n\n") + (lines.length ? ";" : "// No changes"));
      } catch (e) {
        setScript("// " + errorText(e));
      }
    })();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [section, rows, val, opts, view, name]);

  const save = async () => {
    if (busy) return;
    let target = name;
    if (!target) {
      const n = (await prompt(isView ? "New View" : "New Collection", { message: "Name" }))?.trim();
      if (!n) return;
      target = n;
    }
    setBusy(true);
    setError("");
    try {
      // Check every new or changed index before changing anything on the server.
      if (!isView) for (const r of [...plan.replace, ...plan.create]) await Mongo.IndexScript(target, specOf(r));
      if (isNew) await Mongo.CreateCollection(connId, db, target, fullOptions());
      else if (isView && plan.viewChanged) await Mongo.SetViewPipeline(connId, db, target, view.viewOn, view.pipeline);
      if (!isView) {
        for (const d of isNew ? [] : plan.drop) await Mongo.DropIndex(connId, db, target, d);
        for (const r of isNew ? [] : plan.modify) {
          const m = modifyArgs(r);
          await Mongo.ModifyIndex(connId, db, target, r.orig, m.hidden, m.ttl);
        }
        // Dropped and recreated one by one; the backend puts an index back if its new spec is refused.
        for (const r of isNew ? [] : plan.replace) await Mongo.ReplaceIndex(connId, db, target, r.orig, specOf(r));
        for (const r of plan.create) await Mongo.CreateIndex(connId, db, target, specOf(r));
        if (!isNew && plan.validation) await Mongo.SetValidation(connId, db, target, fullOptions());
      }
      if (isNew) {
        setName(target);
        const what = isView ? "View" : "Collection";
        useApp.getState().updateTab(tab.id, { title: `${target} @${db} (${what})`, ref: { ...tab.ref!, name: target, signature: undefined } });
      }
      collectionsChanged(connId, db);
      await load(target);
    } catch (e) {
      setError(errorText(e));
      // What did get applied is now on the server.
      if (!isNew) load(target);
    } finally {
      setBusy(false);
    }
  };

  const setRow = (i: number, patch: Partial<IndexRow>) => setRows((rs) => rs.map((r, j) => (j === i ? { ...r, ...patch } : r)));
  const sections: [string, string][] = isView
    ? [["definition", "Definition"], ["script", "Script Preview"]]
    : [["indexes", "Indexes"], ["validation", "Validation"], ["options", "Options"], ["script", "Script Preview"]];

  return (
    <div
      className="designer mongo-designer"
      onKeyDown={(e) => {
        if ((e.metaKey || e.ctrlKey) && e.key === "s") {
          e.preventDefault();
          save();
        }
      }}
    >
      <div className="toolbar">
        <button className={dirty ? "accent" : ""} disabled={busy || !dirty} onClick={save} title="Save (⌘S)">
          <Save size={14} /> Save
        </button>
        {section === "indexes" && (
          <>
            <span className="tb-sep" />
            <button
              onClick={() => {
                setRows((rs) => [...rs, { orig: "", name: "", keys: "", unique: false, sparse: false, hidden: false, ttl: "", partial: "" }]);
                setSel(rows.length);
              }}
            >
              <Plus size={14} /> Add Index
            </button>
            <button disabled={sel < 0 || rows[sel]?.orig === "_id_"} onClick={() => (setRows((rs) => rs.filter((_, j) => j !== sel)), setSel(-1))}>
              <Minus size={14} /> Delete Index
            </button>
          </>
        )}
        <span className="spacer" />
        {info && !isView && (
          <span className="muted">
            {info.count.toLocaleString()} documents{info.capped ? " · capped" : ""}
            {info.type === "timeseries" ? " · time series" : ""}
          </span>
        )}
      </div>
      <div className="seg-tabs designer-tabs">
        {sections.map(([k, label]) => (
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
        {section === "indexes" && (
          <div className="fields-grid">
          <table>
            <thead>
              <tr>
                <th>Name</th>
                <th title="Comma separated; field:-1 descending, field:text, field:2dsphere, field:hashed">Fields</th>
                <th>Unique</th>
                <th>Sparse</th>
                <th>Hidden</th>
                <th title="Expire documents this many seconds after the date in the (single) field">TTL (s)</th>
                <th>Partial Filter</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((r, i) => {
                const locked = r.orig === "_id_";
                return (
                  <tr key={i} className={sel === i ? "selected" : ""} onMouseDown={() => setSel(i)}>
                    <td>
                      <input value={r.name} disabled={locked} placeholder="(automatic)" onChange={(e) => setRow(i, { name: e.target.value })} />
                    </td>
                    <td>
                      <input value={r.keys} disabled={locked} placeholder="field, other:-1" spellCheck={false} onChange={(e) => setRow(i, { keys: e.target.value })} />
                    </td>
                    {(["unique", "sparse", "hidden"] as const).map((k) => (
                      <td key={k} className="center">
                        <input type="checkbox" disabled={locked} checked={r[k]} onChange={(e) => setRow(i, { [k]: e.target.checked })} />
                      </td>
                    ))}
                    <td>
                      <input value={r.ttl} disabled={locked} className="narrow" onChange={(e) => setRow(i, { ttl: e.target.value.replace(/[^\d]/g, "") })} />
                    </td>
                    <td>
                      <input value={r.partial} disabled={locked} spellCheck={false} placeholder="{ status: 'active' }" onChange={(e) => setRow(i, { partial: e.target.value })} />
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
          </div>
        )}
        {section === "validation" && (
          <div className="form mongo-validation">
            <div className="field">
              <label>Validation Level</label>
              <div className="field-input">
                <select value={val.level} onChange={(e) => setVal({ ...val, level: e.target.value })}>
                  <option value="">(default: strict)</option>
                  <option value="off">off</option>
                  <option value="strict">strict</option>
                  <option value="moderate">moderate</option>
                </select>
              </div>
            </div>
            <div className="field">
              <label>Validation Action</label>
              <div className="field-input">
                <select value={val.action} onChange={(e) => setVal({ ...val, action: e.target.value })}>
                  <option value="">(default: error)</option>
                  <option value="error">error</option>
                  <option value="warn">warn</option>
                </select>
              </div>
            </div>
            <div className="field top">
              <label>Validator</label>
              <div className="field-input">
                <textarea
                  className="code"
                  spellCheck={false}
                  rows={16}
                  placeholder={"{ $jsonSchema: { bsonType: 'object', required: ['email'] } }"}
                  value={val.validator}
                  onChange={(e) => setVal({ ...val, validator: e.target.value })}
                />
              </div>
            </div>
          </div>
        )}
        {section === "options" && (
          <div className="form">
            {!isNew ? (
              <>
                <p className="muted">These options are set when a collection is created.</p>
                {info && <CodeBlock code={formatVal(info.options, "  ")} language="mongo" />}
              </>
            ) : (
              <>
                <div className="field">
                  <label>Capped</label>
                  <div className="field-input">
                    <input type="checkbox" checked={opts.capped} onChange={(e) => setOpts({ ...opts, capped: e.target.checked, size: opts.size || 1048576 })} />
                  </div>
                </div>
                {opts.capped && (
                  <>
                    <div className="field">
                      <label>Size (bytes)</label>
                      <div className="field-input">
                        <input value={opts.size || ""} onChange={(e) => setOpts({ ...opts, size: Number(e.target.value.replace(/\D/g, "")) })} />
                      </div>
                    </div>
                    <div className="field">
                      <label>Max Documents</label>
                      <div className="field-input">
                        <input value={opts.max || ""} placeholder="(no limit)" onChange={(e) => setOpts({ ...opts, max: Number(e.target.value.replace(/\D/g, "")) })} />
                      </div>
                    </div>
                  </>
                )}
                <div className="field">
                  <label>Time Field</label>
                  <div className="field-input">
                    <input value={opts.timeField} placeholder="(not a time series)" onChange={(e) => setOpts({ ...opts, timeField: e.target.value })} />
                  </div>
                </div>
                {opts.timeField && (
                  <>
                    <div className="field">
                      <label>Meta Field</label>
                      <div className="field-input">
                        <input value={opts.metaField} onChange={(e) => setOpts({ ...opts, metaField: e.target.value })} />
                      </div>
                    </div>
                    <div className="field">
                      <label>Granularity</label>
                      <div className="field-input">
                        <select value={opts.granularity} onChange={(e) => setOpts({ ...opts, granularity: e.target.value })}>
                          <option value="">(default: seconds)</option>
                          <option value="seconds">seconds</option>
                          <option value="minutes">minutes</option>
                          <option value="hours">hours</option>
                        </select>
                      </div>
                    </div>
                    <div className="field">
                      <label>Expire After (s)</label>
                      <div className="field-input">
                        <input value={opts.expireAfterSeconds || ""} onChange={(e) => setOpts({ ...opts, expireAfterSeconds: Number(e.target.value.replace(/\D/g, "")) })} />
                      </div>
                    </div>
                  </>
                )}
                <div className="field">
                  <label>Collation</label>
                  <div className="field-input">
                    <input value={opts.collation} spellCheck={false} placeholder="{ locale: 'en', strength: 2 }" onChange={(e) => setOpts({ ...opts, collation: e.target.value })} />
                  </div>
                </div>
              </>
            )}
          </div>
        )}
        {section === "definition" && (
          <div className="form mongo-validation">
            <div className="field">
              <label>View On</label>
              <div className="field-input">
                <select value={view.viewOn} onChange={(e) => setView({ ...view, viewOn: e.target.value })}>
                  <option value="">(choose a collection)</option>
                  {[...new Set([view.viewOn, ...sources].filter(Boolean))].map((s) => (
                    <option key={s} value={s}>
                      {s}
                    </option>
                  ))}
                </select>
              </div>
            </div>
            <div className="field top">
              <label>Pipeline</label>
              <div className="field-input">
                <textarea className="code" spellCheck={false} rows={18} value={view.pipeline} onChange={(e) => setView({ ...view, pipeline: e.target.value })} />
              </div>
            </div>
          </div>
        )}
        {section === "script" && <CodeBlock code={script} language="mongo" />}
      </div>
    </div>
  );
}
