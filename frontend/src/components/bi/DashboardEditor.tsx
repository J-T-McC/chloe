// Dashboard designer: pages of charts and Markdown text on a 12-column grid
// (Navicat's dashboard tab, with tiles that snap to the grid). Pages are 16:9
// slides or reports that grow downward. Tiles are dragged by their grip and
// resized from the corner; Present shows the pages full screen.
import { Copy, Download, FileText, Plus, Presentation, RefreshCw, Save, Shapes, Trash2 } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { B, BI, arr, errorText } from "../../api";
import { useApp, type TabOf } from "../../store";
import { openMenu } from "../ContextMenu";
import { alert, confirm, prompt } from "../Dialogs";
import { createdByLabel } from "../../createdBy";
import { Split } from "../Split";
import { biChanged, openObject } from "./actions";
import { ChartTypeIcon } from "./icons";
import { SHAPES } from "./ShapeTile";
import { ChartTheme } from "./theme";
import { useWorkspace } from "./WorkspaceView";
import { renderPages } from "./ExportHost";
import { buildReport } from "./reportExport";
import { PageCanvas, SLIDE_ROWS } from "./Canvas";
import { ParamBar, ParamValues, dashboardParams, paramValues } from "./Parameters";
import { Toolbar } from "../Toolbar";
import { useSavableTab } from "../useSavableTab";
import { Check } from "../form";

const REFRESH_CHOICES = [0, 1, 5, 15, 30, 60];

const uid = () => (crypto.randomUUID ? crypto.randomUUID() : Math.random().toString(36).slice(2));
const comparable = (d: B.Dashboard | null) => (d ? JSON.stringify({ ...d, modified: undefined }) : "");
const bottom = (p: B.Page) => arr(p.items).reduce((b, it) => Math.max(b, it.y + it.h), 0);

