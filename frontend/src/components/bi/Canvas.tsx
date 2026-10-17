// A dashboard page drawn on its grid: the designer, Present, exports and the
// HTML report viewer all draw pages with PageCanvas, so they look the same.
// Charts read their data through a reader set at startup (Chloe's bindings, or
// the data an HTML report carries), so this module needs nothing of the app.
import { GripVertical, Lock } from "lucide-react";
import { useEffect, useLayoutEffect, useMemo, useState } from "react";
import ReactGridLayout, { verticalCompactor } from "react-grid-layout";
import { createScaledStrategy } from "react-grid-layout/core";
import "react-grid-layout/css/styles.css";
import "react-resizable/css/styles.css";
import type { B } from "../../api";
import { arr } from "../../arr";
import { createdByLabel } from "../../createdBy";
import { ChartInteraction, ChartView } from "./ChartView";
import { sourceFields } from "./fields";
import { Pick, controlFilter, controlStyle, reach } from "./interaction";
import { Markdown } from "./Markdown";
import { ParamValues, expandParams } from "./params";
import { readChart } from "./reader";
import { ShapeTile } from "./ShapeTile";
import { ChartTheme, TOKENS } from "./theme";

export const COLS = 12;
export const SLIDE_ROWS = 24;
export const MARGIN = 12;

const errorText = (e: unknown) => (e instanceof Error ? e.message : typeof e === "string" ? e : e && typeof e === "object" && "message" in e ? String((e as { message: unknown }).message) : String(e));

export interface CanvasProps {
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
  /** Parameter values changed for this session (sent with chart reads). */
  overrides?: ParamValues;
  /** Every parameter's value, for [$name] in text. */
  values?: ParamValues;
  /** The workspace's data sources (their fields decide what a Control reaches). */
  sources?: B.DataSource[];
  /** What a click on a point does: filter (default), highlight or none. */
  interaction?: string;
  /** Drawn from data read earlier (an HTML report): clicks highlight instead of
   *  filtering and Controls are shown but off, since neither can query again. */
  report?: boolean;
}

/** The size of an element, kept current; 0 until it has been laid out, so the
 *  grid is never placed at a guessed width. A tab being hidden (display: none)
 *  reads 0×0: the last size is kept, so the tiles stay mounted and drawn. */
