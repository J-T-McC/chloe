import { CircleStop, Pin, RefreshCw, Skull } from "lucide-react";
import { useCallback, useEffect, useMemo, useState } from "react";
import { Monitor, Process, ServerVar, arr, errorText, copyText } from "../api";
import { Tab, useApp } from "../store";
import { openMenu } from "./ContextMenu";
import { confirm } from "./Dialogs";
import { ConnIcon } from "./icons";
import { Split } from "./Split";
import { Toolbar } from "./Toolbar";

type Page = "processes" | "variables" | "status";
const INTERVALS: Array<[number, string]> = [
  [0, "No Auto Refresh"],
  [1, "Every 1 s"],
  [2, "Every 2 s"],
  [5, "Every 5 s"],
  [10, "Every 10 s"],
  [30, "Every 30 s"],
];

interface Row extends Process {
  connId: string;
  connName: string;
}

export function ServerMonitor({ tab }: { tab: Tab }) {
  const connections = useApp((s) => s.connections);
  const open = useApp((s) => s.open);
  const openConns = connections.filter((c) => open[c.id]);
  const [selected, setSelected] = useState<string[]>(tab.connId ? [tab.connId] : []);
  const [page, setPage] = useState<Page>("processes");
  const [interval, setIntervalSec] = useState(2);
  const [search, setSearch] = useState("");
  const [rows, setRows] = useState<Row[]>([]);
  const [vars, setVars] = useState<Array<ServerVar & { connName: string }>>([]);
  const [errors, setErrors] = useState<string[]>([]);
  const [picked, setPicked] = useState<Set<string>>(new Set());
  const [pinned, setPinned] = useState<Set<string>>(new Set());
  const [loading, setLoading] = useState(false);

  const active = selected.filter((id) => open[id]);
  const multi = active.length > 1;

  const load = useCallback(async () => {
    setLoading(true);
    const errs: string[] = [];
    const name = (id: string) => connections.find((c) => c.id === id)?.name ?? id;
    if (page === "processes") {
      const all = await Promise.all(
        active.map((id) =>
          Monitor.Processes(id)
            .then((ps) => arr(ps).map((p) => ({ ...p, connId: id, connName: name(id) })))
            .catch((e) => (errs.push(`${name(id)}: ${errorText(e)}`), [] as Row[])),
        ),
      );
      setRows(all.flat());
    } else {
      const fn = page === "variables" ? Monitor.Variables : Monitor.Status;
      const all = await Promise.all(
        active.map((id) =>
          fn(id)
            .then((vs) => arr(vs).map((v) => ({ ...v, connName: name(id) })))
            .catch((e) => (errs.push(`${name(id)}: ${errorText(e)}`), [])),
        ),
      );
      setVars(all.flat());
    }
    setErrors(errs);
    setLoading(false);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [page, active.join(","), connections]);

  useEffect(() => {
    load();
  }, [load]);

  // Auto refresh applies to the process list; variables and status refresh on demand.
  useEffect(() => {
    if (!interval || page !== "processes") return;
    const t = setInterval(load, interval * 1000);
    return () => clearInterval(t);
  }, [interval, page, load]);

  const key = (r: Row) => `${r.connId}:${r.id}`;
  const f = search.toLowerCase();
  const shownRows = useMemo(() => {
    const list = rows.filter((r) => !f || [r.id, r.user, r.host, r.database, r.application, r.command, r.state, r.query].some((x) => x?.toLowerCase().includes(f)));
    // Pinned ("always on top") rows first, then the longest-running.
    return list.sort((a, b) => Number(pinned.has(key(b))) - Number(pinned.has(key(a))) || b.timeSec - a.timeSec);
  }, [rows, f, pinned]);
  const shownVars = useMemo(() => vars.filter((v) => !f || (v.name + " " + v.value + " " + (v.description ?? "")).toLowerCase().includes(f)), [vars, f]);

  const pickedRows = shownRows.filter((r) => picked.has(key(r)));

  const kill = async (queryOnly: boolean) => {
    if (!pickedRows.length) return;
    const own = pickedRows.some((r) => r.self);
    const ok = await confirm(queryOnly ? "Cancel the running query?" : `End ${pickedRows.length} process${pickedRows.length > 1 ? "es" : ""}?`, {
      message: (own ? "This includes a session used by Chloe itself. " : "") + pickedRows.map((r) => `${r.connName} #${r.id} (${r.user})`).join(", "),
      danger: !queryOnly,
      okLabel: queryOnly ? "Cancel Query" : "End Process",
    });
    if (!ok) return;
    const errs: string[] = [];
    for (const r of pickedRows) await Monitor.KillProcess(r.connId, r.id, queryOnly).catch((e) => errs.push(`#${r.id}: ${errorText(e)}`));
    setErrors(errs);
    setPicked(new Set());
    load();
  };

  const toggle = (set: Set<string>, k: string) => {
    const n = new Set(set);
    if (n.has(k)) n.delete(k);
    else n.add(k);
    return n;
  };

  return (
    <Split
      id="monitor"
      className="monitor"
      firstSize={210}
      minFirst={140}
      minSecond={400}
      first={
      <div className="monitor-conns">
        <div className="pane-title">Connections</div>
        {openConns.length === 0 && <div className="empty-hint">No open connections.</div>}
        {openConns.map((c) => (
          <label key={c.id} className="check monitor-conn">
            <input type="checkbox" checked={selected.includes(c.id)} onChange={(e) => setSelected((s) => (e.target.checked ? [...s, c.id] : s.filter((x) => x !== c.id)))} />
            <ConnIcon type={c.type} preset={c.preset} />
            {c.name}
          </label>
        ))}
      </div>
      }
      second={
      <div className="monitor-main">
        <Toolbar>
          <div className="seg">
            {(["processes", "variables", "status"] as Page[]).map((p) => (
              <button key={p} className={page === p ? "active" : ""} onClick={() => setPage(p)}>
                {p === "processes" ? "Process List" : p === "variables" ? "Variables" : "Status"}
              </button>
            ))}
          </div>
          <span className="tb-sep" />
          <button title="Refresh" onClick={load}>
            <RefreshCw size={14} className={loading ? "spin" : ""} />
          </button>
          {page === "processes" && (
            <>
              <select value={interval} onChange={(e) => setIntervalSec(Number(e.target.value))}>
                {INTERVALS.map(([n, l]) => (
                  <option key={n} value={n}>
                    {l}
                  </option>
                ))}
              </select>
              <button disabled={!pickedRows.length} onClick={() => kill(true)} title="Cancel the running query, keep the session">
                <CircleStop size={14} /> Cancel Query
              </button>
              <button disabled={!pickedRows.length} onClick={() => kill(false)} title="Terminate the session">
                <Skull size={14} /> End Process
              </button>
            </>
          )}
          <span className="spacer" />
          <input className="search" placeholder="Search" value={search} onChange={(e) => setSearch(e.target.value)} />
        </Toolbar>
        {!!errors.length && <div className="error-banner">{errors.join("\n")}</div>}
        <div className="monitor-table">
          {page === "processes" ? (
            <table>
              <thead>
                <tr>
                  <th />
                  {multi && <th>Connection</th>}
                  <th className="num">ID</th>
                  <th>User</th>
                  <th>Host</th>
                  <th>Database</th>
                  <th>Command</th>
                  <th className="num">Time</th>
                  <th>State</th>
                  <th>Query</th>
                </tr>
              </thead>
              <tbody>
                {shownRows.map((r) => {
                  const k = key(r);
                  return (
                    <tr
                      key={k}
                      className={(picked.has(k) ? "selected" : "") + (r.self ? " self" : "")}
                      onClick={(e) => setPicked((s) => (e.metaKey || e.shiftKey ? toggle(s, k) : new Set([k])))}
                      onContextMenu={(e) => {
                        if (!picked.has(k)) setPicked(new Set([k]));
                        openMenu(e, [
                          { label: "End Process…", danger: true, onClick: () => kill(false) },
                          { label: "Cancel Query…", onClick: () => kill(true) },
                          { separator: true },
                          { label: pinned.has(k) ? "Unpin" : "Always On Top", onClick: () => setPinned((s) => toggle(s, k)) },
                          { label: "Cancel All Always On Top", disabled: !pinned.size, onClick: () => setPinned(new Set()) },
                          { separator: true },
                          { label: "Copy Query", disabled: !r.query, onClick: () => copyText(r.query) },
                        ]);
                      }}
                      title={r.self ? "A session opened by Chloe" : undefined}
                    >
                      <td className="pin">{pinned.has(k) && <Pin size={11} />}</td>
                      {multi && <td>{r.connName}</td>}
                      <td className="num">{r.id}</td>
                      <td>{r.user}</td>
                      <td>{r.host}</td>
                      <td>{r.database}</td>
                      <td>{r.application ? `${r.command} · ${r.application}` : r.command}</td>
                      <td className="num">{formatDuration(r.timeSec)}</td>
                      <td>{r.state}</td>
                      <td className="query" title={r.query}>
                        {r.query}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          ) : (
            <table>
              <thead>
                <tr>
                  {multi && <th>Connection</th>}
                  <th>Name</th>
                  <th>Value</th>
                  <th>Description</th>
                </tr>
              </thead>
              <tbody>
                {shownVars.map((v, i) => (
                  <tr key={i}>
                    {multi && <td>{v.connName}</td>}
                    <td className="mono">{v.name}</td>
                    <td className="mono value" title={v.value}>
                      {v.value}
                    </td>
                    <td className="muted">{v.description}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
        <div className="statusbar">
          <span>
            {page === "processes" ? `${shownRows.length} process${shownRows.length === 1 ? "" : "es"}` : `${shownVars.length} ${page === "variables" ? "variables" : "values"}`}
          </span>
          <span className="spacer" />
          {page === "processes" && <span className="muted">Highlighted rows are Chloe's own sessions</span>}
        </div>
      </div>
      }
    />
  );
}

function formatDuration(s: number) {
  if (s < 60) return `${s}s`;
  if (s < 3600) return `${Math.floor(s / 60)}m ${s % 60}s`;
  if (s < 86400) return `${Math.floor(s / 3600)}h ${Math.floor((s % 3600) / 60)}m`;
  return `${Math.floor(s / 86400)}d ${Math.floor((s % 86400) / 3600)}h`;
}
