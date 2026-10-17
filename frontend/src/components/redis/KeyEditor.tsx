// Value editor for one Redis key: header with name (rename), TTL, size and type,
// then a string editor (text / JSON / hex) or a collection grid with a detail pane.
import { Clock, Minus, Plus, RefreshCw, Save, Trash2 } from "lucide-react";
import { useCallback, useEffect, useMemo, useState } from "react";
import { R, Redis, arr, errorText } from "../../api";
import { TYPE_LABEL, display, formatBytes, formatTTL, hexDump, text } from "../../redis/keys";
import { alert, confirm } from "../Dialogs";
import { Split } from "../Split";
import { Toolbar } from "../Toolbar";
import { Field } from "../form";

interface Props {
  connId: string;
  db: number;
  redisKey: R.Key;
  onDeleted(): void;
  onRenamed(to: R.Value): void;
  onChanged(patch: Partial<R.Key>): void;
}

export function KeyEditor({ connId, db, redisKey, onDeleted, onRenamed, onChanged }: Props) {
  const [kv, setKv] = useState<R.KeyValue | null>(null);
  const [error, setError] = useState("");
  const [name, setName] = useState(redisKey.name.text);
  const [ttl, setTtl] = useState("");
  const key = redisKey.name;

  const load = useCallback(async () => {
    setError("");
    try {
      const v = await Redis.GetValue(connId, db, key, "", 500);
      setKv(v);
      setTtl(v.ttl >= 0 ? String(v.ttl) : "");
      onChanged({ type: v.type, ttl: v.ttl });
    } catch (e) {
      setError(errorText(e));
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [connId, db, key.text, key.base64]);

  useEffect(() => {
    load();
  }, [load]);

  const rename = async () => {
    const to = name.trim();
    if (!to || to === key.text || key.base64) return setName(key.text);
    try {
      await Redis.RenameKey(connId, db, key, text(to));
      onRenamed(text(to));
    } catch (e) {
      alert("Rename failed", errorText(e));
      setName(key.text);
    }
  };

  const applyTTL = async (seconds: number) => {
    try {
      await Redis.SetTTL(connId, db, key, seconds);
      await load();
    } catch (e) {
      alert("Couldn't set TTL", errorText(e));
    }
  };

  const del = async () => {
    if (!(await confirm(`Delete key ${display(key)}?`, { okLabel: "Delete" }))) return;
    try {
      await Redis.DeleteKeys(connId, db, [key]);
      onDeleted();
    } catch (e) {
      alert("Delete failed", errorText(e));
    }
  };

  if (error && !kv) return <div className="center-hint err">{error}</div>;
  if (!kv) return <div className="center-hint">Loading…</div>;

  return (
    <div className="redis-value">
      <div className="redis-value-head">
        <span className={`redis-type t-${kv.type}`}>{TYPE_LABEL[kv.type] ?? kv.type}</span>
        <input
          className="redis-key-input"
          value={key.base64 ? display(key) : name}
          readOnly={key.base64}
          onChange={(e) => setName(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") (e.target as HTMLInputElement).blur();
            if (e.key === "Escape") setName(key.text);
          }}
          onBlur={rename}
          title="Key name (edit to rename)"
        />
        <button onClick={load} title="Reload">
          <RefreshCw size={14} />
        </button>
        <button onClick={del} title="Delete key">
          <Trash2 size={14} />
        </button>
      </div>
      <div className="redis-value-meta">
        <span className="ttl">
          <Clock size={12} /> TTL
          <input
            type="number"
            min={1}
            placeholder="No limit"
            value={ttl}
            onChange={(e) => setTtl(e.target.value)}
            onKeyDown={(e) => e.key === "Enter" && Number(ttl) > 0 && applyTTL(Number(ttl))}
          />
          <button className="small-btn" disabled={!(Number(ttl) > 0) || Number(ttl) === kv.ttl} onClick={() => applyTTL(Number(ttl))}>
            Set
          </button>
          <button className="small-btn" disabled={kv.ttl < 0} onClick={() => applyTTL(0)} title="Remove the expiry (PERSIST)">
            No Limit
          </button>
          <span className="muted">{kv.ttl >= 0 ? `expires in ${formatTTL(kv.ttl)}` : ""}</span>
        </span>
        <span className="spacer" />
        <span className="muted">
          {kv.type === "string" ? formatBytes(kv.length) : `${kv.length.toLocaleString()} ${kv.type === "hash" ? "fields" : kv.type === "stream" ? "entries" : "items"}`}
          {kv.size ? ` · memory ${formatBytes(kv.size)}` : ""}
          {kv.encoding ? ` · ${kv.encoding}` : ""}
        </span>
      </div>
      {error && <div className="error-banner">{error}</div>}
      {kv.type === "string" ? (
        <StringEditor connId={connId} db={db} kv={kv} onSaved={load} />
      ) : ["hash", "list", "set", "zset", "stream"].includes(kv.type) ? (
        <ItemsEditor connId={connId} db={db} kv={kv} reload={load} />
      ) : (
        <div className="center-hint">{kv.type} values (a module type) can be read and changed in the console.</div>
      )}
    </div>
  );
}

// ---- strings ---------------------------------------------------------------------------

type Mode = "text" | "json" | "hex";

function isJSON(s: string) {
  const t = s.trim();
  if (!(t.startsWith("{") || t.startsWith("["))) return false;
  try {
    JSON.parse(t);
    return true;
  } catch {
    return false;
  }
}

function StringEditor({ connId, db, kv, onSaved }: { connId: string; db: number; kv: R.KeyValue; onSaved(): void }) {
  const value = kv.string!;
  const binary = !!value.base64;
  const truncated = kv.length > (binary ? value.text.length * 0.75 : new TextEncoder().encode(value.text).length) + 4;
  const [mode, setMode] = useState<Mode>(binary ? "hex" : isJSON(value.text) ? "json" : "text");
  const shown = useMemo(() => (mode === "json" && !binary ? JSON.stringify(JSON.parse(value.text), null, 2) : mode === "hex" ? hexDump(value) : display(value)), [mode, value, binary]);
  const [draft, setDraft] = useState(shown);
  const [err, setErr] = useState("");
  useEffect(() => setDraft(shown), [shown]);
  const dirty = draft !== shown;
  const editable = !binary && mode !== "hex" && !truncated;

  const save = async () => {
    let out = draft;
    if (mode === "json") {
      try {
        out = JSON.stringify(JSON.parse(draft)); // stored compact, as it was
      } catch (e) {
        return setErr(`Invalid JSON: ${errorText(e)}`);
      }
    }
    setErr("");
    try {
      await Redis.SetString(connId, db, kv.name, text(out));
      onSaved();
    } catch (e) {
      setErr(errorText(e));
    }
  };

  return (
    <div className="redis-string" onKeyDown={(e) => (e.metaKey || e.ctrlKey) && e.key === "s" && (e.preventDefault(), dirty && editable && save())}>
      <Toolbar>
        <div className="seg">
          {(["text", "json", "hex"] as Mode[]).map((m) => (
            <button key={m} className={mode === m ? "active" : ""} disabled={m === "json" && (binary || !isJSON(value.text))} onClick={() => setMode(m)}>
              {m === "text" ? "Text" : m === "json" ? "JSON" : "Hex"}
            </button>
          ))}
        </div>
        <span className="spacer" />
        {binary && <span className="muted small">Binary value — shown read-only</span>}
        {truncated && <span className="muted small">Large value — first 4 MB shown, read-only</span>}
        <button className={dirty ? "accent" : ""} disabled={!dirty || !editable} onClick={save} title="Save (⌘S)">
          <Save size={14} /> Save
        </button>
      </Toolbar>
      {err && <div className="error-banner">{err}</div>}
      <textarea className="redis-text" spellCheck={false} readOnly={!editable} value={draft} onChange={(e) => setDraft(e.target.value)} />
    </div>
  );
}

// ---- collections ------------------------------------------------------------------------

interface Detail {
  /** index into items/entries, or -1 for a new element */
  row: number;
  field: string;
  value: string;
  score: string;
  id: string;
  /** stream: field/value lines */
  pairs: Array<[string, string]>;
}

function ItemsEditor({ connId, db, kv, reload }: { connId: string; db: number; kv: R.KeyValue; reload(): void }) {
  const type = kv.type;
  const [items, setItems] = useState<R.Field[]>(arr(kv.items));
  const [entries, setEntries] = useState<R.Entry[]>(arr(kv.entries));
  const [cursor, setCursor] = useState(kv.cursor);
  const [sel, setSel] = useState<Set<number>>(new Set());
  const [detail, setDetail] = useState<Detail | null>(null);
  const [err, setErr] = useState("");

  useEffect(() => {
    setItems(arr(kv.items));
    setEntries(arr(kv.entries));
    setCursor(kv.cursor);
    setSel(new Set());
    setDetail(null);
  }, [kv]);

  const rowCount = type === "stream" ? entries.length : items.length;

  const loadMore = async () => {
    try {
      const more = await Redis.GetValue(connId, db, kv.name, cursor, 500);
      if (type === "stream") setEntries((e) => [...e, ...arr(more.entries)]);
      else setItems((i) => [...i, ...arr(more.items)]);
      setCursor(more.cursor);
    } catch (e) {
      setErr(errorText(e));
    }
  };

  const open = (row: number) => {
    setSel(new Set([row]));
    if (type === "stream") {
      const e = entries[row];
      setDetail({ row, field: "", value: "", score: "", id: e.id, pairs: arr(e.fields).map((f) => [display(f.field!), display(f.value)]) });
    } else {
      const it = items[row];
      setDetail({ row, field: it.field ? display(it.field) : "", value: display(it.value), score: it.score !== undefined && it.score !== null ? String(it.score) : "", id: "", pairs: [] });
    }
  };

  const binaryRow = detail && detail.row >= 0 && type !== "stream" && (items[detail.row].value.base64 || items[detail.row].field?.base64);

  const save = async () => {
    if (!detail) return;
    setErr("");
    try {
      if (type === "stream") {
        if (detail.row >= 0) return;
        const fields = detail.pairs.filter(([f]) => f.trim()).map(([f, v]) => ({ field: text(f.trim()), value: text(v) }));
        if (!fields.length) return setErr("Add at least one field.");
        await Redis.AddStreamEntry(connId, db, kv.name, detail.id.trim() || "*", fields);
      } else {
        const it = detail.row >= 0 ? items[detail.row] : undefined;
        const edit: R.ItemEdit = { value: text(detail.value) };
        if (type === "hash") {
          if (!detail.field) return setErr("Enter a field name.");
          edit.field = text(detail.field);
          if (it?.field) edit.oldField = it.field;
        }
        if (type === "list" && it) edit.index = it.index;
        if ((type === "set" || type === "zset") && it) edit.oldValue = it.value;
        if (type === "zset") {
          const n = Number(detail.score);
          if (detail.score.trim() === "" || Number.isNaN(n)) return setErr("Enter a numeric score.");
          edit.score = n;
        }
        await Redis.EditItem(connId, db, kv.name, type, edit);
      }
      reload();
    } catch (e) {
      setErr(errorText(e));
    }
  };

  const remove = async () => {
    const rows = [...sel].sort((a, b) => a - b);
    if (!rows.length) return;
    if (!(await confirm(`Delete ${rows.length} ${rows.length === 1 ? "element" : "elements"}?`, { okLabel: "Delete" }))) return;
    const refs: R.ItemRef[] = rows.map((r) =>
      type === "stream" ? { id: entries[r].id } : type === "hash" ? { field: items[r].field } : type === "list" ? { index: items[r].index } : { value: items[r].value },
    );
    try {
      await Redis.DeleteItems(connId, db, kv.name, type, refs);
      reload();
    } catch (e) {
      setErr(errorText(e));
    }
  };

  const pushList = async (head: boolean) => {
    if (!detail) return;
    try {
      await Redis.PushList(connId, db, kv.name, [text(detail.value)], head);
      reload();
    } catch (e) {
      setErr(errorText(e));
    }
  };

  const cols = type === "hash" ? ["Field", "Value"] : type === "list" ? ["#", "Value"] : type === "set" ? ["Member"] : type === "zset" ? ["Member", "Score"] : ["ID", "Fields"];

  return (
    <Split
      id="redis-items"
      vertical
      className="redis-items"
      firstSize="62"
      minFirst="20"
      minSecond="15"
      first={
        <div className="redis-items-top">
          <Toolbar>
            <button onClick={() => (setSel(new Set()), setDetail({ row: -1, field: "", value: "", score: "0", id: "*", pairs: [["", ""]] }))} title="Add an element">
              <Plus size={14} /> {type === "stream" ? "Add Entry" : type === "hash" ? "Add Field" : type === "list" ? "Add Item" : "Add Member"}
            </button>
            <button disabled={!sel.size} onClick={remove} title="Delete selected elements">
              <Minus size={14} /> Delete
            </button>
            <span className="spacer" />
            <span className="muted small">
              {rowCount.toLocaleString()} of {kv.length.toLocaleString()} loaded
            </span>
            {cursor && (
              <button className="small-btn" onClick={loadMore}>
                Load more
              </button>
            )}
          </Toolbar>
          <div className="redis-table-wrap">
            <table className="redis-table">
              <thead>
                <tr>
                  {cols.map((c) => (
                    <th key={c}>{c}</th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {type === "stream"
                  ? entries.map((e, i) => (
                      <tr key={e.id} className={sel.has(i) ? "selected" : ""} onClick={(ev) => (ev.metaKey || ev.ctrlKey ? toggle(setSel, i) : open(i))}>
                        <td className="mono">{e.id}</td>
                        <td>{arr(e.fields).map((f) => `${display(f.field!)}=${display(f.value)}`).join("  ")}</td>
                      </tr>
                    ))
                  : items.map((it, i) => (
                      <tr key={i} className={sel.has(i) ? "selected" : ""} onClick={(ev) => (ev.metaKey || ev.ctrlKey ? toggle(setSel, i) : open(i))}>
                        {type === "hash" && <td>{display(it.field!)}</td>}
                        {type === "list" && <td className="mono muted">{it.index}</td>}
                        <td>{clip(display(it.value))}</td>
                        {type === "zset" && <td className="mono">{it.score}</td>}
                      </tr>
                    ))}
              </tbody>
            </table>
          </div>
        </div>
      }
      second={
        <div className="redis-detail" onKeyDown={(e) => (e.metaKey || e.ctrlKey) && e.key === "s" && (e.preventDefault(), save())}>
          {err && <div className="error-banner">{err}</div>}
          {!detail ? (
            <div className="center-hint">Select an element to edit it, or add one.</div>
          ) : type === "stream" ? (
            <div className="form">
              <Field label="ID">
                <input className="mono" value={detail.id} readOnly={detail.row >= 0} onChange={(e) => setDetail({ ...detail, id: e.target.value })} placeholder="* (auto)" />
              </Field>
              {detail.pairs.map(([f, v], i) => (
                <div className="field" key={i}>
                  <label>{i === 0 ? "Fields" : ""}</label>
                  <div className="field-input row">
                    <input placeholder="field" value={f} readOnly={detail.row >= 0} onChange={(e) => setDetail({ ...detail, pairs: detail.pairs.map((p, j) => (j === i ? [e.target.value, p[1]] : p)) })} />
                    <input placeholder="value" value={v} readOnly={detail.row >= 0} onChange={(e) => setDetail({ ...detail, pairs: detail.pairs.map((p, j) => (j === i ? [p[0], e.target.value] : p)) })} />
                  </div>
                </div>
              ))}
              {detail.row < 0 && (
                <div className="field">
                  <label />
                  <div className="field-input row">
                    <button onClick={() => setDetail({ ...detail, pairs: [...detail.pairs, ["", ""]] })}>
                      <Plus size={13} /> Field
                    </button>
                    <span className="spacer" />
                    <button className="primary" onClick={save}>
                      Add Entry
                    </button>
                  </div>
                </div>
              )}
              {detail.row >= 0 && <div className="muted small">Stream entries can't be changed; delete and add a new one instead.</div>}
            </div>
          ) : (
            <div className="redis-detail-edit">
              <div className="row">
                {type === "hash" && <input placeholder="field" value={detail.field} readOnly={!!binaryRow} onChange={(e) => setDetail({ ...detail, field: e.target.value })} />}
                {type === "list" && <span className="muted">{detail.row >= 0 ? `Index ${items[detail.row].index}` : "New item"}</span>}
                {type === "zset" && (
                  <>
                    <span className="muted">Score</span>
                    <input className="narrow" value={detail.score} onChange={(e) => setDetail({ ...detail, score: e.target.value })} />
                  </>
                )}
                <span className="spacer" />
                {binaryRow && <span className="muted small">Binary value — read-only</span>}
                {type === "list" && detail.row < 0 ? (
                  <>
                    <button onClick={() => pushList(true)}>Push to Head</button>
                    <button className="primary" onClick={() => pushList(false)}>
                      Push to Tail
                    </button>
                  </>
                ) : (
                  <button className="primary" disabled={!!binaryRow} onClick={save} title="Save (⌘S)">
                    <Save size={13} /> {detail.row >= 0 ? "Save" : "Add"}
                  </button>
                )}
              </div>
              <textarea className="redis-text" spellCheck={false} readOnly={!!binaryRow} placeholder={type === "hash" ? "value" : "member"} value={detail.value} onChange={(e) => setDetail({ ...detail, value: e.target.value })} />
            </div>
          )}
        </div>
      }
    />
  );
}

function toggle(set: React.Dispatch<React.SetStateAction<Set<number>>>, i: number) {
  set((s) => {
    const n = new Set(s);
    if (n.has(i)) n.delete(i);
    else n.add(i);
    return n;
  });
}

const clip = (s: string) => (s.length > 300 ? s.slice(0, 300) + "…" : s);
