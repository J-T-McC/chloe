// Dashboard designer: pages of charts and Markdown text on a 12-column grid
// (Navicat's dashboard tab, with tiles that snap to the grid). Pages are 16:9
// slides or reports that grow downward. Tiles are dragged by their grip and
// resized from the corner; Present shows the pages full screen.
import { Copy, Download, FileText, GripVertical, Plus, Presentation, RefreshCw, Save, Trash2 } from "lucide-react";
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import ReactGridLayout, { verticalCompactor } from "react-grid-layout";
import { createScaledStrategy } from "react-grid-layout/core";
import "react-grid-layout/css/styles.css";
import "react-resizable/css/styles.css";
import { B, BI, arr, errorText } from "../../api";
import { tabSavers } from "../../queries";
import { Tab, useApp } from "../../store";
import { openMenu } from "../ContextMenu";
import { alert, confirm, prompt } from "../Dialogs";
import { createdByLabel } from "../model/ModelsList";
import { Split } from "../Split";
import { biChanged, openObject } from "./actions";
import { ChartView } from "./ChartView";
import { ChartTypeIcon } from "./icons";
import { Markdown } from "./Markdown";
import { ChartTheme, TOKENS } from "./theme";
import { useWorkspace } from "./WorkspaceView";
import { renderPages } from "./ExportHost";

const COLS = 12;
const SLIDE_ROWS = 24;
const MARGIN = 12;
const REFRESH_CHOICES = [0, 1, 5, 15, 30, 60];

const uid = () => (crypto.randomUUID ? crypto.randomUUID() : Math.random().toString(36).slice(2));
const comparable = (d: B.Dashboard | null) => (d ? JSON.stringify({ ...d, modified: undefined }) : "");
const bottom = (p: B.Page) => arr(p.items).reduce((b, it) => Math.max(b, it.y + it.h), 0);

