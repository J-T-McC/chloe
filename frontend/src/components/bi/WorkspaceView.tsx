// A BI workspace tab: its data sources and charts in a detail or icon view, with
// quick filters and a properties pane (General, Using, Used By), like Navicat's
// workspace window.
import { ChartColumn, Database, LayoutDashboard, LayoutGrid, List, Plus, RefreshCw, Variable } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { B, BI, arr, errorText } from "../../api";
import { connById, useApp, type TabOf } from "../../store";
import { MenuItem, openMenu } from "../ContextMenu";
import { alert, confirm, prompt } from "../Dialogs";
import { createdByLabel } from "../../createdBy";
import { Split } from "../Split";
import { biChanged, openObject, useBIVersion, workspaceTitle } from "./actions";
import { ChartTypeIcon } from "./icons";
import { ParameterDialog, paramRefs } from "./Parameters";
import { Toolbar } from "../Toolbar";
import { safeStorage } from "../../prefs";

type Filter = "all" | "source" | "chart" | "dashboard" | "parameter";

interface Item {
  kind: "source" | "chart" | "dashboard" | "parameter";
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

const pref = (k: string, d: string) => safeStorage.getItem(k) ?? d;
const setPref = safeStorage.setItem;

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

/** The objects that refer to a parameter as [$name]. */
function paramUsers(w: B.Workspace, name: string): { kind: "source" | "chart" | "dashboard"; id: string; name: string; type?: string }[] {
  const n = name.toLowerCase();
  const has = (...texts: (string | null | undefined)[]) => texts.some((t) => paramRefs(t).includes(n));
  const inFilters = (fs?: B.Filter[] | null) => arr(fs).some((f) => arr(f.values).some((v) => has(v)));
  return [
    ...arr(w.dataSources)
      .filter((s) => has(s.sql) || inFilters(s.filters))
      .map((s) => ({ kind: "source" as const, id: s.id, name: s.name })),
    ...arr(w.charts)
      .filter((c) => has(c.props?.title) || inFilters(c.filters))
      .map((c) => ({ kind: "chart" as const, id: c.id, name: c.name, type: c.type })),
    ...arr(w.dashboards)
      .filter((d) => arr(d.pages).some((p) => arr(p.items).some((it) => has(it.text))))
      .map((d) => ({ kind: "dashboard" as const, id: d.id, name: d.name })),
  ];
}

export function connLabel(s?: B.DataSource | null) {
  if (!s) return "";
  const c = connById(s.connId);
  return [c ? c.name : "(deleted connection)", s.database, s.schema].filter(Boolean).join(" › ");
}

export function WorkspaceView({ tab }: { tab: TabOf<"bi"> }) {
  const path = tab.biPath;
  const { w, error } = useWorkspace(path);
  const [filter, setFilter] = useState<Filter>("all");
  const [view, setView] = useState(() => pref("chloe:bi:view", "detail"));
  const [picked, setPicked] = useState("");
  const [search, setSearch] = useState("");
  const [sort, setSort] = useState<{ key: keyof Item; desc: boolean }>({ key: "name", desc: false });
  // The parameter being created ("new") or edited.
  const [editing, setEditing] = useState<B.Parameter | "new" | null>(null);

  useEffect(() => {
    if (w) useApp.getState().updateTab(tab.id, { title: workspaceTitle(w.name) });
  }, [w?.name]); // eslint-disable-line react-hooks/exhaustive-deps

  const sources = arr(w?.dataSources);
  const charts = arr(w?.charts);
  const dashboards = arr(w?.dashboards);
  const params = arr(w?.parameters);
  const items: Item[] = useMemo(() => {
    const out: Item[] = [
      ...sources.map((s) => ({ kind: "source" as const, id: s.id, name: s.name, createdBy: s.createdBy, created: s.created, modified: s.modified, refreshed: s.mode === "archive" ? s.refreshedAt : "", remarks: s.remarks })),
      ...charts.map((c) => ({ kind: "chart" as const, id: c.id, name: c.name, chartType: c.type, source: sources.find((s) => s.id === c.dataSource)?.name ?? "(deleted)", createdBy: c.createdBy, created: c.created, modified: c.modified, remarks: c.remarks })),
      ...dashboards.map((d) => ({ kind: "dashboard" as const, id: d.id, name: d.name, createdBy: d.createdBy, created: d.created, modified: d.modified })),
      ...params.map((p) => ({ kind: "parameter" as const, id: p.id, name: p.name, createdBy: p.createdBy, created: p.created, modified: p.modified, remarks: p.remarks })),
    ];
    const f = search.toLowerCase();
    return out
      .filter((x) => (filter === "all" || x.kind === filter) && (!f || x.name.toLowerCase().includes(f)))
      .sort((a, b) => String(a[sort.key] ?? "").localeCompare(String(b[sort.key] ?? ""), undefined, { numeric: true }) * (sort.desc ? -1 : 1));
  }, [w, filter, search, sort]); // eslint-disable-line react-hooks/exhaustive-deps
  const sel = items.find((x) => x.id === picked) ?? null;

  const open = (x: Item) => (x.kind === "parameter" ? setEditing(params.find((p) => p.id === x.id) ?? null) : openObject(path, x.kind, x.id, x.name));
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
    const name = await prompt(`Rename ${KIND_NAME[x.kind]}`, { message: "New name", defaultValue: x.name });
    if (!name?.trim() || name.trim() === x.name) return;
    run("Couldn't rename it", async () => {
      if (x.kind === "parameter") await BI.SaveParameter(path, { ...params.find((p) => p.id === x.id)!, name: name.trim() });
      else if (x.kind === "dashboard") await BI.SaveDashboard(path, { ...dashboards.find((d) => d.id === x.id)!, name: name.trim() });
      else if (x.kind === "chart") await BI.SaveChart(path, { ...charts.find((c) => c.id === x.id)!, name: name.trim() });
      else await BI.SaveSource(path, { ...sources.find((s) => s.id === x.id)!, name: name.trim() });
      const app = useApp.getState();
      for (const t of app.tabs) if (t.biPath === path && t.biId === x.id) app.updateTab(t.id, { title: name.trim() });
    });
  };
  const remove = async (x: Item) => {
    if (x.kind === "parameter") {
      const users = w ? paramUsers(w, x.name).map((u) => u.name) : [];
      const msg = users.length ? `Delete the parameter "${x.name}"? These refer to it and will show an error until it's back: ${users.join(", ")}.` : `Delete the parameter "${x.name}"? This can't be undone.`;
      if (!(await confirm("Delete Parameter", { message: msg, okLabel: "Delete" }))) return;
      return run("Couldn't delete it", () => BI.DeleteObject(path, x.id));
    }
    const users = x.kind === "source" ? charts.filter((c) => c.dataSource === x.id).map((c) => c.name) : x.kind === "chart" ? dashboards.filter((d) => arr(d.pages).some((p) => arr(p.items).some((it) => it.chart === x.id))).map((d) => d.name) : [];
    const msg = !users.length
      ? `Delete "${x.name}"? This can't be undone.`
      : x.kind === "source"
        ? `Delete the data source "${x.name}"? These charts use it and will show that it's gone: ${users.join(", ")}.`
        : `Delete the chart "${x.name}"? It will be taken off these dashboards: ${users.join(", ")}.`;
    if (!(await confirm(`Delete ${KIND_NAME[x.kind]}`, { message: msg, okLabel: "Delete" }))) return;
    run("Couldn't delete it", async () => {
      await BI.DeleteObject(path, x.id);
      const app = useApp.getState();
      app.closeTabs(app.tabs.filter((t) => t.biPath === path && t.biId === x.id).map((t) => t.id));
    });
  };
  const menu = (x: Item): MenuItem[] => [
    { label: x.kind === "dashboard" ? "Open Dashboard" : x.kind === "parameter" ? "Edit Parameter…" : `Design ${KIND_NAME[x.kind]}`, onClick: () => open(x) },
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
      <Toolbar>
        <button onClick={() => openObject(path, "source", "", "")}>
          <Plus size={14} /> New Data Source
        </button>
        <button onClick={newChart}>
          <Plus size={14} /> New Chart
        </button>
        <button onClick={() => openObject(path, "dashboard", "", "")}>
          <Plus size={14} /> New Dashboard
        </button>
        <button onClick={() => setEditing("new")}>
          <Plus size={14} /> New Parameter
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
          {(["all", "source", "chart", "dashboard", "parameter"] as Filter[]).map((f) => (
            <button key={f} className={filter === f ? "on" : ""} onClick={() => setFilter(f)}>
              {f === "all" ? "All" : KIND_NAME[f]}
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
      </Toolbar>
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
                        <KindIcon x={x} size={13} /> {x.name}
                      </td>
                      <td>{KIND_NAME[x.kind]}</td>
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
                    <div className="bi-icon-art">
                      <KindIcon x={x} size={34} strokeWidth={1.4} />
                    </div>
                    <div className="bi-icon-name">{x.name}</div>
                  </div>
                ))}
              </div>
            )}
            {w && !items.length && (
              <div className="center-hint">{sources.length + charts.length + dashboards.length ? "Nothing matches the filter." : "This workspace is empty. Start with New Data Source: a table, view or query that charts are built on."}</div>
            )}
          </div>
        }
        second={<Properties w={w} sel={sel} path={path} />}
      />
      {editing && (
        <ParameterDialog
          path={path}
          param={editing === "new" ? null : editing}
          onClose={(saved) => {
            setEditing(null);
            if (saved) biChanged(path);
          }}
        />
      )}
    </div>
  );
}