export function DashboardEditor({ tab }: { tab: TabOf<"bidash"> }) {
  const path = tab.biPath;
  const { w } = useWorkspace(path);
  const [saved, setSaved] = useState<B.Dashboard | null>(null);
  const [draft, setDraft] = useState<B.Dashboard | null>(null);
  const [pageIdx, setPageIdx] = useState(0);
  const [selected, setSelected] = useState("");
  // When Refresh (or auto refresh) last ran, in ms. Charts take data read from
  // then on, so every page shares one read of each source per refresh.
  const [tick, setTick] = useState(0);
  const [present, setPresent] = useState(false);
  const [error, setError] = useState("");
  // An export in progress (pages captured so far), and the file the last one wrote.
  const [exporting, setExporting] = useState<{ done: number; total: number; what: string } | null>(null);
  const [exported, setExported] = useState("");
  const cancelExport = useRef(false);
  // Parameter values changed on this dashboard, for this session only.
  const [overrides, setOverrides] = useState<ParamValues>({});
  const usedParams = useMemo(() => dashboardParams(w, draft), [w, draft]);
  const values = useMemo(() => paramValues(w?.parameters, overrides), [w?.parameters, overrides]);
  const setParam = (name: string, v: string) => setOverrides((o) => ({ ...o, [name]: v }));

  const dirty = !!draft && comparable(draft) !== comparable(saved);
  useEffect(() => {
    if (!w) return;
    const found = tab.biId ? arr(w.dashboards).find((d) => d.id === tab.biId) : null;
    if (found) {
      if (!dirty) {
        setSaved(found);
        setDraft(found);
        if (tab.title !== found.name) useApp.getState().updateTab(tab.id, { title: found.name });
      }
      return;
    }
    if (draft) return;
    let n = arr(w.dashboards).length + 1;
    while (arr(w.dashboards).some((d) => d.name === `Dashboard ${n}`)) n++;
    setDraft({ id: "", name: `Dashboard ${n}`, pages: [{ id: uid(), size: "16:9", items: [] }] });
  }, [w]); // eslint-disable-line react-hooks/exhaustive-deps

  // Auto refresh while the tab is open.
  useEffect(() => {
    const m = draft?.refresh ?? 0;
    if (!m) return;
    const t = setInterval(() => setTick(Date.now()), m * 60_000);
    return () => clearInterval(t);
  }, [draft?.refresh]);

  const save = useCallback(async (): Promise<boolean> => {
    if (!draft) return false;
    setError("");
    try {
      const s = await BI.SaveDashboard(path, draft);
      setSaved(s);
      setDraft(s);
      useApp.getState().updateTab(tab.id, { biId: s.id, title: s.name, dirty: false });
      biChanged(path);
      return true;
    } catch (e) {
      setError(errorText(e));
      return false;
    }
  }, [draft, path, tab.id]);
  useSavableTab(tab.id, dirty, save);

  const charts = arr(w?.charts);
  const theme: ChartTheme = w?.theme === "dark" ? "dark" : "light";
  if (!draft) return <div className="center-hint">Loading…</div>;
  const pages = arr(draft.pages);
  const page = pages[Math.min(pageIdx, pages.length - 1)];
  const item = arr(page?.items).find((x) => x.id === selected) ?? null;

  const setPage = (patch: Partial<B.Page>) => setDraft((d) => (d ? { ...d, pages: arr(d.pages).map((p) => (p.id === page.id ? { ...p, ...patch } : p)) } : d));
  const setItems = (fn: (items: B.Item[]) => B.Item[]) => setPage({ items: fn(arr(page.items)) });
  const setItem = (id: string, patch: Partial<B.Item>) => setItems((xs) => xs.map((x) => (x.id === id ? { ...x, ...patch } : x)));
  const place = (w: number, h: number): { y: number; h: number } | null => {
    const y = bottom(page);
    if (page.size !== "16:9") return { y, h };
    if (SLIDE_ROWS - y < 2) return null;
    return { y, h: Math.min(h, SLIDE_ROWS - y) };
  };
  const addTile = (tile: Omit<B.Item, "id" | "x" | "y" | "w" | "h">, w: number, h: number) => {
    const at = place(w, h);
    if (!at) return alert("No room on this slide", "Make room by resizing a tile, or switch the page to Report to let it grow.");
    const id = uid();
    setItems((xs) => [...xs, { ...tile, id, x: 0, y: at.y, w, h: at.h }]);
    setSelected(id);
  };
  const addChart = (e: React.MouseEvent) => {
    if (!charts.length) return alert("Add Chart", "This workspace has no charts yet. Create one first.");
    const r = e.currentTarget.getBoundingClientRect();
    openMenu(
      { clientX: r.left, clientY: r.bottom + 2, preventDefault() {} },
      charts.map((c) => ({ label: c.name, onClick: () => addTile({ kind: "chart", chart: c.id }, c.type === "value" ? 3 : 6, c.type === "value" ? 6 : 10) })),
    );
  };
  const addPage = (copyOf?: B.Page) => {
    const p: B.Page = copyOf ? { ...copyOf, id: uid(), name: copyOf.name ? `${copyOf.name} copy` : "", items: arr(copyOf.items).map((it) => ({ ...it, id: uid() })) } : { id: uid(), size: page?.size ?? "16:9", items: [] };
    const at = copyOf ? pageIdx + 1 : pages.length;
    setDraft({ ...draft, pages: [...pages.slice(0, at), p, ...pages.slice(at)] });
    setPageIdx(at);
    setSelected("");
  };
  const deletePage = async () => {
    if (pages.length < 2) return;
    if (arr(page.items).length && !(await confirm("Delete Page", { message: "Delete this page and its tiles?", okLabel: "Delete" }))) return;
    setDraft({ ...draft, pages: pages.filter((p) => p.id !== page.id) });
    setPageIdx(Math.max(0, pageIdx - 1));
  };
  const refreshMenu = (e: React.MouseEvent) => {
    e.preventDefault();
    const r = e.currentTarget.getBoundingClientRect();
    openMenu(
      { clientX: r.left, clientY: r.bottom + 2, preventDefault() {} },
      REFRESH_CHOICES.map((m) => ({ label: m ? `Auto refresh every ${m} min` : "Auto refresh off", checked: (draft.refresh ?? 0) === m, onClick: () => setDraft({ ...draft, refresh: m }) })),
    );
  };

  return (
    <div
      className="bi-dash-editor"
      tabIndex={-1}
      onKeyDown={(e) => {
        const meta = e.metaKey || e.ctrlKey;
        const typing = (e.target as HTMLElement).closest("input, textarea, select");
        if (meta && e.key.toLowerCase() === "s") (e.preventDefault(), save());
        if (meta && e.key.toLowerCase() === "r") (e.preventDefault(), setTick(Date.now()));
        if (meta && e.key === "Enter") (e.preventDefault(), setPresent(true));
        if (!typing && item && (e.key === "Delete" || e.key === "Backspace")) (e.preventDefault(), setItems((xs) => xs.filter((x) => x.id !== item.id)), setSelected(""));
      }}
    >
      <Toolbar>
        <button onClick={save} className={dirty ? "accent" : ""} title="Save (⌘S)">
          <Save size={14} /> Save
        </button>
        <span className="tb-sep" />
        <button onClick={addChart}>
          <Plus size={14} /> Add Chart
        </button>
        <button onClick={() => addTile({ kind: "text", text: "## Title\n\nWrite a summary here." }, 12, 4)}>
          <FileText size={14} /> Add Text
        </button>
        <button
          onClick={(e) =>
            openMenu(
              e,
              SHAPES.map(([shape, label]) => {
                // Lines run across one row; outlines start roughly square; icons a little taller than wide.
                const [w, h] = shape === "line" || shape === "arrow" ? [4, 1] : ["database", "cloud", "server", "desktop", "mobile", "person"].includes(shape) ? [2, 5] : [3, 6];
                return { label, onClick: () => addTile({ kind: "shape", shape, lineWidth: 2 } as never, w, h) };
              }),
            )
          }
        >
          <Shapes size={14} /> Add Shape
        </button>
        <span className="tb-sep" />
        <button onClick={() => setTick(Date.now())} onContextMenu={refreshMenu} title="Refresh Data (⌘R); right-click for auto refresh">
          <RefreshCw size={14} /> Refresh Data
        </button>
        <button className="tb-drop" onClick={refreshMenu} title="Auto refresh" aria-label="Auto refresh">
          {draft.refresh ? `every ${draft.refresh} min` : "▾"}
        </button>
        <button onClick={() => setPresent(true)} title="Present (⌘↵)">
          <Presentation size={14} /> Present
        </button>
        <button
          title="Export this page as PNG, or every page as PDF or HTML"
          disabled={!!exporting}
          onClick={(e) => {
            const r = e.currentTarget.getBoundingClientRect();
            const run = async (format: "png" | "pdf" | "html") => {
              setError("");
              setExported("");
              try {
                // Ask where first: drawing every page takes a while, and a cancelled dialog shouldn't.
                const file = await BI.ChooseExportFile(format === "png" ? `${draft.name} – ${page.name || `Page ${pageIdx + 1}`}` : draft.name, format);
                if (!file) return;
                const which = format === "png" ? [Math.min(pageIdx, pages.length - 1)] : pages.map((_, i) => i);
                cancelExport.current = false;
                const cancelled = () => cancelExport.current;
                if (format === "html") {
                  setExporting({ done: 0, total: 0, what: "chart" });
                  const html = await buildReport(path, draft, { overrides, since: tick, cancelled, progress: (done, total) => setExporting({ done, total, what: "chart" }) });
                  await BI.WriteExport(file, format, [html]);
                } else {
                  setExporting({ done: 0, total: which.length, what: "page" });
                  const pngs = await renderPages(path, draft, which, 2, overrides, { page: (done, total) => setExporting({ done, total, what: "page" }), cancelled, since: tick });
                  await BI.WriteExport(file, format, pngs);
                }
                setExported(file);
              } catch (err) {
                if (!cancelExport.current) setError(errorText(err));
              } finally {
                setExporting(null);
              }
            };
            openMenu({ clientX: r.left, clientY: r.bottom + 2, preventDefault() {} }, [
              { label: "This Page as PNG…", onClick: () => run("png") },
              { label: "All Pages as PDF…", onClick: () => run("pdf") },
              { label: "All Pages as HTML…", onClick: () => run("html") },
            ]);
          }}
        >
          <Download size={14} /> Export
        </button>
        <span className="spacer" />
        <label className="tb-label">
          Page{" "}
          <select aria-label="Page size" value={page.size} onChange={(e) => setPage({ size: e.target.value })}>
            <option value="16:9">16:9 slide</option>
            <option value="report">Report (scrolls)</option>
          </select>
        </label>
      </Toolbar>
      {error && <div className="error-banner">{error}</div>}
      {exporting && (
        <div className="notice-banner" role="status">
          <span>
            {exporting.what === "chart" ? "Reading" : "Exporting"}
            {exporting.total > 1 ? ` ${exporting.what} ${Math.min(exporting.done + 1, exporting.total)} of ${exporting.total}` : ""}…
          </span>
          <progress max={exporting.total} value={exporting.done} />
          <button onClick={() => (cancelExport.current = true)}>Cancel</button>
        </div>
      )}
      {exported && !exporting && (
        <div className="notice-banner" role="status">
          <span>Saved {exported}</span>
          <button onClick={() => BI.Reveal(exported).catch((e) => setError(errorText(e)))}>Show in Finder</button>
          <button onClick={() => setExported("")} aria-label="Dismiss">
            ✕
          </button>
        </div>
      )}
      <ParamBar params={usedParams} values={values} onChange={setParam} />
      <Split
        id="bi-dash-pages"
        className="objects-split"
        firstSize={150}
        minFirst={110}
        maxFirst="30"
        first={
          <div className="bi-pages">
            <div className="pane-title">Pages</div>
            {pages.map((p, i) => (
              <div
                key={p.id}
                className={"bi-page-thumb" + (p.id === page.id ? " selected" : "")}
                onClick={() => (setPageIdx(i), setSelected(""))}
                onDoubleClick={async () => {
                  const name = await prompt("Rename Page", { defaultValue: p.name ?? "" });
                  if (name !== null) setDraft({ ...draft, pages: pages.map((x) => (x.id === p.id ? { ...x, name: name.trim() } : x)) });
                }}
                onContextMenu={(e) =>
                  openMenu(e, [
                    { label: "Duplicate Page", onClick: () => (setPageIdx(i), addPage(p)) },
                    { label: "Delete Page", disabled: pages.length < 2, onClick: () => (setPageIdx(i), deletePage()) },
                  ])
                }
              >
                <div className={"bi-page-art " + (p.size === "16:9" ? "slide" : "report")}>{i + 1}</div>
                <div className="bi-page-name">{p.name || `Page ${i + 1}`}</div>
              </div>
            ))}
            <div className="bi-page-actions">
              <button className="small-btn" title="Add Page" onClick={() => addPage()}>
                <Plus size={12} />
              </button>
              <button className="small-btn" title="Duplicate Page" onClick={() => addPage(page)}>
                <Copy size={12} />
              </button>
              <button className="small-btn" title="Delete Page" disabled={pages.length < 2} onClick={deletePage}>
                <Trash2 size={12} />
              </button>
            </div>
          </div>
        }
        second={
          <Split
            id="bi-dash-props"
            className="objects-split"
            secondSize={260}
            minFirst={360}
            minSecond={190}
            maxSecond="40"
            first={
              <div className="bi-dash-canvas-wrap" onPointerDown={(e) => !(e.target as HTMLElement).closest(".bi-tile") && setSelected("")}>
                <PageCanvas
                  page={page}
                  charts={charts}
                  path={path}
                  theme={theme}
                  tick={tick}
                  overrides={overrides}
                  values={values}
                  sources={arr(w?.dataSources)}
                  interaction={draft.interaction}
                  editable
                  selected={selected}
                  onSelect={setSelected}
                  onLayout={(layout) =>
                    setItems((xs) => {
                      let changed = false;
                      const next = xs.map((x) => {
                        const l = layout.find((p) => p.i === x.id);
                        if (!l || (l.x === x.x && l.y === x.y && l.w === x.w && l.h === x.h)) return x;
                        changed = true;
                        return { ...x, x: l.x, y: l.y, w: l.w, h: l.h };
                      });
                      return changed ? next : xs;
                    })
                  }
                />
              </div>
            }
            second={
              <DashProps
                draft={draft}
                setDraft={setDraft}
                page={page}
                setPage={setPage}
                item={item}
                setItem={setItem}
                charts={charts}
                remove={() => item && (setItems((xs) => xs.filter((x) => x.id !== item.id)), setSelected(""))}
                openChart={(c) => openObject(path, "chart", c.id, c.name)}
              />
            }
          />
        }
      />
      {present && (
        <PresentDashboard pages={pages} start={pageIdx} charts={charts} path={path} theme={theme} tick={tick} onClose={() => setPresent(false)} overrides={overrides} values={values} sources={arr(w?.dataSources)} interaction={draft.interaction}>
          {() => <ParamBar params={usedParams} values={values} onChange={setParam} />}
        </PresentDashboard>
      )}
    </div>
  );
}