export function DashboardEditor({ tab }: { tab: Tab }) {
  const path = tab.biPath!;
  const { w } = useWorkspace(path);
  const [saved, setSaved] = useState<B.Dashboard | null>(null);
  const [draft, setDraft] = useState<B.Dashboard | null>(null);
  const [pageIdx, setPageIdx] = useState(0);
  const [selected, setSelected] = useState("");
  const [tick, setTick] = useState(0);
  const [present, setPresent] = useState(false);
  const [error, setError] = useState("");

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
    const t = setInterval(() => setTick((x) => x + 1), m * 60_000);
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
  const saveRef = useRef(save);
  saveRef.current = save;
  useEffect(() => {
    tabSavers.set(tab.id, () => saveRef.current());
    return () => void tabSavers.delete(tab.id);
  }, [tab.id]);
  useEffect(() => {
    useApp.getState().updateTab(tab.id, { dirty });
  }, [dirty, tab.id]);

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
        if (meta && e.key.toLowerCase() === "r") (e.preventDefault(), setTick((x) => x + 1));
        if (meta && e.key === "Enter") (e.preventDefault(), setPresent(true));
        if (!typing && item && (e.key === "Delete" || e.key === "Backspace")) (e.preventDefault(), setItems((xs) => xs.filter((x) => x.id !== item.id)), setSelected(""));
      }}
    >
      <div className="toolbar">
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
        <span className="tb-sep" />
        <button onClick={() => setTick((x) => x + 1)} onContextMenu={refreshMenu} title="Refresh Data (⌘R); right-click for auto refresh">
          <RefreshCw size={14} /> Refresh Data
        </button>
        <button className="tb-drop" onClick={refreshMenu} title="Auto refresh" aria-label="Auto refresh">
          {draft.refresh ? `every ${draft.refresh} min` : "▾"}
        </button>
        <button onClick={() => setPresent(true)} title="Present (⌘↵)">
          <Presentation size={14} /> Present
        </button>
        <button
          title="Export this page as PNG, or every page as PDF"
          onClick={(e) => {
            const r = e.currentTarget.getBoundingClientRect();
            const run = async (format: "png" | "pdf") => {
              setError("");
              try {
                const pngs = await renderPages(path, draft, format === "png" ? [Math.min(pageIdx, pages.length - 1)] : pages.map((_, i) => i), 2);
                await BI.SaveExport(format === "png" ? `${draft.name} – ${page.name || `Page ${pageIdx + 1}`}` : draft.name, format, pngs);
              } catch (err) {
                setError(errorText(err));
              }
            };
            openMenu({ clientX: r.left, clientY: r.bottom + 2, preventDefault() {} }, [
              { label: "This Page as PNG…", onClick: () => run("png") },
              { label: "All Pages as PDF…", onClick: () => run("pdf") },
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
      </div>
      {error && <div className="error-banner">{error}</div>}
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
      {present && <PresentDashboard pages={pages} start={pageIdx} charts={charts} path={path} theme={theme} tick={tick} onClose={() => setPresent(false)} />}
    </div>
  );
}

interface CanvasProps {
  page: B.Page;
  charts: B.Chart[];
  path: string;
  theme: ChartTheme;
  tick: number;
  editable?: boolean;
  selected?: string;
  onSelect?(id: string): void;
  onLayout?(layout: readonly { i: string; x: number; y: number; w: number; h: number }[]): void;
  /** Fit a slide inside this height too (Present). */
  maxHeight?: number;
  /** Draw without animation, for capture. */
  still?: boolean;
}

/** The width of an element, kept current; 0 until it has been laid out (a hidden
 *  tab has none), so the grid is never placed at a guessed width. */
function useSize() {
  // A callback ref, so an element that appears later (the scaled page) is measured too.
  const [el, ref] = useState<HTMLDivElement | null>(null);
  const [size, setSize] = useState({ width: 0, height: 0 });
  useLayoutEffect(() => {
    if (!el) return;
    setSize({ width: el.clientWidth, height: el.clientHeight });
    const ro = new ResizeObserver(([e]) => setSize({ width: Math.floor(e.contentRect.width), height: Math.floor(e.contentRect.height) }));
    ro.observe(el);
    return () => ro.disconnect();
  }, [el]);
  return { ref, ...size };
}

/** Pages are laid out at one size, 1280px wide (a slide 1280×720), and scaled to
 *  fit where they're shown, so the designer, Present and exports look the same. */
export const PAGE_WIDTH = 1280;
export const SLIDE_HEIGHT = 720;
// A grid row is 18px with 12px gaps: tiles grow in 30px steps.
const REPORT_ROW = 18;
const SLIDE_ROW = (SLIDE_HEIGHT - 2 * MARGIN - (SLIDE_ROWS - 1) * MARGIN) / SLIDE_ROWS;

/** A page's tiles on the grid, scaled to fit: a slide within the space given. */
export function PageCanvas({ page, charts, path, theme, tick, editable, selected, onSelect, onLayout, maxHeight, still }: CanvasProps) {
  const outer = useSize();
  const inner = useSize();
  const slide = page.size === "16:9";
  const scale = outer.width ? (slide && maxHeight ? Math.min(outer.width / PAGE_WIDTH, maxHeight / SLIDE_HEIGHT) : outer.width / PAGE_WIDTH) : 0;
  const strategy = useMemo(() => createScaledStrategy(scale || 1), [scale]);
  const layout = useMemo(() => arr(page.items).map((it) => ({ i: it.id, x: it.x, y: it.y, w: it.w, h: it.h, minW: 2, minH: it.kind === "text" ? 2 : 4 })), [page.items]);
  const t = TOKENS[theme];
  const logicalHeight = slide ? SLIDE_HEIGHT : inner.height;
  return (
    <div ref={outer.ref} className={"bi-canvas " + (slide ? "slide" : "report")} data-testid="bi-canvas">
      {scale > 0 && (
        <div className="bi-canvas-frame" style={{ width: PAGE_WIDTH * scale, height: logicalHeight * scale }}>
          <div
            ref={inner.ref}
            className="bi-canvas-page"
            style={{ width: PAGE_WIDTH, minHeight: slide ? SLIDE_HEIGHT : 400, height: slide ? SLIDE_HEIGHT : undefined, transform: `scale(${scale})`, background: t.plane, color: t.ink }}
          >
            <ReactGridLayout
              layout={layout}
              width={PAGE_WIDTH}
              gridConfig={{ cols: COLS, rowHeight: slide ? SLIDE_ROW : REPORT_ROW, margin: [MARGIN, MARGIN], containerPadding: [MARGIN, MARGIN], maxRows: slide ? SLIDE_ROWS : Infinity }}
              dragConfig={{ enabled: !!editable, handle: ".bi-tile-grip", bounded: slide }}
              resizeConfig={{ enabled: !!editable }}
              positionStrategy={strategy}
              compactor={verticalCompactor}
              onLayoutChange={(l) => onLayout?.(l)}
            >
              {arr(page.items).map((it) => (
                <div key={it.id} className={"bi-tile" + (it.id === selected ? " selected" : "")} onPointerDown={() => onSelect?.(it.id)} data-kind={it.kind}>
                  {editable && (
                    <div className="bi-tile-grip" title="Drag to move">
                      <GripVertical size={13} />
                    </div>
                  )}
                  {it.kind === "chart" ? <ChartTile chartId={it.chart ?? ""} charts={charts} path={path} theme={theme} tick={tick} still={still} /> : <TextTile item={it} theme={theme} />}
                </div>
              ))}
            </ReactGridLayout>
            {!arr(page.items).length && editable && <div className="bi-canvas-empty">Add charts and text with the toolbar.</div>}
          </div>
        </div>
      )}
    </div>
  );
}

function ChartTile({ chartId, charts, path, theme, tick, still }: { chartId: string; charts: B.Chart[]; path: string; theme: ChartTheme; tick: number; still?: boolean }) {
  const chart = charts.find((c) => c.id === chartId);
  const [data, setData] = useState<B.Data | null>(null);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const key = chart ? JSON.stringify({ ...chart, props: undefined, name: undefined, modified: undefined }) : "";
  useEffect(() => {
    if (!chart) return;
    let live = true;
    setLoading(true);
    BI.ChartData(path, chart)
      .then((d) => live && (setData(d), setError("")))
      .catch((e) => live && setError(errorText(e)))
      .finally(() => live && setLoading(false));
    return () => {
      live = false;
    };
  }, [key, path, tick]); // eslint-disable-line react-hooks/exhaustive-deps
  if (!chart) return <div className="bi-chart-msg" data-state="ready">The chart was deleted.</div>;
  return (
    <div className="bi-tile-chart" data-state={loading || (!data && !error) ? "loading" : "ready"}>
      <ChartView chart={chart} data={data} theme={theme} error={error} loading={loading} hint="The chart has no fields yet." still={still} />
    </div>
  );
}

function TextTile({ item, theme }: { item: B.Item; theme: ChartTheme }) {
  const t = TOKENS[theme];
  const by = createdByLabel(item.createdBy);
  return (
    <div className={"bi-text-tile theme-" + theme + (by ? " notes" : "")} style={{ background: t.surface, color: t.ink, borderColor: t.border }} data-testid={by ? "bi-notes" : "bi-text"}>
      {by && (
        <div className="bi-notes-head">
          Notes from {by.replace(/^AI \((.*)\)$/, "$1")}
          {item.updated ? ` · ${new Date(item.updated).toLocaleString()}` : ""}
        </div>
      )}
      <Markdown text={item.text ?? ""} />
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
        <div className="pane-title">{item.kind === "chart" ? "Chart" : "Text"}</div>
        {item.kind === "chart" ? (
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

function PresentDashboard({ pages, start, charts, path, theme, tick, onClose }: { pages: B.Page[]; start: number; charts: B.Chart[]; path: string; theme: ChartTheme; tick: number; onClose(): void }) {
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
      <PageCanvas page={page} charts={charts} path={path} theme={theme} tick={tick} maxHeight={h - 70} />
      <div className="bi-present-hint">
        Page {i + 1} of {pages.length} · ← → to change page · Esc to exit
      </div>
    </div>
  );
}

