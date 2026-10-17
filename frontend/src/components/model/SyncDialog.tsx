// Synchronize a data model to a database: pick the target, compare, choose which
// table changes to run, execute.
import { useEffect, useMemo, useState } from "react";
import { DBType, DM, Explorer, Models, arr, errorText } from "../../api";
import { invalidateCatalog } from "../../editor/completion";
import { useApp } from "../../store";
import { CodeBlock } from "../ObjectsPane";
import { Modal } from "../Modal";
import { Field, Check } from "../form";

const KIND_LABEL: Record<string, string> = { create: "Create", alter: "Modify", drop: "Delete" };

export function SyncDialog({ model, onClose, onSynced }: { model: DM.Model; onClose(): void; onSynced?(link: DM.Link): void }) {
  const pg = model.dialect === "postgres";
  const connections = useApp((s) => s.connections).filter((c) => (pg ? c.type === DBType.PostgreSQL : c.type === DBType.MySQL || c.type === DBType.MariaDB));
  // Default to the database the model came from or was last synchronized to.
  const linked = model.link && connections.some((c) => c.id === model.link!.connId) ? model.link : null;
  const [connId, setConnId] = useState(linked?.connId ?? connections[0]?.id ?? "");
  const [databases, setDatabases] = useState<string[]>([]);
  const [database, setDatabase] = useState("");
  const [schemas, setSchemas] = useState<string[]>([]);
  const [schema, setSchema] = useState(model.link?.schema || model.schema || "");
  const [dropMissing, setDropMissing] = useState(false);
  const [plan, setPlan] = useState<DM.SyncPlan | null>(null);
  const [include, setInclude] = useState<Record<number, boolean>>({});
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<{ text: string; sql?: string } | null>(null);
  const [done, setDone] = useState("");

  useEffect(() => {
    if (!connId) return;
    setDatabases([]);
    setPlan(null);
    (async () => {
      if (!(await useApp.getState().connect(connId))) return;
      try {
        const dbs = arr(await Explorer.ListDatabases(connId));
        setDatabases(dbs);
        const preferred = linked?.connId === connId ? linked.database : "";
        setDatabase((d) => (dbs.includes(d) ? d : dbs.includes(preferred) ? preferred : (dbs.find((x) => x === model.name) ?? dbs.find((x) => !SYSTEM_DBS.has(x)) ?? dbs[0] ?? "")));
      } catch (e) {
        setError({ text: errorText(e) });
      }
    })();
  }, [connId, model.name]);

  useEffect(() => {
    setPlan(null);
    if (!pg || !connId || !database) return;
    Explorer.ListSchemas(connId, database)
      .then((s) => setSchemas(arr(s)))
      .catch(() => setSchemas([]));
  }, [pg, connId, database]);

  const target: DM.Target = { database, schema: pg ? schema : undefined };

  const compare = async () => {
    setBusy(true);
    setError(null);
    setDone("");
    try {
      const p = await Models.PlanSync(connId, model, target, dropMissing);
      setPlan(p);
      setInclude(Object.fromEntries(arr(p.actions).map((_, i) => [i, true])));
    } catch (e) {
      setError({ text: errorText(e) });
    } finally {
      setBusy(false);
    }
  };

  const chosen = useMemo(() => arr(plan?.actions).filter((_, i) => include[i]), [plan, include]);
  const statements = useMemo(() => {
    // Same order as SyncPlan.Statements: creates, then alters, then drops.
    return ["create", "alter", "drop"].flatMap((k) => chosen.filter((a) => a.kind === k).flatMap((a) => arr(a.statements)));
  }, [chosen]);

  const execute = async () => {
    setBusy(true);
    setError(null);
    try {
      const res = await Models.ApplySync(connId, target, { ...plan!, actions: chosen });
      if (res.error) {
        setError({ text: res.error + (res.executed ? `\n(${res.executed} statement(s) before it were applied)` : ""), sql: res.failedStatement });
        return;
      }
      invalidateCatalog(connId, database);
      useApp.setState((s) => ({ objectsVersion: s.objectsVersion + 1 }));
      setDone(`Synchronized: ${statements.length} statement(s) executed.`);
      onSynced?.({ connId, database, schema: pg ? schema : undefined });
      setPlan(null);
    } catch (e) {
      setError({ text: errorText(e) });
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal title="Synchronize to Database" className="wizard wide sync-dialog" onClose={onClose}>
      <div className="modal-body form">
        <Field label="Connection">
          <select value={connId} onChange={(e) => setConnId(e.target.value)}>
            {!connections.length && <option value="">No {pg ? "PostgreSQL" : "MySQL"} connections</option>}
            {connections.map((c) => (
              <option key={c.id} value={c.id}>
                {c.name}
              </option>
            ))}
          </select>
        </Field>
        <div className="field">
          <label>Database</label>
          <div className="field-input row">
            <select value={database} onChange={(e) => (setDatabase(e.target.value), setPlan(null))}>
              {databases.map((d) => (
                <option key={d}>{d}</option>
              ))}
            </select>
            {pg && (
              <select value={schema} onChange={(e) => (setSchema(e.target.value), setPlan(null))} title="Schema">
                <option value="">(table schemas)</option>
                {schemas.map((s) => (
                  <option key={s}>{s}</option>
                ))}
              </select>
            )}
          </div>
        </div>
        <Field>
          <Check label="Delete tables that aren't in the model" checked={dropMissing} onChange={(v) => (setDropMissing(v), setPlan(null))} />
        </Field>
        {plan && (
          <div className="sync-plan">
            <div className="sync-actions">
              {!arr(plan.actions).length && <div className="center-hint">The database already matches the model.</div>}
              {arr(plan.actions).map((a, i) => (
                <label key={i} className="sync-action">
                  <input type="checkbox" checked={!!include[i]} onChange={(e) => setInclude((x) => ({ ...x, [i]: e.target.checked }))} />
                  {a.table.startsWith("(") ? (
                    <>
                      <span className="badge kind-create">Add</span>
                      <span>Foreign keys of new tables</span>
                    </>
                  ) : (
                    <>
                      <span className={`badge kind-${a.kind}`}>{KIND_LABEL[a.kind] ?? a.kind}</span>
                      <span>{a.table}</span>
                    </>
                  )}
                  <span className="muted">{arr(a.statements).length} statement(s)</span>
                </label>
              ))}
              {arr(plan.unchanged).length > 0 && <div className="muted small">Unchanged: {arr(plan.unchanged).join(", ")}</div>}
              {arr(plan.warnings).map((w, i) => (
                <div key={i} className="badge warn block">
                  {w}
                </div>
              ))}
            </div>
            <div className="sync-sql">{statements.length ? <CodeBlock code={statements.join(";\n\n") + ";"} /> : <div className="center-hint">Nothing selected.</div>}</div>
          </div>
        )}
      </div>
      {error && (
        <div className="status-line err">
          {error.text}
          {error.sql && <pre>{error.sql}</pre>}
        </div>
      )}
      {done && <div className="status-line ok">{done}</div>}
      <div className="modal-footer">
        <span className="spacer" />
        <button onClick={onClose}>{done ? "Close" : "Cancel"}</button>
        <button disabled={!connId || !database || busy} onClick={compare}>
          {busy && !plan ? "Comparing…" : "Compare"}
        </button>
        <button className="primary" disabled={!plan || !statements.length || busy} onClick={execute}>
          Execute
        </button>
      </div>
    </Modal>
  );
}

const SYSTEM_DBS = new Set(["information_schema", "mysql", "performance_schema", "sys", "template0", "template1"]);
