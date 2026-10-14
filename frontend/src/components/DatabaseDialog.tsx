// New Database / Edit Database, with a SQL preview, plus Delete Database.
import { useEffect, useMemo, useState } from "react";
import { create } from "zustand";
import { D, DBType, Design, arr, errorText } from "../api";
import { invalidateCatalog } from "../editor/completion";
import { connById, useApp } from "../store";
import { useTree } from "../tree";
import { alert, confirm } from "./Dialogs";
import { CodeBlock } from "./ObjectsPane";

const useDbDialog = create<{ target?: { connId: string; name?: string } }>(() => ({}));

/** Open New Database (no name) or Edit Database. */
export const openDatabaseDialog = (connId: string, name?: string) => useDbDialog.setState({ target: { connId, name } });

const refreshConnection = (connId: string) =>
  useTree.getState().refresh({ key: connId, type: "conn", label: "", connId, depth: 0, expandable: true });

export async function deleteDatabase(connId: string, name: string) {
  if (!(await confirm(`Delete database ${name}?`, { message: "All of its tables and data are deleted. This can't be undone.", okLabel: "Delete" }))) return;
  await useApp.getState().closeDatabase(connId, name);
  const res = await Design.DropDatabase(connId, name);
  if (res.error) return alert("Delete failed", res.error);
  invalidateCatalog(connId, name);
  await refreshConnection(connId);
}

export function DatabaseDialog() {
  const target = useDbDialog((s) => s.target);
  if (!target) return null;
  return <DatabaseDialogInner key={`${target.connId}/${target.name ?? ""}`} connId={target.connId} editName={target.name} />;
}

type DB = D.Database;

