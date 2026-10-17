// Chart designer: the data source's fields on the left, the chart type and shelves
// with a live preview in the middle, and the chart's properties on the right
// (Navicat's chart tab). Fields are dragged onto shelves (or double-clicked);
// each placed field has a menu for its aggregate, value operation and alias.
import { Calendar, ChevronDown, Database, Hash, Presentation, RefreshCw, Save, Type, X } from "lucide-react";
import { useCallback, useEffect, useMemo, useState } from "react";
import { B, BI, BICatalog, arr, errorText } from "../../api";
import { Tab, connById, useApp } from "../../store";
import { DBType } from "../../api";
import { MenuItem, openMenu } from "../ContextMenu";
import { prompt } from "../Dialogs";
import { Split } from "../Split";
import { biChanged, loadCatalog, openObject, sourceFields } from "./actions";
import { ChartView } from "./ChartView";
import { FilterList, cleanFilters } from "./FilterList";
import { ChartTypeIcon } from "./icons";
import { PALETTES } from "./theme";
import { CHART_TYPE_NAMES, connLabel, useWorkspace } from "./WorkspaceView";
import { Toolbar } from "../Toolbar";
import { useSavableTab } from "../useSavableTab";
import { Check } from "../form";

type ShelfName = "dimension" | "series" | "value" | "secondary" | "target";
const SHELVES: ShelfName[] = ["dimension", "series", "value", "target", "secondary"];
const SHELF_LABEL: Record<ShelfName, string> = { dimension: "Dimension", series: "Series", value: "Value", secondary: "Secondary Value", target: "Target" };
const TYPE_ORDER = ["bar", "stacked_bar", "hbar", "stacked_hbar", "waterfall", "tornado", "line", "area", "stacked_area", "bar_line", "stacked_bar_line", "pie", "donut", "treemap", "scatter", "heatmap", "value", "trend", "kpi", "gauge", "table", "pivot", "control"];
const STACKED = new Set(["stacked_bar", "stacked_hbar", "stacked_area", "stacked_bar_line"]);
const LINES = new Set(["line", "area", "stacked_area", "bar_line", "stacked_bar_line"]);
const CARTESIAN = new Set(["bar", "stacked_bar", "hbar", "stacked_hbar", "line", "area", "stacked_area", "bar_line", "stacked_bar_line", "waterfall", "tornado", "scatter", "heatmap"]);
/** Types that draw one figure: no legend, no categories to sort. */
const FIGURES = new Set(["value", "kpi", "gauge", "trend", "control"]);
/** Types whose values can take a running total or a difference. */
const OPERABLE = new Set(["bar", "stacked_bar", "hbar", "stacked_hbar", "line", "area", "stacked_area", "bar_line", "stacked_bar_line", "trend", "table", "pivot"]);

export const AGG_NAMES: Record<string, string> = {
  "": "None",
  sum: "Sum",
  avg: "Average",
  count: "Count",
  count_distinct: "Count (Distinct)",
  min: "Min",
  max: "Max",
  median: "Median",
  year: "Year",
  quarter: "Quarter",
  month: "Month",
  week: "Week",
  day: "Day",
  hour: "Hour",
  minute: "Minute",
  second: "Second",
  quarter_x: "Quarter (Extract)",
  month_x: "Month (Extract)",
  week_x: "Week (Extract)",
  day_x: "Day (Extract)",
  weekday_x: "Weekday (Extract)",
  hour_x: "Hour (Extract)",
  minute_x: "Minute (Extract)",
  second_x: "Second (Extract)",
};

const grouped = (s: ShelfName) => s === "dimension" || s === "series";

export function defaultAggregate(shelf: ShelfName, f: B.Field, raw: boolean) {
  if (grouped(shelf)) return f.type === "datetime" ? "month" : "";
  if (raw) return "";
  return f.type === "number" ? "sum" : "count";
}

export function allowedAggregates(cat: BICatalog, shelf: ShelfName, f: B.Field, raw: boolean, dialect: string) {
  if (grouped(shelf)) return f.type === "datetime" ? ["", ...arr(cat.dateBuckets)] : [""];
  let out = [...arr(cat.valueAggregates?.[f.type])];
  if (dialect !== "postgres") out = out.filter((a) => a !== "median");
  // A table column groups by its plain value or, for dates, by a bucket.
  return raw ? ["", ...(f.type === "datetime" ? arr(cat.dateBuckets) : []), ...out] : out;
}

