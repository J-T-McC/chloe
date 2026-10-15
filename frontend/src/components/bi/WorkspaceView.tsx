// A BI workspace tab: its data sources and charts in a detail or icon view, with
// quick filters and a properties pane (General, Using, Used By), like Navicat's
// workspace window.
import { ChartColumn, Database, LayoutGrid, List, Plus, RefreshCw } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { B, BI, arr, errorText } from "../../api";
import { Tab, connById, useApp } from "../../store";
import { MenuItem, openMenu } from "../ContextMenu";
import { alert, confirm, prompt } from "../Dialogs";
import { createdByLabel } from "../model/ModelsList";
import { Split } from "../Split";
import { biChanged, openObject, useBIVersion, workspaceTitle } from "./actions";
import { ChartTypeIcon } from "./icons";

type Filter = "all" | "source" | "chart";

interface Item {
  kind: "source" | "chart";
  id: string;
  name: string;
  chartType?: string;
  source?: string;
  createdBy?: string;
  created?: string;
  modified?: string;
  refreshed?: string;
  remarks?: string;
}

const pref = (k: string, d: string) => {
  try {
    return localStorage.getItem(k) ?? d;
  } catch {
    return d;
  }
};
const setPref = (k: string, v: string) => {
  try {
    localStorage.setItem(k, v);
  } catch {
    /* ignore */
  }
};

export function useWorkspace(path: string) {
  const version = useBIVersion((s) => s.ws[path] ?? 0);
  const [w, setW] = useState<B.Workspace | null>(null);
  const [error, setError] = useState("");
  useEffect(() => {
    BI.Open(path)
      .then((x) => (setW(x), setError("")))
      .catch((e) => setError(errorText(e)));
  }, [path, version]);
  return { w, error };
}

const when = (s?: string) => (s ? new Date(s).toLocaleString() : "");

export function connLabel(s?: B.DataSource | null) {
  if (!s) return "";
  const c = connById(s.connId);
  return [c ? c.name : "(deleted connection)", s.database, s.schema].filter(Boolean).join(" › ");
}

