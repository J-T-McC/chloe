import { useEffect, useState } from "react";
import { arr } from "../arr";
import { PageCanvas } from "../components/bi/Canvas";
import { TOKENS } from "../components/bi/theme";
import type { ReportPayload } from "./payload";

const pageName = (r: ReportPayload, i: number) => r.pages[i]?.name || `Page ${i + 1}`;

/** Pages one at a time with tabs (← → change page), or every page in a column. */
export function Viewer({ report }: { report: ReportPayload }) {
  const [page, setPage] = useState(0);
  const [all, setAll] = useState(false);
  const [height, setHeight] = useState(window.innerHeight);
  const t = TOKENS[report.theme];
  const pages = arr(report.pages);
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (all || (e.target as HTMLElement).closest("input, select, textarea")) return;
      if (e.key === "ArrowRight") setPage((p) => Math.min(p + 1, pages.length - 1));
      if (e.key === "ArrowLeft") setPage((p) => Math.max(p - 1, 0));
    };
    const onResize = () => setHeight(window.innerHeight);
    window.addEventListener("keydown", onKey);
    window.addEventListener("resize", onResize);
    return () => {
      window.removeEventListener("keydown", onKey);
      window.removeEventListener("resize", onResize);
    };
  }, [all, pages.length]);
  useEffect(() => {
    document.title = report.name;
  }, [report.name]);
  const canvas = (i: number) => (
    <section key={pages[i].id} className="report-page" aria-label={pageName(report, i)}>
      {all && pages.length > 1 && <h2 className="report-page-name">{pageName(report, i)}</h2>}
      <PageCanvas
        page={pages[i]}
        charts={arr(report.charts)}
        path=""
        theme={report.theme}
        tick={0}
        values={report.values}
        sources={arr(report.sources)}
        interaction={report.interaction}
        maxHeight={all ? undefined : height - 72}
        report
      />
    </section>
  );
  return (
    <div className={"report theme-" + report.theme} style={{ background: t.plane, color: t.ink }}>
      <header className="report-bar" style={{ background: t.surface, borderColor: t.border }}>
        <div className="report-title">
          <h1>{report.name}</h1>
          <span className="report-meta">
            {report.workspace} · data as of {new Date(report.asOf).toLocaleString()}
            {report.params.map((p) => (
              <span key={p.name} className="report-param" style={{ borderColor: t.border }}>
                {p.name}: <b>{p.value}</b>
              </span>
            ))}
          </span>
        </div>
        {pages.length > 1 && (
          <nav className="report-tabs" aria-label="Pages">
            {pages.map((_, i) => (
              <button key={i} className={!all && i === page ? "on" : ""} aria-current={!all && i === page ? "page" : undefined} onClick={() => (setAll(false), setPage(i))}>
                {pageName(report, i)}
              </button>
            ))}
            <button className={all ? "on" : ""} onClick={() => setAll(true)} title="Every page, one after another (for reading or printing)">
              All pages
            </button>
          </nav>
        )}
      </header>
      <main className="report-body">{all ? pages.map((_, i) => canvas(i)) : pages[page] && canvas(page)}</main>
    </div>
  );
}