function DatabaseDialogInner({ connId, editName }: { connId: string; editName?: string }) {
  const pg = connById(connId)?.type === DBType.PostgreSQL;
  const [meta, setMeta] = useState<D.DatabaseMeta | null>(null);
  const [original, setOriginal] = useState<DB | null>(null);
  const [d, setD] = useState<DB>({ name: "" });
  const [tab, setTab] = useState<"general" | "sql">("general");
  const [sql, setSql] = useState<string[]>([]);
  const [error, setError] = useState<{ text: string; sql?: string } | null>(null);
  const [busy, setBusy] = useState(false);
  const close = () => useDbDialog.setState({ target: undefined });
  const editing = editName !== undefined;

  useEffect(() => {
    (async () => {
      try {
        setMeta(await Design.DatabaseMeta(connId));
        if (editing) {
          const cur = await Design.LoadDatabase(connId, editName!);
          setOriginal(cur);
          setD(cur);
        }
      } catch (e) {
        setError({ text: errorText(e) });
      }
    })();
  }, [connId, editName, editing]);

  useEffect(() => {
    if (tab !== "sql") return;
    Design.DatabaseSQL(connId, original, d)
      .then((s) => (setSql(arr(s)), setError(null)))
      .catch((e) => (setSql([]), setError({ text: errorText(e) })));
  }, [tab, d, original, connId]);

  // Without a chosen charset the database gets the server's, so offer its collations.
  const charset = d.charset || meta?.defaultCharset;
  const collations = useMemo(() => arr(meta?.collations).filter((c) => !charset || c.charset === charset), [meta, charset]);
  const set = (patch: Partial<DB>) => setD((x) => ({ ...x, ...patch }));

  const ok = async () => {
    setBusy(true);
    setError(null);
    try {
      const res = await Design.ApplyDatabase(connId, original, d);
      if (res.error) return setError({ text: res.error, sql: res.failedStatement });
      if (original && original.name !== d.name) await useApp.getState().closeDatabase(connId, original.name);
      invalidateCatalog(connId);
      await refreshConnection(connId);
      close();
    } catch (e) {
      setError({ text: errorText(e) });
    } finally {
      setBusy(false);
    }
  };

  const select = (label: string, value: string | undefined, options: string[], onChange: (v: string) => void, placeholder = "", disabled = false) => (
    <div className="field">
      <label>{label}</label>
      <div className="field-input">
        <select value={value ?? ""} disabled={disabled} onChange={(e) => onChange(e.target.value)}>
          <option value="">{placeholder}</option>
          {value && !options.includes(value) && <option>{value}</option>}
          {options.map((o) => (
            <option key={o}>{o}</option>
          ))}
        </select>
      </div>
    </div>
  );

  return (
    <div className="modal-backdrop" onMouseDown={close}>
      <div className="modal database-dialog" onMouseDown={(e) => e.stopPropagation()} onKeyDown={(e) => e.key === "Escape" && close()}>
        <div className="modal-title">{editing ? `Edit Database — ${editName}` : "New Database"}</div>
        <div className="seg-tabs centered">
          <button className={tab === "general" ? "active" : ""} onClick={() => setTab("general")}>
            General
          </button>
          <button className={tab === "sql" ? "active" : ""} onClick={() => setTab("sql")}>
            SQL Preview
          </button>
        </div>
        {tab === "general" ? (
          <div className="modal-body form">
            <div className="field">
              <label>Database Name</label>
              <div className="field-input">
                <input autoFocus value={d.name} disabled={editing && !pg} onChange={(e) => set({ name: e.target.value })} onKeyDown={(e) => e.key === "Enter" && ok()} />
              </div>
            </div>
            {!pg && (
              <>
                {select("Character Set", d.charset, arr(meta?.charsets), (v) => set({ charset: v || undefined, collation: undefined }), meta?.defaultCharset ? `(default: ${meta.defaultCharset})` : "")}
                {select("Collation", d.collation, collations.map((c) => c.name), (v) => set({ collation: v || undefined }), !d.charset && meta?.defaultCollation ? `(default: ${meta.defaultCollation})` : collations.find((c) => c.default) ? `(default: ${collations.find((c) => c.default)!.name})` : "")}
              </>
            )}
            {pg && (
              <>
                {select("Owner", d.owner, arr(meta?.owners), (v) => set({ owner: v || undefined }), "(current user)")}
                {select("Template", d.template, arr(meta?.templates), (v) => set({ template: v || undefined }), "(template1)", editing)}
                {select("Encoding", d.encoding, arr(meta?.encodings), (v) => set({ encoding: v || undefined }), "(template's)", editing)}
                {select("Collation", d.collation, arr(meta?.locales), (v) => set({ collation: v || undefined }), "(template's)", editing)}
                {select("Character Type", d.ctype, arr(meta?.locales), (v) => set({ ctype: v || undefined }), "(template's)", editing)}
                {select("Tablespace", d.tablespace, arr(meta?.tablespaces), (v) => set({ tablespace: v || undefined }), "(default)")}
                <div className="field">
                  <label>Connection Limit</label>
                  <div className="field-input">
                    <input
                      type="number"
                      min={-1}
                      placeholder="-1 (no limit)"
                      value={d.connLimit ?? ""}
                      onChange={(e) => set({ connLimit: e.target.value === "" || Number(e.target.value) < 0 ? undefined : Number(e.target.value) })}
                    />
                  </div>
                </div>
                <div className="field">
                  <label>Comment</label>
                  <div className="field-input">
                    <textarea rows={3} value={d.comment ?? ""} onChange={(e) => set({ comment: e.target.value || undefined })} />
                  </div>
                </div>
                {!editing && (d.encoding || d.collation || d.ctype) && !d.template && (
                  <div className="muted small">A different encoding or locale than template1's usually needs template0.</div>
                )}
              </>
            )}
          </div>
        ) : (
          <div className="modal-body">{sql.length ? <CodeBlock code={sql.join(";\n\n") + ";"} /> : <div className="center-hint">No changes.</div>}</div>
        )}
        {error && (
          <div className="status-line err">
            {error.text}
            {error.sql && <pre>{error.sql}</pre>}
          </div>
        )}
        <div className="modal-footer">
          <span className="spacer" />
          <button onClick={close}>Cancel</button>
          <button className="primary" disabled={busy || !d.name.trim()} onClick={ok}>
            OK
          </button>
        </div>
      </div>
    </div>
  );
}