const KIND_NAME = { source: "Data Source", chart: "Chart", dashboard: "Dashboard", parameter: "Parameter" } as const;

function KindIcon({ x, size, strokeWidth }: { x: Pick<Item, "kind" | "chartType">; size: number; strokeWidth?: number }) {
  if (x.kind === "source") return <Database size={size} strokeWidth={strokeWidth} />;
  if (x.kind === "dashboard") return <LayoutDashboard size={size} strokeWidth={strokeWidth} />;
  if (x.kind === "parameter") return <Variable size={size} strokeWidth={strokeWidth} />;
  return <ChartTypeIcon type={x.chartType!} size={size} strokeWidth={strokeWidth} />;
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
  waterfall: "Waterfall",
  tornado: "Tornado",
  treemap: "Treemap",
  scatter: "Scatter",
  heatmap: "Heatmap",
  trend: "Trend",
  kpi: "KPI",
  gauge: "Gauge",
  pivot: "Pivot Table",
  control: "Control",
};
export const chartTypeName = (t: string) => CHART_TYPE_NAMES[t] ?? t;

function Properties({ w, sel, path }: { w: B.Workspace | null; sel: Item | null; path: string }) {
  const [tab, setTab] = useState<"general" | "using" | "usedby">("general");
  if (!w) return <div className="info-panel empty" />;
  const sources = arr(w.dataSources);
  const charts = arr(w.charts);
  const dashboards = arr(w.dashboards);
  const src = sel?.kind === "source" ? sources.find((s) => s.id === sel.id) : sel ? sources.find((s) => s.id === charts.find((c) => c.id === sel.id)?.dataSource) : null;
  type Dep = { kind: "source" | "chart" | "dashboard"; id: string; name: string; type?: string };
  const board = sel?.kind === "dashboard" ? dashboards.find((d) => d.id === sel.id) : null;
  const boardCharts = board ? charts.filter((c) => arr(board.pages).some((p) => arr(p.items).some((it) => it.chart === c.id))) : [];
  const using: Dep[] = sel?.kind === "chart" ? (src ? [{ kind: "source", id: src.id, name: src.name }] : []) : boardCharts.map((c) => ({ kind: "chart", id: c.id, name: c.name, type: c.type }));
  const param = sel?.kind === "parameter" ? arr(w.parameters).find((p) => p.id === sel.id) : null;
  const usedBy: Dep[] =
    param
      ? paramUsers(w, param.name)
      : sel?.kind === "source"
      ? charts.filter((c) => c.dataSource === sel.id).map((c) => ({ kind: "chart", id: c.id, name: c.name, type: c.type }))
      : sel?.kind === "chart"
        ? dashboards.filter((d) => arr(d.pages).some((p) => arr(p.items).some((it) => it.chart === sel.id))).map((d) => ({ kind: "dashboard", id: d.id, name: d.name }))
        : [];
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
              <KindIcon x={sel} size={16} /> {sel.name}
            </div>
            <dl>
              <dt>Type</dt>
              <dd>{sel.kind === "chart" ? `Chart (${chartTypeName(sel.chartType!)})` : KIND_NAME[sel.kind]}</dd>
              {board && (
                <>
                  <dt>Pages</dt>
                  <dd>{arr(board.pages).length}</dd>
                </>
              )}
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
              {param && (
                <>
                  <dt>Refer to it as</dt>
                  <dd>
                    <code>[${param.name}]</code>
                  </dd>
                  <dt>Value</dt>
                  <dd>{param.value}</dd>
                  <dt>Allowed</dt>
                  <dd>{param.allowed === "list" ? arr(param.list).map((o) => o.value).join(", ") : param.allowed === "range" ? `${param.min} to ${param.max}` : `Any ${param.type === "number" ? "number" : param.type === "datetime" ? "date" : "text"}`}</dd>
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
              <dt>Dashboards</dt>
              <dd>{dashboards.length}</dd>
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
              <KindIcon x={{ kind: x.kind, chartType: x.type }} size={13} /> {x.name}
            </li>
          ))}
          {!(tab === "using" ? using : usedBy).length && <li className="muted">{tab === "using" ? "It doesn't depend on anything." : "Nothing uses it."}</li>}
        </ul>
      )}
    </div>
  );
}