function DashProps(p: {
  draft: B.Dashboard;
  setDraft(d: B.Dashboard): void;
  page: B.Page;
  setPage(patch: Partial<B.Page>): void;
  item: B.Item | null;
  setItem(id: string, patch: Partial<B.Item>): void;
  charts: B.Chart[];
  remove(): void;
  openChart(c: B.Chart): void;
}) {
  const { draft, page, item } = p;
  if (item) {
    const chart = p.charts.find((c) => c.id === item.chart);
    return (
      <div className="bi-props-pane">
        <div className="pane-title">{item.kind === "chart" ? "Chart" : item.kind === "shape" ? "Shape" : "Text"}</div>
        {item.kind === "shape" ? (
          <section className="bi-prop-section">
            <label className="bi-prop">
              Shape
              <select aria-label="Shape" value={item.shape} onChange={(e) => p.setItem(item.id, { shape: e.target.value })}>
                {SHAPES.map(([s, l]) => (
                  <option key={s} value={s}>
                    {l}
                  </option>
                ))}
              </select>
            </label>
            <div className="bi-prop-row">
              <label className="bi-prop">
                Colour
                <input aria-label="Shape colour" type="color" value={item.color || "#52514e"} onChange={(e) => p.setItem(item.id, { color: e.target.value })} />
              </label>
              <label className="bi-prop">
                Line width
                <input aria-label="Line width" type="number" min={1} max={12} value={item.lineWidth || 2} onChange={(e) => p.setItem(item.id, { lineWidth: Math.max(1, Number(e.target.value) || 1) })} />
              </label>
            </div>
            <Check label="Filled" checked={!!item.filled} onChange={(v) => p.setItem(item.id, { filled: v })} />
            <Check label="Dashed" checked={!!item.dashed} onChange={(v) => p.setItem(item.id, { dashed: v })} />
          </section>
        ) : item.kind === "chart" ? (
          <section className="bi-prop-section">
            <label className="bi-prop">
              Chart
              <select aria-label="Tile chart" value={item.chart} onChange={(e) => p.setItem(item.id, { chart: e.target.value })}>
                {!chart && <option value={item.chart}>(deleted chart)</option>}
                {p.charts.map((c) => (
                  <option key={c.id} value={c.id}>
                    {c.name}
                  </option>
                ))}
              </select>
            </label>
            {chart && (
              <button onClick={() => p.openChart(chart)}>
                <ChartTypeIcon type={chart.type} size={13} /> Design Chart
              </button>
            )}
            {chart?.type === "control" && (
              <fieldset className="bi-targets">
                <legend>Target charts</legend>
                <div className="muted">None picked: every chart on the page with the field {arr(chart.dimension)[0]?.field ?? ""}.</div>
                {arr(page.items)
                  .filter((x) => x.kind === "chart" && x.id !== item.id)
                  .map((x) => p.charts.find((c) => c.id === x.chart))
                  .filter((c): c is B.Chart => !!c && c.type !== "control")
                  .map((c) => (
                    <label key={c.id} className="check">
                      <input
                        type="checkbox"
                        checked={arr(item.targets).includes(c.id)}
                        onChange={(e) => p.setItem(item.id, { targets: e.target.checked ? [...arr(item.targets), c.id] : arr(item.targets).filter((t) => t !== c.id) })}
                      />
                      {c.name}
                    </label>
                  ))}
              </fieldset>
            )}
          </section>
        ) : (
          <section className="bi-prop-section">
            {item.createdBy && <div className="muted">{createdByLabel(item.createdBy)} wrote these notes; they replace them when they write again.</div>}
            <label className="bi-prop">
              Markdown
              <textarea aria-label="Text" className="bi-md-input" rows={14} value={item.text ?? ""} onChange={(e) => p.setItem(item.id, { text: e.target.value })} />
            </label>
          </section>
        )}
        <section className="bi-prop-section">
          <div className="muted">
            Column {item.x + 1}, row {item.y + 1} · {item.w}×{item.h}
          </div>
          <label className="check">
            <input type="checkbox" aria-label="Lock tile" checked={!!item.locked} onChange={(e) => p.setItem(item.id, { locked: e.target.checked })} /> Lock position and size
          </label>
          <button onClick={p.remove}>
            <Trash2 size={13} /> Remove from Page
          </button>
        </section>
      </div>
    );
  }
  return (
    <div className="bi-props-pane">
      <div className="pane-title">Dashboard</div>
      <section className="bi-prop-section">
        <label className="bi-prop">
          Name
          <input aria-label="Dashboard name" value={draft.name} onChange={(e) => p.setDraft({ ...draft, name: e.target.value })} />
        </label>
        <label className="bi-prop">
          Auto refresh
          <select aria-label="Refresh" value={draft.refresh ?? 0} onChange={(e) => p.setDraft({ ...draft, refresh: Number(e.target.value) })}>
            {REFRESH_CHOICES.map((m) => (
              <option key={m} value={m}>
                {m ? `Every ${m} min` : "Off"}
              </option>
            ))}
          </select>
        </label>
        <label className="bi-prop">
          Clicking a point
          <select aria-label="Chart interaction" value={draft.interaction || "filter"} onChange={(e) => p.setDraft({ ...draft, interaction: e.target.value === "filter" ? "" : e.target.value })}>
            <option value="filter">Filters charts on the same data source</option>
            <option value="highlight">Highlights it in charts on the same data source</option>
            <option value="none">Does nothing</option>
          </select>
        </label>
        {draft.createdBy && <div className="muted">Created by {createdByLabel(draft.createdBy)}</div>}
      </section>
      <div className="pane-title">Page</div>
      <section className="bi-prop-section">
        <label className="bi-prop">
          Name
          <input aria-label="Page name" placeholder="(untitled)" value={page.name ?? ""} onChange={(e) => p.setPage({ name: e.target.value })} />
        </label>
        <label className="bi-prop">
          Page note
          <textarea aria-label="Page note" rows={3} value={page.note ?? ""} onChange={(e) => p.setPage({ note: e.target.value })} />
        </label>
      </section>
    </div>
  );
}