export function fieldLabel(cf: B.ChartField) {
  if (cf.alias) return cf.alias;
  if (!cf.aggregate) return cf.field;
  return /^(sum|avg|count|count_distinct|min|max|median)$/.test(cf.aggregate) ? `${AGG_NAMES[cf.aggregate]} of ${cf.field}` : `${cf.field} (${AGG_NAMES[cf.aggregate]})`;
}

/** Keep only what a chart type's shelves take. */
export function fitToType(c: B.Chart, t: B.ChartType, fields: B.Field[]): B.Chart {
  const out = { ...c };
  for (const s of SHELVES) out[s] = arr(c[s]).slice(0, t[s].max);
  if (!t.raw) out.value = arr(out.value).map((v) => (v.aggregate ? v : { ...v, aggregate: fields.find((f) => f.name === v.field)?.type === "number" ? "sum" : "count" }));
  if (arr(out.series).length && arr(out.value).length > 1) out.value = arr(out.value).slice(0, 1);
  return out;
}

const queryKey = (c: B.Chart | null) => (c ? JSON.stringify([c.dataSource, c.type, c.dimension, c.series, c.value, c.secondary, c.target, cleanFilters(arr(c.filters)), c.sort, c.top, c.others, c.props?.sameDays]) : "");
const comparable = (c: B.Chart | null) => (c ? JSON.stringify({ ...c, modified: undefined, filters: cleanFilters(arr(c.filters)) }) : "");