export function WorkspaceView({ tab }: { tab: Tab }) {
  const path = tab.biPath!;
  const { w, error } = useWorkspace(path);
  const [filter, setFilter] = useState<Filter>("all");
  const [view, setView] = useState(() => pref("chloe:bi:view", "detail"));
  const [picked, setPicked] = useState("");
  const [search, setSearch] = useState("");
  const [sort, setSort] = useState<{ key: keyof Item; desc: boolean }>({ key: "name", desc: false });

  useEffect(() => {
    if (w) useApp.getState().updateTab(tab.id, { title: workspaceTitle(w.name) });
  }, [w?.name]); // eslint-disable-line react-hooks/exhaustive-deps

  const sources = arr(w?.dataSources);
  const charts = arr(w?.charts);
  const items: Item[] = useMemo(() => {
    const out: Item[] = [
      ...sources.map((s) => ({ kind: "source" as const, id: s.id, name: s.name, createdBy: s.createdBy, created: s.created, modified: s.modified, refreshed: s.mode === "archive" ? s.refreshedAt : "", remarks: s.remarks })),
      ...charts.map((c) => ({ kind: "chart" as const, id: c.id, name: c.name, chartType: c.type, source: sources.find((s) => s.id === c.dataSource)?.name ?? "(deleted)", createdBy: c.createdBy, created: c.created, modified: c.modified, remarks: c.remarks })),
    ];
    const f = search.toLowerCase();
    return out
      .filter((x) => (filter === "all" || x.kind === filter) && (!f || x.name.toLowerCase().includes(f)))
      .sort((a, b) => String(a[sort.key] ?? "").localeCompare(String(b[sort.key] ?? ""), undefined, { numeric: true }) * (sort.desc ? -1 : 1));
  }, [w, filter, search, sort]); // eslint-disable-line react-hooks/exhaustive-deps
  const sel = items.find((x) => x.id === picked) ?? null;

  const open = (x: Item) => openObject(path, x.kind, x.id, x.name);
  const newChart = () => {
    if (!sources.length) return alert("New Chart", "Add a data source first: charts are built on one.");
    openObject(path, "chart", "", "");
  };
  const run = async (what: string, fn: () => Promise<unknown>) => {
    try {
      await fn();
    } catch (e) {
      alert(what, errorText(e));
    }
    biChanged(path);
  };
  const rename = async (x: Item) => {
    const name = await prompt(x.kind === "source" ? "Rename Data Source" : "Rename Chart", { message: "New name", defaultValue: x.name });
    if (!name?.trim() || name.trim() === x.name) return;
    run("Couldn't rename it", async () => {
      if (x.kind === "chart") await BI.SaveChart(path, { ...charts.find((c) => c.id === x.id)!, name: name.trim() });
      else await BI.SaveSource(path, { ...sources.find((s) => s.id === x.id)!, name: name.trim() });
      const app = useApp.getState();
      for (const t of app.tabs) if (t.biPath === path && t.biId === x.id) app.updateTab(t.id, { title: name.trim() });
    });
  };
  const remove = async (x: Item) => {
    const users = x.kind === "source" ? charts.filter((c) => c.dataSource === x.id).map((c) => c.name) : [];
    const msg = users.length ? `Delete the data source "${x.name}"? These charts use it and will show that it's gone: ${users.join(", ")}.` : `Delete "${x.name}"? This can't be undone.`;
    if (!(await confirm(x.kind === "source" ? "Delete Data Source" : "Delete Chart", { message: msg, okLabel: "Delete" }))) return;
    run("Couldn't delete it", async () => {
      await BI.DeleteObject(path, x.id);
      const app = useApp.getState();
      app.closeTabs(app.tabs.filter((t) => t.biPath === path && t.biId === x.id).map((t) => t.id));
    });
  };
  const menu = (x: Item): MenuItem[] => [
    { label: x.kind === "source" ? "Design Data Source" : "Design Chart", onClick: () => open(x) },
    { label: "Rename…", onClick: () => rename(x) },
    { label: "Delete…", onClick: () => remove(x) },
    ...(x.kind === "source" && sources.find((s) => s.id === x.id)?.mode === "archive"
      ? [{ separator: true }, { label: "Refresh Data", onClick: () => run("Couldn't refresh the data", () => BI.RefreshSource(path, x.id)) }]
      : []),
  ];
  const th = (key: keyof Item, label: string) => (
    <th onClick={() => setSort((s) => ({ key, desc: s.key === key ? !s.desc : false }))} className="sortable">
      {label}
      {sort.key === key ? (sort.desc ? " ▾" : " ▴") : ""}
    </th>
  );

  return (
    <div className="objects-pane bi-workspace">
      <div className="toolbar">
        <button onClick={() => openObject(path, "source", "", "")}>
          <Plus size={14} /> New Data Source
        </button>
        <button onClick={newChart}>
          <Plus size={14} /> New Chart
        </button>
        <span className="tb-sep" />
        <label className="tb-label">
          Theme{" "}
          <select aria-label="Theme" value={w?.theme || "light"} onChange={(e) => run("Couldn't change the theme", () => BI.SetTheme(path, e.target.value))}>
            <option value="light">Light</option>
            <option value="dark">Dark</option>
          </select>
        </label>
        <span className="tb-sep" />
        <div className="seg">
          {(["all", "source", "chart"] as Filter[]).map((f) => (
            <button key={f} className={filter === f ? "on" : ""} onClick={() => setFilter(f)}>
              {f === "all" ? "All" : f === "source" ? "Data Source" : "Chart"}
            </button>
          ))}
        </div>
        <span className="spacer" />
        <button title="Detail view" className={view === "detail" ? "on" : ""} onClick={() => (setView("detail"), setPref("chloe:bi:view", "detail"))}>
          <List size={14} />
        </button>
        <button title="Icon view" className={view === "icon" ? "on" : ""} onClick={() => (setView("icon"), setPref("chloe:bi:view", "icon"))}>
          <LayoutGrid size={14} />
        </button>
        <button title="Refresh" onClick={() => biChanged(path)}>
          <RefreshCw size={14} />
        </button>
        <input className="search" placeholder="Filter" value={search} onChange={(e) => setSearch(e.target.value)} />
      </div>
      {error && <div className="error-banner">{error}</div>}
      <Split
        id="bi-workspace-props"
        className="objects-split"
        secondSize={300}
        minFirst={260}
        minSecond={180}
        maxSecond="60"
        first={
          <div className="object-list" tabIndex={0} onMouseDown={(e) => e.target === e.currentTarget && setPicked("")} onKeyDown={(e) => e.key === "Enter" && sel && open(sel)}>
            {view === "detail" ? (
              <table>
                <thead>
                  <tr>
                    {th("name", "Name")}
                    {th("kind", "Type")}
                    {th("chartType", "Chart Type")}
                    {th("source", "Data Source")}
                    {th("createdBy", "Created By")}
                    {th("modified", "Modified")}
                    {th("refreshed", "Data Refresh Time")}
                  </tr>
                </thead>
                <tbody>
                  {items.map((x) => (
                    <tr key={x.id} className={x.id === picked ? "selected" : ""} onClick={() => setPicked(x.id)} onDoubleClick={() => open(x)} onContextMenu={(e) => (setPicked(x.id), openMenu(e, menu(x)))}>
                      <td>
                        {x.kind === "source" ? <Database size={13} /> : <ChartTypeIcon type={x.chartType!} size={13} />} {x.name}
                      </td>
                      <td>{x.kind === "source" ? "Data Source" : "Chart"}</td>
                      <td>{x.chartType ? chartTypeName(x.chartType) : ""}</td>
                      <td>{x.source ?? ""}</td>
                      <td className="muted">{createdByLabel(x.createdBy)}</td>
                      <td>{when(x.modified)}</td>
                      <td>{when(x.refreshed)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            ) : (
              <div className="bi-icons">
                {items.map((x) => (
                  <div key={x.id} className={"bi-icon" + (x.id === picked ? " selected" : "")} onClick={() => setPicked(x.id)} onDoubleClick={() => open(x)} onContextMenu={(e) => (setPicked(x.id), openMenu(e, menu(x)))}>
                    <div className="bi-icon-art">{x.kind === "source" ? <Database size={34} strokeWidth={1.4} /> : <ChartTypeIcon type={x.chartType!} size={34} strokeWidth={1.4} />}</div>
                    <div className="bi-icon-name">{x.name}</div>
                  </div>
                ))}
              </div>
            )}
            {w && !items.length && (
              <div className="center-hint">{sources.length + charts.length ? "Nothing matches the filter." : "This workspace is empty. Start with New Data Source: a table, view or query that charts are built on."}</div>
            )}
          </div>
        }
        second={<Properties w={w} sel={sel} path={path} />}
      />
    </div>
  );
}

export const CHART_TYPE_NAMES: Record<string, string> = {
  bar: "Bar",
  stacked_bar: "Stacked Bar",
  hbar: "Horizontal Bar",
  stacked_hbar: "Horizontal Stacked Bar",
  line: "Line",
  area: "Area",
  stacked_area: "Stacked Area",
  bar_line: "Bar and Line",
  stacked_bar_line: "Stacked Bar and Line",
  pie: "Pie",
  donut: "Donut",
  value: "Value",
  table: "Table",
};
export const chartTypeName = (t: string) => CHART_TYPE_NAMES[t] ?? t;

function Properties({ w, sel, path }: { w: B.Workspace | null; sel: Item | null; path: string }) {
  const [tab, setTab] = useState<"general" | "using" | "usedby">("general");
  if (!w) return <div className="info-panel empty" />;
  const sources = arr(w.dataSources);
  const charts = arr(w.charts);
  const src = sel?.kind === "source" ? sources.find((s) => s.id === sel.id) : sel ? sources.find((s) => s.id === charts.find((c) => c.id === sel.id)?.dataSource) : null;
  const using = sel?.kind === "chart" ? (src ? [{ kind: "source" as const, id: src.id, name: src.name }] : []) : [];
  const usedBy = sel?.kind === "source" ? charts.filter((c) => c.dataSource === sel.id).map((c) => ({ kind: "chart" as const, id: c.id, name: c.name, type: c.type })) : [];
  return (
    <div className="info-panel bi-props">
      <div className="seg-tabs compact">
        <button className={tab === "general" ? "active" : ""} onClick={() => setTab("general")}>
          General
        </button>
        <button className={tab === "using" ? "active" : ""} onClick={() => setTab("using")} disabled={!sel}>
          Using
        </button>
        <button className={tab === "usedby" ? "active" : ""} onClick={() => setTab("usedby")} disabled={!sel}>
          Used By
        </button>
      </div>
      {tab === "general" || !sel ? (
        sel ? (
          <>
            <div className="info-head">
              {sel.kind === "source" ? <Database size={16} /> : <ChartTypeIcon type={sel.chartType!} size={16} />} {sel.name}
            </div>
            <dl>
              <dt>Type</dt>
              <dd>{sel.kind === "source" ? "Data Source" : `Chart (${chartTypeName(sel.chartType!)})`}</dd>
              {sel.kind === "source" && src && (
                <>
                  <dt>Connection</dt>
                  <dd>{connLabel(src)}</dd>
                  <dt>Reads</dt>
                  <dd>{src.table ? src.table : "Query"}</dd>
                  <dt>Mode</dt>
                  <dd>{src.mode === "archive" ? `Archive (${(src.archiveRows ?? 0).toLocaleString()} rows, ${when(src.refreshedAt)})` : "Live"}</dd>
                  <dt>Fields</dt>
                  <dd>{arr(src.fields).length}</dd>
                </>
              )}
              {sel.kind === "chart" && (
                <>
                  <dt>Data source</dt>
                  <dd>{sel.source}</dd>
                </>
              )}
              {sel.createdBy && (
                <>
                  <dt>Created by</dt>
                  <dd>{createdByLabel(sel.createdBy)}</dd>
                </>
              )}
              <dt>Created</dt>
              <dd>{when(sel.created)}</dd>
              <dt>Modified</dt>
              <dd>{when(sel.modified)}</dd>
              {sel.remarks && (
                <>
                  <dt>Remarks</dt>
                  <dd>{sel.remarks}</dd>
                </>
              )}
            </dl>
          </>
        ) : (
          <>
            <div className="info-head">
              <ChartColumn size={16} /> {w.name}
            </div>
            <dl>
              <dt>Data sources</dt>
              <dd>{sources.length}</dd>
              <dt>Charts</dt>
              <dd>{charts.length}</dd>
              <dt>Theme</dt>
              <dd>{w.theme === "dark" ? "Dark" : "Light"}</dd>
              {w.createdBy && (
                <>
                  <dt>Created by</dt>
                  <dd>{createdByLabel(w.createdBy)}</dd>
                </>
              )}
              <dt>File</dt>
              <dd>{path}</dd>
            </dl>
          </>
        )
      ) : (
        <ul className="bi-deps">
          {(tab === "using" ? using : usedBy).map((x) => (
            <li key={x.id} onDoubleClick={() => openObject(path, x.kind, x.id, x.name)}>
              {x.kind === "source" ? <Database size={13} /> : <ChartTypeIcon type={(x as { type: string }).type} size={13} />} {x.name}
            </li>
          ))}
          {!(tab === "using" ? using : usedBy).length && <li className="muted">{tab === "using" ? "It doesn't depend on anything." : "Nothing uses it."}</li>}
        </ul>
      )}
    </div>
  );
}