function useSize() {
  // A callback ref, so an element that appears later (the scaled page) is measured too.
  const [el, ref] = useState<HTMLDivElement | null>(null);
  const [size, setSize] = useState({ width: 0, height: 0 });
  useLayoutEffect(() => {
    if (!el) return;
    setSize({ width: el.clientWidth, height: el.clientHeight });
    const ro = new ResizeObserver(([e]) => {
      const width = Math.floor(e.contentRect.width), height = Math.floor(e.contentRect.height);
      if (width || height) setSize({ width, height });
    });
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
export function PageCanvas({ page, charts, path, theme, tick, editable, selected, onSelect, onLayout, maxHeight, still, overrides, values, sources, interaction, report }: CanvasProps) {
  // Clicked points and Control values, for this page while it's shown.
  const [picks, setPicks] = useState<Pick[]>([]);
  const [controlValues, setControlValues] = useState<Record<string, string[] | null>>({});
  useEffect(() => (setPicks([]), setControlValues({})), [page.id]);
  useEffect(() => {
    if (!picks.length) return;
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && setPicks([]);
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [picks.length]);
  const mode = report && interaction !== "none" ? "highlight" : interaction || "filter";
  const fieldsOf = (id: string) => sourceFields(sources?.find((s) => s.id === id)).map((f) => f.name);
  const controls = arr(page.items).flatMap((it) => {
    const c = charts.find((x) => x.id === it.chart);
    const field = c?.type === "control" ? arr(c.dimension)[0]?.field : undefined;
    if (!c || !field) return [];
    const type = sourceFields(sources?.find((s) => s.id === c.dataSource)).find((f) => f.name === field)?.type;
    return [{ item: it.id, field, targets: arr(it.targets), filter: controlFilter(field, controlStyle(c, type), controlValues[it.id] ?? null) }];
  });
  const pick = (it: B.Item, c: B.Chart) => (key: unknown, label: string) => {
    const dim = arr(c.dimension)[0];
    if (!dim || still) return;
    setPicks((ps) => {
      const same = ps.find((p) => p.item === it.id);
      const rest = ps.filter((p) => p.item !== it.id);
      // Clicking the selected point again clears it.
      if (same && JSON.stringify(same.key) === JSON.stringify(key)) return rest;
      return [...rest, { item: it.id, chart: c.id, source: c.dataSource, field: dim.field, bucket: dim.aggregate ?? "", key, label }];
    });
  };
  const outer = useSize();
  const inner = useSize();
  const slide = page.size === "16:9";
  const scale = outer.width ? (slide && maxHeight ? Math.min(outer.width / PAGE_WIDTH, maxHeight / SLIDE_HEIGHT) : outer.width / PAGE_WIDTH) : 0;
  const strategy = useMemo(() => createScaledStrategy(scale || 1), [scale]);
  const layout = useMemo(
    () => arr(page.items).map((it) => ({ i: it.id, x: it.x, y: it.y, w: it.w, h: it.h, minW: it.kind === "shape" ? 1 : 2, minH: it.kind === "chart" ? 4 : it.kind === "shape" ? 1 : 2, static: !!it.locked })),
    [page.items],
  );
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
                  {editable && !it.locked && (
                    <div className="bi-tile-grip" title="Drag to move">
                      <GripVertical size={13} />
                    </div>
                  )}
                  {editable && it.locked && (
                    <div className="bi-tile-lock" title="Locked: unlock it in its properties to move or resize it">
                      <Lock size={11} />
                    </div>
                  )}
                  {it.kind === "chart" ? (
                    (() => {
                      const c = charts.find((x) => x.id === it.chart);
                      const r = c ? reach({ item: it.id, chart: c }, picks, controls, mode, fieldsOf) : { filters: [] };
                      return (
                        <ChartTile
                          chartId={it.chart ?? ""}
                          charts={charts}
                          path={path}
                          theme={theme}
                          tick={tick}
                          still={still}
                          overrides={overrides}
                          extra={r.filters}
                          interaction={
                            c
                              ? {
                                  selected: r.highlight,
                                  onPick: pick(it, c),
                                  control: c.type === "control" && !report ? { value: controlValues[it.id] ?? null, onChange: (v) => setControlValues((m) => ({ ...m, [it.id]: v })) } : undefined,
                                }
                              : undefined
                          }
                        />
                      );
                    })()
                  ) : it.kind === "shape" ? (
                    <ShapeTile item={it} theme={theme} />
                  ) : (
                    <TextTile item={it} theme={theme} values={values} />
                  )}
                </div>
              ))}
            </ReactGridLayout>
            {!arr(page.items).length && editable && <div className="bi-canvas-empty">Add charts and text with the toolbar.</div>}
          </div>
        </div>
      )}
      {!!picks.length && (
        <div className="bi-picks" data-testid="bi-picks">
          {picks.map((p) => (
            <span key={p.item} className="bi-pick">
              {mode === "highlight" ? "Highlighting" : "Filtered by"} {p.label}
              <button className="bi-pick-x" title="Clear" aria-label={`Clear ${p.label}`} onClick={() => setPicks((ps) => ps.filter((x) => x.item !== p.item))}>
                ×
              </button>
            </span>
          ))}
        </div>
      )}
    </div>
  );
}

function ChartTile({
  chartId,
  charts,
  path,
  theme,
  tick,
  still,
  overrides,
  extra,
  interaction,
}: {
  chartId: string;
  charts: B.Chart[];
  path: string;
  theme: ChartTheme;
  tick: number;
  still?: boolean;
  overrides?: ParamValues;
  /** Filters from clicks and controls elsewhere on the page. */
  extra?: B.Filter[];
  interaction?: ChartInteraction;
}) {
  const chart = charts.find((c) => c.id === chartId);
  const [data, setData] = useState<B.Data | null>(null);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const key = chart ? JSON.stringify({ ...chart, props: undefined, name: undefined, modified: undefined, overrides, extra }) : "";
  useEffect(() => {
    if (!chart) return;
    let live = true;
    setLoading(true);
    readChart(path, extra?.length ? { ...chart, filters: [...arr(chart.filters), ...extra] } : chart, overrides && Object.keys(overrides).length ? overrides : null, tick)
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
      <ChartView chart={chart} data={data} theme={theme} error={error} loading={loading} hint="The chart has no fields yet." still={still} interaction={interaction} />
    </div>
  );
}

function TextTile({ item, theme, values }: { item: B.Item; theme: ChartTheme; values?: ParamValues }) {
  const t = TOKENS[theme];
  const by = createdByLabel(item.createdBy);
  return (
    <div className={"bi-text-tile theme-" + theme + (by ? " notes" : "")} style={{ background: t.surface, color: t.ink, borderColor: t.border }} data-testid={by ? "bi-notes" : "bi-text"}>
      {by ? (
        // Notes sit centred in their tile, so the grid's leftover space splits above and below.
        <div className="bi-notes-body">
          <div className="bi-notes-head">
            Notes from {by.replace(/^AI \((.*)\)$/, "$1")}
            {item.updated ? ` · ${new Date(item.updated).toLocaleString()}` : ""}
          </div>
          <Markdown text={expandParams(item.text ?? "", values)} />
        </div>
      ) : (
        <Markdown text={expandParams(item.text ?? "", values)} />
      )}
    </div>
  );
}