export function ChartEditor({ tab }: { tab: Tab }) {
  const path = tab.biPath!;
  const { w } = useWorkspace(path);
  const [cat, setCat] = useState<BICatalog | null>(null);
  const [saved, setSaved] = useState<B.Chart | null>(null);
  const [draft, setDraft] = useState<B.Chart | null>(null);
  const [data, setData] = useState<B.Data | null>(null);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const [saveError, setSaveError] = useState("");
  const [present, setPresent] = useState(false);
  // When Refresh was last asked for (ms): data is read again from then on.
  const [tick, setTick] = useState(0);
  useEffect(() => void loadCatalog().then(setCat), []);

  const dirty = !!draft && comparable(draft) !== comparable(saved);
  useEffect(() => {
    if (!w) return;
    const found = tab.biId ? arr(w.charts).find((c) => c.id === tab.biId) : null;
    if (found) {
      if (!dirty) {
        setSaved(found);
        setDraft(found);
        if (tab.title !== found.name) useApp.getState().updateTab(tab.id, { title: found.name });
      }
      return;
    }
    if (draft) return;
    let n = arr(w.charts).length + 1;
    while (arr(w.charts).some((c) => c.name === `Chart ${n}`)) n++;
    const sources = arr(w.dataSources);
    setDraft({ id: "", name: `Chart ${n}`, dataSource: sources[sources.length - 1]?.id ?? "", type: "bar", props: {} });
  }, [w]); // eslint-disable-line react-hooks/exhaustive-deps

  const src = arr(w?.dataSources).find((s) => s.id === draft?.dataSource) ?? null;
  const fields = useMemo(() => sourceFields(src), [src]);
  const dialect = src?.mode === "archive" ? "sqlite" : connById(src?.connId)?.type === DBType.PostgreSQL ? "postgres" : "mysql";
  const type = cat?.types?.[draft?.type ?? "bar"];
  const theme = w?.theme === "dark" ? "dark" : "light";

  // Read the chart's data whenever what it queries changes.
  const key = queryKey(draft);
  useEffect(() => {
    if (!draft?.dataSource) return;
    let live = true;
    setLoading(true);
    const t = setTimeout(() => {
      BI.ChartData(path, { ...draft, filters: cleanFilters(arr(draft.filters)) }, null, tick)
        .then((d) => live && (setData(d), setError("")))
        .catch((e) => live && (setError(errorText(e)), setData(null)))
        .finally(() => live && setLoading(false));
    }, 250);
    return () => {
      live = false;
      clearTimeout(t);
    };
  }, [key, path, tick]); // eslint-disable-line react-hooks/exhaustive-deps

  const update = (patch: Partial<B.Chart>) => setDraft((d) => (d ? { ...d, ...patch } : d));
  const props = draft?.props ?? {};
  const setProps = (patch: Partial<B.Props>) => update({ props: { ...props, ...patch } });

  const save = useCallback(async (): Promise<boolean> => {
    if (!draft) return false;
    setSaveError("");
    try {
      const s = await BI.SaveChart(path, { ...draft, filters: cleanFilters(arr(draft.filters)) });
      setSaved(s);
      setDraft(s);
      useApp.getState().updateTab(tab.id, { biId: s.id, title: s.name, dirty: false });
      biChanged(path);
      return true;
    } catch (e) {
      setSaveError(errorText(e));
      return false;
    }
  }, [draft, path, tab.id]);
  useSavableTab(tab.id, dirty, save);

  if (!draft || !cat || !type) return <div className="center-hint">Loading…</div>;

  const addField = (shelf: ShelfName, name: string) => {
    const f = fields.find((x) => x.name === name);
    if (!f || type[shelf].max === 0) return;
    const cf: B.ChartField = { field: name, aggregate: defaultAggregate(shelf, f, !!type.raw && shelf === "value") };
    const cur = arr(draft[shelf]);
    let next = cur.length >= type[shelf].max ? [...cur.slice(0, type[shelf].max - 1), cf] : [...cur, cf];
    if (shelf === "value" && arr(draft.series).length) next = [cf];
    const patch: Partial<B.Chart> = { [shelf]: next };
    if (shelf === "series" && arr(draft.value).length > 1) patch.value = arr(draft.value).slice(0, 1);
    update(patch);
  };
  /** Double-click: the first shelf that wants this kind of field. */
  const autoAdd = (f: B.Field) => {
    const order: ShelfName[] = f.type === "number" && type.value.max > 0 ? ["value", "target", "dimension", "series", "secondary"] : ["dimension", "series", "value", "target", "secondary"];
    const shelf = order.find((s) => type[s].max > arr(draft[s]).length) ?? order.find((s) => type[s].max > 0);
    if (shelf) addField(shelf, f.name);
  };
  const setField = (shelf: ShelfName, i: number, patch: Partial<B.ChartField>) => update({ [shelf]: arr(draft[shelf]).map((x, j) => (j === i ? { ...x, ...patch } : x)) });
  const removeField = (shelf: ShelfName, i: number) => update({ [shelf]: arr(draft[shelf]).filter((_, j) => j !== i) });
  const pillMenu = (shelf: ShelfName, i: number, cf: B.ChartField): MenuItem[] => {
    const f = fields.find((x) => x.name === cf.field);
    const aggs = f ? allowedAggregates(cat, shelf, f, !!type.raw && shelf === "value", dialect) : [];
    const items: MenuItem[] = [];
    if (aggs.length > 1) items.push({ label: grouped(shelf) ? "Group By" : "Aggregate", submenu: aggs.map((a) => ({ label: AGG_NAMES[a] ?? a, checked: (cf.aggregate ?? "") === a, onClick: () => setField(shelf, i, { aggregate: a }) })) });
    if (!grouped(shelf) && !type.raw && shelf === "value" && OPERABLE.has(draft.type))
      items.push({
        label: "Value Operation",
        submenu: [
          { label: "None", checked: !cf.op, onClick: () => setField(shelf, i, { op: "" }) },
          { label: "Cumulative", checked: cf.op === "cumulative", onClick: () => setField(shelf, i, { op: "cumulative" }) },
          { label: "Subtract", checked: cf.op === "subtract", onClick: () => setField(shelf, i, { op: "subtract" }) },
        ],
      });
    items.push({
      label: "Set Alias…",
      onClick: async () => {
        const a = await prompt("Set Alias", { message: `Label for ${cf.field}`, defaultValue: cf.alias ?? "" });
        if (a !== null) setField(shelf, i, { alias: a.trim() });
      },
    });
    if (grouped(shelf) && shelf === "dimension")
      items.push({
        label: "Sort",
        submenu: [
          { label: "Default", checked: !draft.sort, onClick: () => update({ sort: null }) },
          { label: "Ascending", checked: draft.sort?.by === "dimension" && !draft.sort.desc, onClick: () => update({ sort: { by: "dimension" } }) },
          { label: "Descending", checked: draft.sort?.by === "dimension" && !!draft.sort.desc, onClick: () => update({ sort: { by: "dimension", desc: true } }) },
        ],
      });
    items.push({ separator: true }, { label: "Remove", onClick: () => removeField(shelf, i) });
    return items;
  };

  const chartForView = { ...draft, props };
  const asOf = data?.ranAt ? new Date(data.ranAt).toLocaleString() : "";

  return (
    <div
      className="bi-chart-editor"
      onKeyDown={(e) => {
        const meta = e.metaKey || e.ctrlKey;
        if (meta && e.key.toLowerCase() === "s") (e.preventDefault(), save());
        if (meta && e.key.toLowerCase() === "r") (e.preventDefault(), setTick(Date.now()));
        if (meta && e.key === "Enter") (e.preventDefault(), setPresent(true));
      }}
      tabIndex={-1}
    >
      <Toolbar>
        <button onClick={save} className={dirty ? "accent" : ""} title="Save (⌘S)">
          <Save size={14} /> Save
        </button>
        <button onClick={() => setTick(Date.now())} title="Refresh Data (⌘R)">
          <RefreshCw size={14} className={loading ? "spin" : ""} /> Refresh
        </button>
        <button onClick={() => setPresent(true)} title="Present (⌘↵)">
          <Presentation size={14} /> Present
        </button>
        <span className="tb-sep" />
        <label className="tb-label">
          Data Source{" "}
          <select aria-label="Data source" value={draft.dataSource} onChange={(e) => update({ dataSource: e.target.value })}>
            {!src && <option value={draft.dataSource}>(deleted data source)</option>}
            {arr(w?.dataSources).map((s) => (
              <option key={s.id} value={s.id}>
                {s.name}
              </option>
            ))}
          </select>
        </label>
        {src && (
          <button onClick={() => openObject(path, "source", src.id, src.name)} title="Design the data source">
            <Database size={14} />
          </button>
        )}
        <span className="spacer" />
        <span className="muted bi-asof">{data?.complete ? `${data.mode === "archive" ? "Snapshot" : "Live"} · ${asOf}` : src ? connLabel(src) : ""}</span>
      </Toolbar>
      {saveError && <div className="error-banner">{saveError}</div>}
      <Split
        id="bi-chart-fields"
        className="objects-split"
        firstSize={210}
        minFirst={150}
        maxFirst="40"
        first={
          <div className="bi-fields">
            <div className="pane-title">Fields</div>
            {fields.map((f) => (
              <div
                key={f.name}
                className="bi-field"
                draggable
                onDragStart={(e) => {
                  e.dataTransfer.setData("application/x-chloe-field", f.name);
                  e.dataTransfer.effectAllowed = "copy";
                }}
                onDoubleClick={() => autoAdd(f)}
                title={`${f.name} — ${f.type}${f.dbType ? ` (${f.dbType})` : ""}. Drag onto a shelf, or double-click.`}
              >
                <span className={"bi-type " + f.type}>{f.type === "number" ? <Hash size={12} /> : f.type === "datetime" ? <Calendar size={12} /> : <Type size={12} />}</span>
                {f.name}
              </div>
            ))}
            {!fields.length && <div className="center-hint small">{src ? "The data source has no fields yet. Open it and Apply and Refresh Data." : "Pick a data source."}</div>}
          </div>
        }
        second={
          <Split
            id="bi-chart-props"
            className="objects-split"
            secondSize={290}
            minFirst={320}
            minSecond={200}
            maxSecond="45"
            first={
              <div className="bi-chart-center">
                <div className="bi-types" role="radiogroup" aria-label="Chart type">
                  {TYPE_ORDER.map((t) => (
                    <button key={t} role="radio" aria-checked={draft.type === t} aria-label={CHART_TYPE_NAMES[t]} title={CHART_TYPE_NAMES[t]} className={draft.type === t ? "on" : ""} onClick={() => setDraft(fitToType({ ...draft, type: t }, cat.types![t]!, fields))}>
                      <ChartTypeIcon type={t} size={17} />
                    </button>
                  ))}
                </div>
                <div className="bi-shelves">
                  {SHELVES.filter((s) => type[s].max > 0).map((s) => (
                    <div
                      key={s}
                      className="bi-shelf"
                      data-shelf={s}
                      onDragOver={(e) => {
                        if (e.dataTransfer.types.includes("application/x-chloe-field")) {
                          e.preventDefault();
                          e.currentTarget.classList.add("over");
                        }
                      }}
                      onDragLeave={(e) => e.currentTarget.classList.remove("over")}
                      onDrop={(e) => {
                        e.preventDefault();
                        e.currentTarget.classList.remove("over");
                        addField(s, e.dataTransfer.getData("application/x-chloe-field"));
                      }}
                    >
                      <div className="bi-shelf-label">
                        {type[s].label || SHELF_LABEL[s]}
                        {type[s].min > 0 && arr(draft[s]).length < type[s].min && <span className="bi-need"> required</span>}
                      </div>
                      <div className="bi-shelf-fields">
                        {arr(draft[s]).map((cf, i) => (
                          <span key={i} className={"bi-pill " + (grouped(s) ? "dim" : "val")} onClick={(e) => openMenu(e, pillMenu(s, i, cf))} title="Click for options">
                            {fieldLabel(cf)}
                            {cf.op ? ` · ${cf.op === "cumulative" ? "Cumulative" : "Subtract"}` : ""}
                            <ChevronDown size={11} />
                            <button
                              className="bi-pill-x"
                              title="Remove"
                              onClick={(e) => {
                                e.stopPropagation();
                                removeField(s, i);
                              }}
                            >
                              <X size={11} />
                            </button>
                          </span>
                        ))}
                        {arr(draft[s]).length < type[s].max && <span className="bi-drop">Drag Fields Here</span>}
                      </div>
                    </div>
                  ))}
                </div>
                <div className="bi-preview-area">
                  <ChartView chart={chartForView} data={data} theme={theme} error={error} loading={loading} />
                </div>
              </div>
            }
            second={<PropsPane draft={draft} props={props} setProps={setProps} update={update} fields={fields} />}
          />
        }
      />
      {present && <PresentOverlay chart={chartForView} data={data} theme={theme} error={error} onClose={() => setPresent(false)} />}
    </div>
  );
}

