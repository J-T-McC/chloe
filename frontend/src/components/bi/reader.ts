// How charts read their data: through Chloe in the app, from embedded data in an HTML
// report. Its own module so the app's entry can set it without loading the canvas.
import type { B } from "../../api";
import type { ParamValues } from "./params";

/** Reads what a chart draws: since is the last Refresh (ms), 0 for any recent data. */
export type ChartReader = (path: string, chart: B.Chart, params: ParamValues | null, since: number) => Promise<B.Data>;

export let readChart: ChartReader = () => Promise.reject(new Error("no chart reader"));

export function setChartReader(r: ChartReader) {
  readChart = r;
}
