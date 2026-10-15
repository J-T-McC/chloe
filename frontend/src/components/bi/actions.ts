// Opening BI workspaces and their data sources and charts, and keeping open lists
// and tabs in step when a workspace changes (here or over MCP).
import { create } from "zustand";
import { B, BI, BICatalog, Events, arr, errorText } from "../../api";
import { useApp } from "../../store";
import { alert, prompt } from "../Dialogs";

/** Bumped per workspace path (and "" for the library) when something changes. */
export const useBIVersion = create<{ lib: number; ws: Record<string, number> }>(() => ({ lib: 0, ws: {} }));

export function biChanged(path?: string) {
  useBIVersion.setState((s) => ({ lib: s.lib + 1, ws: path ? { ...s.ws, [path]: (s.ws[path] ?? 0) + 1 } : s.ws }));
}

let listening = false;
export function listenBI() {
  if (listening) return;
  listening = true;
  Events.On("bi:changed", (ev: { data: string }) => biChanged(ev.data));
  Events.On("mcp:open-bi", (ev: { data: { path: string; id: string } }) => openObjectById(ev.data.path, ev.data.id));
}

let catalog: Promise<BICatalog> | null = null;
export const loadCatalog = () => (catalog ??= BI.Catalog());

/** Workspace objects by tab kind. */
export type ObjectKind = "source" | "chart" | "dashboard";

export function workspaceTitle(name: string) {
  return `${name} (BI)`;
}

export async function openWorkspace(path: string) {
  try {
    const w = await BI.Open(path);
    useApp.getState().openTab({ kind: "bi", title: workspaceTitle(w.name), biPath: path });
  } catch (e) {
    alert("Could not open the workspace", errorText(e));
  }
}

const TAB_KIND = { source: "bisource", chart: "bichart", dashboard: "bidash" } as const;
const NEW_TITLE = { source: "New Data Source", chart: "New Chart", dashboard: "New Dashboard" };

export function openObject(path: string, kind: ObjectKind, id: string, name: string) {
  useApp.getState().openTab({ kind: TAB_KIND[kind], title: name || NEW_TITLE[kind], biPath: path, biId: id });
}

async function openObjectById(path: string, id: string) {
  try {
    const w = await BI.Open(path);
    const c = arr(w.charts).find((x) => x.id === id);
    const s = arr(w.dataSources).find((x) => x.id === id);
    const d = arr(w.dashboards).find((x) => x.id === id);
    if (d) openObject(path, "dashboard", d.id, d.name);
    else if (c) openObject(path, "chart", c.id, c.name);
    else if (s) openObject(path, "source", s.id, s.name);
  } catch (e) {
    alert("Could not open the chart", errorText(e));
  }
}

export async function newWorkspace() {
  const name = await prompt("New Workspace", { message: "Workspace name", defaultValue: "Untitled" });
  if (!name?.trim()) return;
  try {
    const p = await BI.Create(name.trim());
    biChanged(p);
    openWorkspace(p);
  } catch (e) {
    alert("Could not create the workspace", errorText(e));
  }
}

/** A new chart on a draft data source, from a data grid's table or query. */
export const useCreateChart = create<{ src: B.DataSource | null }>(() => ({ src: null }));

export function createChartFrom(src: Pick<B.DataSource, "connId" | "database" | "schema" | "table" | "sql" | "name">) {
  useCreateChart.setState({ src: { id: "", mode: "live", ...src } as B.DataSource });
}

/** Keep tabs pointing at a workspace whose file moved (renamed). */
export function moveWorkspaceTabs(from: string, to: string, name?: string) {
  const app = useApp.getState();
  for (const t of app.tabs) {
    if (t.biPath === from) app.updateTab(t.id, { biPath: to, ...(t.kind === "bi" && name ? { title: workspaceTitle(name) } : {}) });
  }
}

/** The fields charts can use: the source's columns after projection, then its
 *  custom fields (typed when the source was saved). */
export function sourceFields(src: B.DataSource | null | undefined): B.Field[] {
  const all = arr(src?.fields);
  const p = src?.projection;
  const base = !p || !arr(p.fields).length ? all : all.filter((f) => arr(p.fields).includes(f.name) !== !!p.exclude);
  return [...base, ...arr(src?.customFields).map((cf) => ({ name: cf.name, type: cf.type || "string", dbType: "custom" }) as B.Field)];
}