function PresentOverlay({ chart, data, theme, error, onClose }: { chart: B.Chart; data: B.Data | null; theme: "light" | "dark"; error: string; onClose(): void }) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);
  return (
    <div className={"bi-present theme-" + theme} onDoubleClick={onClose} data-testid="bi-present">
      <ChartView chart={chart} data={data} theme={theme} error={error} />
      <div className="bi-present-hint">Press Esc to exit</div>
    </div>
  );
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section className="bi-prop-section">
      <div className="bi-prop-title">{title}</div>
      {children}
    </section>
  );
}

function PropsPane({ draft, props, setProps, update, fields }: { draft: B.Chart; props: B.Props; setProps(p: Partial<B.Props>): void; update(p: Partial<B.Chart>): void; fields: B.Field[] }) {
  const num = props.number ?? {};
  const setNum = (p: Partial<B.NumberFormat>) => setProps({ number: { ...num, ...p } });
  const t = draft.type;
  const pie = t === "pie" || t === "donut";
  const sortValue = !draft.sort ? "" : `${draft.sort.by}:${draft.sort.desc ? "desc" : "asc"}:${draft.sort.field ?? ""}`;
  const table = t === "table";
  // Value fields a sort can name: a table's aggregated columns, a chart's values.
  const sortable = [...arr(draft.value), ...arr(draft.secondary)].filter((v) => !table || v.aggregate);
  const multi = sortable.length > 1;
  return (
    <div className="bi-props-pane">
      <div className="pane-title">Properties</div>
      <Section title="General">
        <label className="bi-prop">
          Name
          <input aria-label="Chart name" value={draft.name} onChange={(e) => update({ name: e.target.value })} />
        </label>
        <Check label="Show title" checked={!props.hideTitle} onChange={(v) => setProps({ hideTitle: !v })} />
        <label className="bi-prop">
          Title
          <input aria-label="Chart title" placeholder={draft.name} value={props.title ?? ""} onChange={(e) => setProps({ title: e.target.value })} />
        </label>
        <label className="bi-prop">
          Remarks
          <input aria-label="Remarks" value={draft.remarks ?? ""} onChange={(e) => update({ remarks: e.target.value })} />
        </label>
      </Section>
      {!FIGURES.has(t) && t !== "table" && t !== "pivot" && t !== "treemap" && t !== "heatmap" && t !== "waterfall" && (
        <Section title="Legend">
          <label className="bi-prop">
            Position
            <select aria-label="Legend" value={props.legend || "auto"} onChange={(e) => setProps({ legend: e.target.value })}>
              <option value="auto">Automatic</option>
              <option value="top">Top</option>
              <option value="bottom">Bottom</option>
              <option value="right">Right</option>
              <option value="none">Hidden</option>
            </select>
          </label>
        </Section>
      )}
      {t === "control" && (
        <Section title="Control">
          <label className="bi-prop">
            Style
            <select aria-label="Control style" value={props.control ?? ""} onChange={(e) => setProps({ control: e.target.value })}>
              <option value="">Automatic (a range for numbers and dates)</option>
              <option value="list">List (pick several)</option>
              <option value="combo">Drop-down (pick one)</option>
              <option value="range">Range</option>
            </select>
          </label>
          <p className="hint">On a dashboard, a Control filters the charts it targets (set on its tile), or every chart on the page with its field.</p>
        </Section>
      )}
      {(t === "kpi" || t === "gauge" || t === "trend") && (
        <Section title={t === "gauge" ? "Range" : t === "kpi" ? "Target" : "Change"}>
          {t === "kpi" && !arr(draft.target).length && (
            <label className="bi-prop">
              Target value
              <input aria-label="Target value" type="number" value={props.goal ?? ""} onChange={(e) => setProps({ goal: e.target.value === "" ? null : Number(e.target.value) })} />
            </label>
          )}
          {t === "gauge" && (
            <div className="bi-prop-row">
              <label className="bi-prop">
                Minimum
                <input aria-label="Minimum" type="number" placeholder="0" value={props.min ?? ""} onChange={(e) => setProps({ min: e.target.value === "" ? null : Number(e.target.value) })} />
              </label>
              <label className="bi-prop">
                Maximum
                <input aria-label="Maximum" type="number" placeholder={arr(draft.target).length ? "Target field" : "auto"} value={props.max ?? ""} onChange={(e) => setProps({ max: e.target.value === "" ? null : Number(e.target.value) })} />
              </label>
            </div>
          )}
          {t !== "gauge" && (
            <Check label="Lower is better" checked={!!props.lowerIsBetter} onChange={(v) => setProps({ lowerIsBetter: v })} />
          )}
          {t === "trend" && (
            <label className="check" title="While the latest week, month, quarter or year is still going, compare it with the same days of the one before">
              <input type="checkbox" checked={!!props.sameDays} onChange={(e) => setProps({ sameDays: e.target.checked })} /> Compare the same days of the previous period
            </label>
          )}
        </Section>
      )}
      {!FIGURES.has(t) && (
        <Section title="Data">
          {t !== "table" && t !== "pivot" && (
            <label className="check">
              <input type="checkbox" aria-label="Show data labels" checked={!!props.labels} onChange={(e) => setProps({ labels: e.target.checked })} /> Show data labels
            </label>
          )}
          {pie && (
            <Check label="Labels as percentages" checked={!!props.labelPercent} onChange={(v) => setProps({ labelPercent: v })} />
          )}
          {STACKED.has(t) && (
            <Check label="Use 100% stacked" checked={!!props.stack100} onChange={(v) => setProps({ stack100: v })} />
          )}
          {LINES.has(t) && (
            <Check label="Smooth lines" checked={!!props.smooth} onChange={(v) => setProps({ smooth: v })} />
          )}
          {(
            <>
              <label className="bi-prop">
                Sort
                <select
                  aria-label="Sort"
                  value={sortValue}
                  onChange={(e) => {
                    const [by, dir, field] = e.target.value.split(":");
                    update({ sort: by ? { by, desc: dir === "desc", ...(field ? { field } : {}) } : null });
                  }}
                >
                  <option value="">{table ? "As read" : "Automatic"}</option>
                  <option value="dimension:asc:">{table ? "By first column, ascending" : "By dimension, ascending"}</option>
                  <option value="dimension:desc:">{table ? "By first column, descending" : "By dimension, descending"}</option>
                  {(multi ? sortable : sortable.slice(0, 1)).flatMap((v) => {
                    const name = multi ? fieldLabel(v) : "value";
                    const f = multi ? v.field : "";
                    return [
                      <option key={"d" + v.field + v.aggregate} value={`value:desc:${f}`}>{`By ${name}, largest first`}</option>,
                      <option key={"a" + v.field + v.aggregate} value={`value:asc:${f}`}>{`By ${name}, smallest first`}</option>,
                    ];
                  })}
                  {!!draft.sort?.field && !sortable.some((v) => v.field === draft.sort?.field) && <option value={sortValue}>{`By ${draft.sort.field}`}</option>}
                </select>
              </label>
              <label className="bi-prop">
                Show only top
                <input aria-label="Top" type="number" min={0} placeholder="all" value={draft.top || ""} onChange={(e) => update({ top: Math.max(0, Number(e.target.value) || 0) })} />
              </label>
              {!!draft.top && !table && (
                <Check label="Group the rest as Others" checked={!!draft.others} onChange={(v) => update({ others: v })} />
              )}
            </>
          )}
        </Section>
      )}
      <Section title="Number Format">
        <div className="bi-prop-row">
          <label className="bi-prop">
            Prefix
            <input aria-label="Prefix" value={num.prefix ?? ""} onChange={(e) => setNum({ prefix: e.target.value })} />
          </label>
          <label className="bi-prop">
            Suffix
            <input aria-label="Suffix" value={num.suffix ?? ""} onChange={(e) => setNum({ suffix: e.target.value })} />
          </label>
        </div>
        <div className="bi-prop-row">
          <label className="bi-prop">
            Unit
            <select aria-label="Unit" value={num.unit ?? ""} onChange={(e) => setNum({ unit: e.target.value })}>
              <option value="">None</option>
              <option value="auto">Automatic</option>
              <option value="K">Thousands (K)</option>
              <option value="M">Millions (M)</option>
              <option value="B">Billions (B)</option>
            </select>
          </label>
          <label className="bi-prop">
            Decimals
            <select aria-label="Decimals" value={num.decimals ?? ""} onChange={(e) => setNum({ decimals: e.target.value === "" ? null : Number(e.target.value) })}>
              <option value="">Automatic</option>
              {[0, 1, 2, 3, 4].map((d) => (
                <option key={d} value={d}>
                  {d}
                </option>
              ))}
            </select>
          </label>
        </div>
        <Check label="Thousands separator" checked={!num.noSeparator} onChange={(v) => setNum({ noSeparator: !v })} />
      </Section>
      {!FIGURES.has(t) && (
        <Section title="Labels">
          <label className="bi-prop">
            Category values
            <select aria-label="Label case" value={props.labelCase ?? ""} onChange={(e) => setProps({ labelCase: e.target.value })}>
              <option value="">Readable (ended_early → Ended Early)</option>
              <option value="raw">As stored</option>
            </select>
          </label>
        </Section>
      )}
      <Section title="Date Format">
        <label className="bi-prop">
          Custom
          <input aria-label="Date format" placeholder="Automatic (e.g. MMM YYYY)" value={props.dateFormat ?? ""} onChange={(e) => setProps({ dateFormat: e.target.value })} title="D DD M MM MMM MMMM YY YYYY h hh H HH m mm S SS wd Wd WD w WW q QQ p" />
        </label>
      </Section>
      {!FIGURES.has(t) && t !== "table" && t !== "pivot" && t !== "heatmap" && t !== "waterfall" && (
        <Section title="Colors">
          <label className="bi-prop">
            Palette
            <select aria-label="Palette" value={props.palette || "chloe"} onChange={(e) => setProps({ palette: e.target.value })}>
              {Object.entries(PALETTES).map(([k, p]) => (
                <option key={k} value={k}>
                  {p.label}
                </option>
              ))}
            </select>
          </label>
        </Section>
      )}
      {CARTESIAN.has(t) && (
        <Section title="Axes">
          <label className="bi-prop">
            X-axis title
            <input aria-label="X-axis title" value={props.xTitle ?? ""} onChange={(e) => setProps({ xTitle: e.target.value })} />
          </label>
          <label className="bi-prop">
            Y-axis title
            <input aria-label="Y-axis title" value={props.yTitle ?? ""} onChange={(e) => setProps({ yTitle: e.target.value })} />
          </label>
        </Section>
      )}
      <Section title="Data Filter">
        <FilterList fields={fields} filters={arr(draft.filters)} onChange={(filters) => update({ filters })} />
      </Section>
    </div>
  );
}
