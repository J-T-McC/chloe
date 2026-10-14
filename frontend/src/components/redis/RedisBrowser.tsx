// Redis key browser for one logical database: a scanned key list (folders split on
// ":" or a flat list) next to the selected key's value editor.
import { useVirtualizer } from "@tanstack/react-virtual";
import { ChevronDown, ChevronRight, Folder, KeyRound, ListTree, Plus, RefreshCw, Search, TerminalSquare, Trash2 } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { R, Redis, arr, errorText } from "../../api";
import { KeyRow, TYPES, TYPE_LABEL, formatTTL, keyId, keyRows, toPattern } from "../../redis/keys";
import { connById } from "../../store";
import { openMenu } from "../ContextMenu";
import { alert, confirm } from "../Dialogs";
import { Split } from "../Split";
import { KeyEditor } from "./KeyEditor";
import { NewKeyDialog } from "./NewKeyDialog";
import { openRedisConsole } from "./actions";

const BATCH = 1000;
const AUTO_LIMIT = 20000; // keys loaded before asking to load more

export function RedisBrowser({ connId, db }: { connId: string; db: number }) {
  const [keys, setKeys] = useState<R.Key[]>([]);
  const [cursor, setCursor] = useState("0");
  const [done, setDone] = useState(false);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [search, setSearch] = useState("");
  const [pattern, setPattern] = useState("*");
  const [type, setType] = useState("");
  const [tree, setTree] = useState(true);
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [current, setCurrent] = useState<R.Key | null>(null);
  const [creating, setCreating] = useState(false);
  const scanId = useRef(0);
  const scroller = useRef<HTMLDivElement>(null);
  const sep = ":";

  // Scan from the start, continuing in batches up to AUTO_LIMIT keys.
  const scan = useCallback(
    async (from: string, append: boolean, limit = AUTO_LIMIT) => {
      const id = ++scanId.current;
      setLoading(true);
      setError("");
      let cur = from;
      let loaded = 0;
      if (!append) setKeys([]);
      try {
        do {
          const res = await Redis.ScanKeys(connId, db, cur, pattern, type, BATCH);
          if (id !== scanId.current) return; // superseded by a newer scan
          const batch = arr(res.keys);
          loaded += batch.length;
          cur = res.cursor;
          // SCAN may return a key more than once.
          setKeys((k) => [...k, ...batch].filter(uniq()));
        } while (cur !== "0" && loaded < limit);
        setCursor(cur);
        setDone(cur === "0");
      } catch (e) {
        if (id === scanId.current) setError(errorText(e));
      } finally {
        if (id === scanId.current) setLoading(false);
      }
    },
    [connId, db, pattern, type],
  );

  useEffect(() => {
    setSelected(new Set());
    scan("0", false);
  }, [scan]);

  const reload = () => scan("0", false);

  const rows = useMemo(() => keyRows(keys, sep, expanded, tree), [keys, expanded, tree]);
  const virt = useVirtualizer({ count: rows.length, getScrollElement: () => scroller.current, estimateSize: () => 24, overscan: 20 });

  const toggleFolder = (id: string) =>
    setExpanded((e) => {
      const n = new Set(e);
      if (n.has(id)) n.delete(id);
      else n.add(id);
      return n;
    });

  const click = (r: KeyRow, e: React.MouseEvent) => {
    if (r.kind === "folder") return toggleFolder(r.id);
    if (e.metaKey || e.ctrlKey) {
      setSelected((s) => {
        const n = new Set(s);
        if (n.has(r.id)) n.delete(r.id);
        else n.add(r.id);
        return n;
      });
      return;
    }
    if (e.shiftKey && current) {
      const keyRowsOnly = rows.filter((x) => x.kind === "key");
      const a = keyRowsOnly.findIndex((x) => x.id === keyId(current));
      const b = keyRowsOnly.findIndex((x) => x.id === r.id);
      if (a >= 0 && b >= 0) {
        setSelected(new Set(keyRowsOnly.slice(Math.min(a, b), Math.max(a, b) + 1).map((x) => x.id)));
        return;
      }
    }
    setSelected(new Set([r.id]));
    setCurrent(r.key!);
  };

  const keysIn = (folderId: string) => keys.filter((k) => keyId(k).startsWith(folderId));

  const deleteKeys = async (victims: R.Key[]) => {
    if (!victims.length) return;
    const what = victims.length === 1 ? `key ${victims[0].name.text}` : `${victims.length.toLocaleString()} keys`;
    if (!(await confirm(`Delete ${what}?`, { okLabel: "Delete" }))) return;
    try {
      // Unlink in chunks so huge selections don't build one giant command.
      for (let i = 0; i < victims.length; i += 500) await Redis.DeleteKeys(connId, db, victims.slice(i, i + 500).map((k) => k.name));
      const gone = new Set(victims.map(keyId));
      setKeys((ks) => ks.filter((k) => !gone.has(keyId(k))));
      setSelected(new Set());
      if (current && gone.has(keyId(current))) setCurrent(null);
    } catch (e) {
      alert("Delete failed", errorText(e));
    }
  };

  const selectedKeys = () => keys.filter((k) => selected.has(keyId(k)));

  const rowMenu = (r: KeyRow, e: React.MouseEvent) => {
    if (r.kind === "key" && !selected.has(r.id)) {
      setSelected(new Set([r.id]));
      setCurrent(r.key!);
    }
    const victims = r.kind === "folder" ? keysIn(r.id) : selected.has(r.id) ? selectedKeys() : [r.key!];
    openMenu(e, [
      ...(r.kind === "folder"
        ? [
            { label: expanded.has(r.id) ? "Collapse" : "Expand", onClick: () => toggleFolder(r.id) },
            { label: `Filter to ${r.id}*`, onClick: () => (setSearch(r.id + "*"), setPattern(r.id + "*")) },
          ]
        : [{ label: "Copy Key Name", onClick: () => navigator.clipboard.writeText(victims.map((k) => k.name.text).join("\n")) }]),
      { label: "New Key…", onClick: () => setCreating(true) },
      { separator: true },
      { label: r.kind === "folder" ? `Delete ${victims.length.toLocaleString()} Loaded Keys…` : victims.length > 1 ? `Delete ${victims.length} Keys` : "Delete Key", onClick: () => deleteKeys(victims) },
      { separator: true },
      { label: "Refresh", shortcut: "⌘R", onClick: reload },
    ]);
  };

  return (
    <Split
      id="redis-browser"
      className="redis-browser"
      firstSize={340}
      minFirst={220}
      maxFirst="70"
      first={
        <div
          className="redis-keys"
          tabIndex={0}
          onKeyDown={(e) => {
            const meta = e.metaKey || e.ctrlKey;
            if (meta && e.key === "r") (e.preventDefault(), reload());
            else if (meta && e.key === "n") (e.preventDefault(), setCreating(true));
            else if ((e.key === "Delete" || e.key === "Backspace") && selected.size && (e.target as HTMLElement).tagName !== "INPUT") (e.preventDefault(), deleteKeys(selectedKeys()));
          }}
        >
          <div className="toolbar">
            <button onClick={() => setCreating(true)} title="New key (⌘N)">
              <Plus size={14} /> New Key
            </button>
            <button disabled={!selected.size} onClick={() => deleteKeys(selectedKeys())} title="Delete selected keys">
              <Trash2 size={14} />
            </button>
            <button onClick={reload} title="Refresh (⌘R)">
              <RefreshCw size={14} className={loading ? "spin" : ""} />
            </button>
            <button className={tree ? "on" : ""} onClick={() => setTree((t) => !t)} title={tree ? "Show a flat list" : `Group keys by "${sep}"`}>
              <ListTree size={14} />
            </button>
            <span className="spacer" />
            <button onClick={() => openRedisConsole(connId, db)} title="Open a console on this database">
              <TerminalSquare size={14} />
            </button>
          </div>
          <div className="redis-filter">
            <div className="erd-search">
              <Search size={13} />
              <input
                placeholder="Filter keys (user:* or text)"
                value={search}
                onChange={(e) => setSearch(e.target.value)}
                onKeyDown={(e) => e.key === "Enter" && setPattern(toPattern(search))}
                onBlur={() => setPattern(toPattern(search))}
              />
            </div>
            <select value={type} onChange={(e) => setType(e.target.value)} title="Key type">
              <option value="">All types</option>
              {TYPES.map((t) => (
                <option key={t} value={t}>
                  {TYPE_LABEL[t]}
                </option>
              ))}
            </select>
          </div>
          {error && <div className="error-banner">{error}</div>}
          <div className="redis-key-list" ref={scroller}>
            <div style={{ height: virt.getTotalSize(), position: "relative" }}>
              {virt.getVirtualItems().map((vi) => {
                const r = rows[vi.index];
                return (
                  <div
                    key={r.id}
                    className={"redis-key-row" + (selected.has(r.id) ? " selected" : "") + (current && r.id === keyId(current) ? " current" : "")}
                    style={{ top: vi.start, height: vi.size, paddingLeft: 6 + r.depth * 14 }}
                    onClick={(e) => click(r, e)}
                    onContextMenu={(e) => rowMenu(r, e)}
                    title={r.kind === "key" ? r.key!.name.text : r.id + "*"}
                  >
                    {r.kind === "folder" ? (
                      <>
                        {expanded.has(r.id) ? <ChevronDown size={12} /> : <ChevronRight size={12} />}
                        <Folder size={13} className="kind-icon kind-group" />
                        <span className="redis-key-name">{r.label}</span>
                        <span className="muted">{r.count.toLocaleString()}</span>
                      </>
                    ) : (
                      <>
                        <span style={{ width: 12 }} />
                        <span className={`redis-type t-${r.key!.type}`}>{typeAbbr(r.key!.type)}</span>
                        <span className="redis-key-name">{r.label}</span>
                        {r.key!.ttl >= 0 && <span className="muted small">{formatTTL(r.key!.ttl)}</span>}
                      </>
                    )}
                  </div>
                );
              })}
            </div>
            {!loading && !keys.length && !error && <div className="center-hint">{pattern === "*" && !type ? "This database is empty." : "No matching keys."}</div>}
          </div>
          <div className="statusbar">
            <span>
              {keys.length.toLocaleString()} key{keys.length === 1 ? "" : "s"}
              {selected.size > 1 ? ` · ${selected.size} selected` : ""}
            </span>
            <span className="spacer" />
            {loading ? (
              <span className="muted">Scanning…</span>
            ) : !done ? (
              <button className="small-btn" onClick={() => scan(cursor, true)}>
                Load more
              </button>
            ) : (
              <span className="muted">db{db}</span>
            )}
          </div>
          {creating && (
            <NewKeyDialog
              connId={connId}
              db={db}
              prefix={current && tree ? display0(current).slice(0, display0(current).lastIndexOf(sep) + 1) : ""}
              onClose={(created) => {
                setCreating(false);
                if (created) {
                  setKeys((ks) => [...ks.filter((k) => keyId(k) !== keyId(created)), created]);
                  setSelected(new Set([keyId(created)]));
                  setCurrent(created);
                }
              }}
            />
          )}
        </div>
      }
      second={
        current ? (
          <KeyEditor
            key={`${db}:${keyId(current)}`}
            connId={connId}
            db={db}
            redisKey={current}
            onDeleted={() => {
              setKeys((ks) => ks.filter((k) => keyId(k) !== keyId(current)));
              setCurrent(null);
            }}
            onRenamed={(to) => {
              const next = { ...current, name: to };
              setKeys((ks) => ks.map((k) => (keyId(k) === keyId(current) ? next : k)));
              setSelected(new Set([keyId(next)]));
              setCurrent(next);
            }}
            onChanged={(patch) => setKeys((ks) => ks.map((k) => (keyId(k) === keyId(current) ? { ...k, ...patch } : k)))}
          />
        ) : (
          <div className="center-hint">
            <KeyRound size={22} />
            <div>Select a key to see its value.</div>
            <div className="muted small">{connById(connId)?.name} · db{db}</div>
          </div>
        )
      }
    />
  );
}

const display0 = (k: R.Key) => k.name.text;

const uniq = () => {
  const seen = new Set<string>();
  return (k: R.Key) => {
    const id = keyId(k);
    if (seen.has(id)) return false;
    seen.add(id);
    return true;
  };
};

function typeAbbr(t: string) {
  return ({ string: "STR", hash: "HASH", list: "LIST", set: "SET", zset: "ZSET", stream: "STRM" } as Record<string, string>)[t] ?? t.slice(0, 4).toUpperCase();
}
