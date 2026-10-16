// Draws dashboard pages off screen and captures them as PNGs: for Export in the
// designer, and when an agent asks over MCP (export_dashboard). Pages are drawn
// at their layout size (1280px wide) without animation, once every chart has its
// data, and captured at the requested pixel ratio.
import { toPng } from "html-to-image";
import { useEffect, useRef, useState } from "react";
import { create } from "zustand";
import { B, BI, Events, arr, errorText } from "../../api";
import { PAGE_WIDTH, PageCanvas } from "./Canvas";
import { buildReport } from "./reportExport";
import { ParamValues, paramValues } from "./Parameters";
import { ChartTheme } from "./theme";

interface Job {
  path: string;
  dashboard: B.Dashboard;
  charts: B.Chart[];
  theme: ChartTheme;
  pages: number[];
  scale: number;
  overrides: ParamValues;
  values: ParamValues;
  progress?: RenderProgress;
  done(pngs: string[] | null, error?: string): void;
}

/** Follows a render: told as each page is captured, and asked whether to stop. */
export interface RenderProgress {
  page?(done: number, total: number): void;
  cancelled?(): boolean;
  /** Draw with data read at or after this time (ms); 0 takes recent data. */
  since?: number;
}

// A page that can't be captured shouldn't hold up every export after it.
const CAPTURE_TIMEOUT = 60_000;

/** html-to-image rejects with a DOM Event when the browser can't decode the page
 *  it drew (usually a page too tall to capture at this scale). */
function captureError(e: unknown, page: number, total: number): string {
  const where = total > 1 ? `Page ${page + 1}` : "The page";
  if (typeof Event !== "undefined" && e instanceof Event) return `${where} couldn't be captured; it may be too tall to export at this size`;
  return `${where}: ${errorText(e)}`;
}

const useJobs = create<{ queue: Job[] }>(() => ({ queue: [] }));

/** Render pages (0-based) of a dashboard: the saved one by id, or a draft, with
 *  parameter values changed for this session if any. */
export async function renderPages(path: string, dashboard: B.Dashboard | string, pages: number[], scale: number, overrides: ParamValues = {}, progress?: RenderProgress): Promise<string[]> {
  const w = await BI.Open(path);
  const d = typeof dashboard === "string" ? arr(w.dashboards).find((x) => x.id === dashboard) : dashboard;
  if (!d) throw new Error("the dashboard is no longer in the workspace");
  return new Promise((resolve, reject) => {
    const job: Job = {
      path,
      dashboard: d,
      charts: arr(w.charts),
      theme: w.theme === "dark" ? "dark" : "light",
      pages,
      scale,
      overrides,
      values: paramValues(w.parameters, overrides),
      progress,
      done: (pngs, error) => (pngs ? resolve(pngs) : reject(new Error(error ?? "rendering failed"))),
    };
    useJobs.setState((s) => ({ queue: [...s.queue, job] }));
  });
}

const wait = (ms: number) => new Promise((r) => setTimeout(r, ms));
const frame = () => new Promise((r) => requestAnimationFrame(() => r(null)));

export function ExportHost() {
  const job = useJobs((s) => s.queue[0]);
  const [index, setIndex] = useState(0);
  const stage = useRef<HTMLDivElement>(null);
  const results = useRef<string[]>([]);

  // Agents ask over MCP.
  useEffect(
    () =>
      Events.On("bi:render", (ev) => {
        const r = ev.data;
        if (r.format === "html") {
          buildReport(r.path, r.dashboard, { pages: arr(r.pages), overrides: (r.params ?? {}) as Record<string, string>, since: r.since || 0 })
            .then((html) => BI.Rendered(r.id, [html], ""))
            .catch((e) => BI.Rendered(r.id, [], errorText(e)));
          return;
        }
        renderPages(r.path, r.dashboard, arr(r.pages), r.scale || 1, (r.params ?? {}) as Record<string, string>, { since: r.since || 0 })
          .then((pngs) => BI.Rendered(r.id, pngs, ""))
          .catch((e) => BI.Rendered(r.id, [], errorText(e)));
      }),
    [],
  );

  // Capture the page on stage once its charts are drawn, then move on.
  useEffect(() => {
    if (!job) return;
    let live = true;
    (async () => {
      try {
        const started = Date.now();
        for (;;) {
          await wait(120);
          if (!live) return;
          if (job.progress?.cancelled?.()) throw new Error("cancelled");
          const el = stage.current?.querySelector(".bi-canvas-frame");
          const busy = stage.current?.querySelector("[data-state=loading]");
          if (el && !busy && (el as HTMLElement).offsetHeight > 0) break;
          if (Date.now() - started > 20_000) throw new Error("the charts took too long to load");
        }
        await frame();
        await frame();
        const el = stage.current!.querySelector(".bi-canvas-frame") as HTMLElement;
        let png: string;
        try {
          png = await Promise.race([
            toPng(el, { pixelRatio: job.scale, cacheBust: false, width: el.offsetWidth, height: el.offsetHeight }),
            wait(CAPTURE_TIMEOUT).then(() => Promise.reject(new Error("capturing the page took too long"))),
          ]);
        } catch (e) {
          throw new Error(captureError(e, index, job.pages.length));
        }
        if (!live) return;
        results.current.push(png);
        job.progress?.page?.(index + 1, job.pages.length);
        if (index + 1 < job.pages.length) {
          setIndex(index + 1);
          return;
        }
        job.done(results.current);
      } catch (e) {
        job.done(null, errorText(e));
      }
      results.current = [];
      setIndex(0);
      useJobs.setState((s) => ({ queue: s.queue.slice(1) }));
    })();
    return () => {
      live = false;
    };
  }, [job, index]);

  if (!job) return null;
  const page = arr(job.dashboard.pages)[job.pages[index]];
  if (!page) {
    job.done(null, `no page ${job.pages[index] + 1}`);
    useJobs.setState((s) => ({ queue: s.queue.slice(1) }));
    return null;
  }
  return (
    <div className="bi-export-stage" ref={stage} style={{ width: PAGE_WIDTH }} aria-hidden>
      <PageCanvas key={`${job.dashboard.id}/${index}`} page={page} charts={job.charts} path={job.path} theme={job.theme} tick={job.progress?.since ?? 0} still overrides={job.overrides} values={job.values} />
    </div>
  );
}