function PresentDashboard({
  pages,
  start,
  charts,
  path,
  theme,
  tick,
  onClose,
  overrides,
  values,
  sources,
  interaction,
  children,
}: {
  pages: B.Page[];
  start: number;
  charts: B.Chart[];
  path: string;
  theme: ChartTheme;
  tick: number;
  onClose(): void;
  overrides?: ParamValues;
  values?: ParamValues;
  sources?: B.DataSource[];
  interaction?: string;
  /** Parameter controls, shown above the page. */
  children?: (o?: ParamValues) => React.ReactNode;
}) {
  const [i, setI] = useState(start);
  const [h, setH] = useState(window.innerHeight);
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
      if (e.key === "ArrowRight" || e.key === "PageDown") setI((x) => Math.min(pages.length - 1, x + 1));
      if (e.key === "ArrowLeft" || e.key === "PageUp") setI((x) => Math.max(0, x - 1));
    };
    const onResize = () => setH(window.innerHeight);
    window.addEventListener("keydown", onKey);
    window.addEventListener("resize", onResize);
    return () => (window.removeEventListener("keydown", onKey), window.removeEventListener("resize", onResize));
  }, [onClose, pages.length]);
  const page = pages[i];
  return (
    <div className={"bi-present dash theme-" + theme} data-testid="bi-present">
      {children?.(overrides)}
      <PageCanvas page={page} charts={charts} path={path} theme={theme} tick={tick} maxHeight={h - 70 - (children ? 48 : 0)} overrides={overrides} values={values} sources={sources} interaction={interaction} />
      <div className="bi-present-hint">
        Page {i + 1} of {pages.length} · ← → to change page · Esc to exit
      </div>
    </div>
  );
}

