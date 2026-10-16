// The HTML report viewer: a self-contained page that draws an exported
// dashboard with Chloe's own canvas and charts, from the data it carries.
import ReactDOM from "react-dom/client";
import "../styles.css";
import "./report.css";
import { setChartReader } from "../components/bi/Canvas";
import { PAYLOAD_ID, ReportPayload } from "./payload";
import { Viewer } from "./Viewer";

const el = document.getElementById(PAYLOAD_ID);
const payload = JSON.parse(el?.textContent || "null") as ReportPayload | null;

if (payload) {
  setChartReader((_path, chart) => {
    const d = payload.data[chart.id];
    if (!d) return Promise.reject(new Error("This chart wasn't exported."));
    if ("error" in d) return Promise.reject(new Error(d.error));
    return Promise.resolve(d);
  });
  ReactDOM.createRoot(document.getElementById("root") as HTMLElement).render(<Viewer report={payload} />);
}
