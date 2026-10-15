// History Log: every statement run from query tabs and MongoDB shells, newest first.
import { useVirtualizer } from "@tanstack/react-virtual";
import { Copy, FilePlus2, RefreshCw, Trash2 } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { create } from "zustand";
import { Query, arr, errorText, type HistoryEntry, copyText } from "../api";
import { connById, useApp } from "../store";
import { alert, confirm } from "./Dialogs";
import { openMenu } from "./ContextMenu";
import { newQuery } from "./Sidebar";
import { Split } from "./Split";

// The connection the log is filtered to ("" for all), set by whoever opens it.
const useFilter = create<{ conn: string }>(() => ({ conn: "" }));

export function openHistoryLog(connId = "") {
  useFilter.setState({ conn: connId });
  useApp.getState().openTab({ kind: "history", title: "History Log" });
}

const time = (iso: string) => {
  const d = new Date(iso);
  return isNaN(+d) ? iso : d.toLocaleString(undefined, { year: "numeric", month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", second: "2-digit" });
};

const result = (h: HistoryEntry) => (h.error ? "Error" : h.rows < 0 ? "Result set" : `${h.rows.toLocaleString()} row${h.rows === 1 ? "" : "s"}`);

export function HistoryLog({ active }: { active: boolean }) {
  const conn = useFilter((s) => s.conn);
  const connections = useApp((s) => s.connections);
  const [entries, setEntries] = useState<HistoryEntry[]>([]);
  const [search, setSearch] = useState("");
  const [errorsOnly, setErrorsOnly] = useState(false);
  const [sel, setSel] = useState<number | null>(null);
  const scroller = useRef<HTMLDivElement>(null);

  const load = () =>
    Query.History("")
      .then((h) => setEntries(arr(h)))
      .catch((e) => alert("Couldn't read the history", errorText(e)));
  // Statements run elsewhere show up when the tab is looked at again.
  useEffect(() => {
    if (active) load();
  }, [active]);

  const shown = useMemo(() => {
    const f = search.trim().toLowerCase();
    return entries.filter((h) => (!conn || h.connId === conn) && (!errorsOnly || h.error) && (!f || h.sql.toLowerCase().includes(f) || (h.database ?? "").toLowerCase().includes(f)));
  }, [entries, conn, search, errorsOnly]);
  useEffect(() => setSel(null), [conn, search, errorsOnly]);

  const v = useVirtualizer({ count: shown.length, getScrollElement: () => scroller.current, estimateSize: () => 24, overscan: 20 });
  const picked = sel !== null ? shown[sel] : undefined;

  const openInQuery = (h: HistoryEntry) => {
    const c = connById(h.connId);
    if (!c) return alert("Connection not found", "The connection this statement ran on has been deleted.");
    // A MongoDB script opens in a MongoDB query tab.
    newQuery(h.connId, h.database || undefined, h.schema || undefined, h.sql);
  };
  const clear = async () => {
    if (!(await confirm("Clear the history log?", { message: "Every recorded statement, for all connections, is removed.", okLabel: "Clear" }))) return;
    await Query.ClearHistory().catch((e) => alert("Couldn't clear the history", errorText(e)));
    load();
  };

  return (
    <div className="history-log">
      <div className="toolbar">
        <button disabled={!picked} onClick={() => picked && openInQuery(picked)} title="Open the statement in a new query">
          <FilePlus2 size={14} /> Open in Query
        </button>
        <button disabled={!picked} onClick={() => picked && copyText(picked.sql)}>
          <Copy size={14} /> Copy
        </button>
        <button onClick={load} title="Refresh">
          <RefreshCw size={14} />
        </button>
        <button onClick={clear} disabled={!entries.length}>
          <Trash2 size={14} /> Clear…
        </button>
        <span className="tb-sep" />
        <select value={conn} onChange={(e) => useFilter.setState({ conn: e.target.value })} aria-label="Connection">
          <option value="">All connections</option>
          {connections.map((c) => (
            <option key={c.id} value={c.id}>
              {c.name}
            </option>
          ))}
        </select>
        <label className="check">
          <input type="checkbox" checked={errorsOnly} onChange={(e) => setErrorsOnly(e.target.checked)} /> Errors only
        </label>
        <span className="spacer" />
        <span className="muted">{shown.length.toLocaleString()} statement{shown.length === 1 ? "" : "s"}</span>
        <input className="search" placeholder="Search" value={search} onChange={(e) => setSearch(e.target.value)} />
      </div>
      <Split
        id="history-log"
        vertical
        firstSize="65"
        minFirst="20"
        minSecond="10"
        first={
          <div className="hist-list">
            <div className="hist-row hist-head">
              <span>Time</span>
              <span>Connection</span>
              <span>Database</span>
              <span className="num">Duration</span>
              <span>Result</span>
              <span>SQL</span>
            </div>
            <div className="hist-body" ref={scroller} tabIndex={0}
              onKeyDown={(e) => {
                if (e.key === "ArrowDown" || e.key === "ArrowUp") {
                  e.preventDefault();
                  const next = Math.max(0, Math.min(shown.length - 1, (sel ?? -1) + (e.key === "ArrowDown" ? 1 : -1)));
                  setSel(next);
                  v.scrollToIndex(next);
                } else if (e.key === "Enter" && picked) openInQuery(picked);
              }}
            >
              <div style={{ height: v.getTotalSize(), position: "relative" }}>
                {v.getVirtualItems().map((vi) => {
                  const h = shown[vi.index];
                  return (
                    <div
                      key={vi.key}
                      className={"hist-row" + (sel === vi.index ? " selected" : "") + (h.error ? " failed" : "")}
                      style={{ top: vi.start }}
                      onClick={() => setSel(vi.index)}
                      onDoubleClick={() => openInQuery(h)}
                      onContextMenu={(e) => {
                        setSel(vi.index);
                        openMenu(e, [
                          { label: "Open in Query", onClick: () => openInQuery(h) },
                          { label: "Copy SQL", onClick: () => copyText(h.sql) },
                        ]);
                      }}
                    >
                      <span>{time(h.time)}</span>
                      <span>{connById(h.connId)?.name ?? "(deleted)"}</span>
                      <span>{[h.database, h.schema].filter(Boolean).join(".")}</span>
                      <span className="num">{h.durationMs} ms</span>
                      <span className={h.error ? "err" : "muted"}>{result(h)}</span>
                      <span className="hist-sql">{h.sql.replace(/\s+/g, " ")}</span>
                    </div>
                  );
                })}
              </div>
              {!shown.length && <div className="center-hint">{entries.length ? "No matching statements." : "Statements you run appear here."}</div>}
            </div>
          </div>
        }
        second={
          <div className="hist-detail">
            {picked ? (
              <>
                {picked.error && <div className="error-banner">{picked.error}</div>}
                <pre>{picked.sql}</pre>
              </>
            ) : (
              <div className="center-hint">Select a statement to see it in full.</div>
            )}
          </div>
        }
      />
    </div>
  );
}
