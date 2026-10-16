import React from "react";
import ReactDOM from "react-dom/client";
import { Browser } from "@wailsio/runtime";
import App from "./App";
import { setChartReader } from "./components/bi/Canvas";
import { setOpenLink } from "./components/bi/links";
import { BI } from "./api";
import "./styles.css";
import { initTheme } from "./theme";

initTheme();

// Dashboard links open in the system browser, and charts read through Chloe.
setOpenLink((href) => void Browser.OpenURL(href).catch(() => window.open(href, "_blank", "noopener")));
setChartReader((path, chart, params, since) => BI.ChartData(path, chart, params, since));

// ⌘R / F5 must never reload the app (views use ⌘R to run or refresh). Capture
// phase, so it holds even when a view stops the event.
window.addEventListener(
  "keydown",
  (e) => {
    if (((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "r") || e.key === "F5") e.preventDefault();
  },
  true,
);

// A database tool's inputs are names and code: no autocorrect, capitalisation or
// spellcheck suggestions (WebKit offers them on every field by default).
document.addEventListener(
  "focusin",
  (e) => {
    const el = e.target;
    if (!(el instanceof HTMLInputElement || el instanceof HTMLTextAreaElement)) return;
    if (el.dataset.plain) return;
    el.dataset.plain = "1";
    el.setAttribute("autocorrect", "off");
    el.setAttribute("autocapitalize", "off");
    el.setAttribute("autocomplete", "off");
    el.spellcheck = false;
  },
  true,
);

ReactDOM.createRoot(document.getElementById("root") as HTMLElement).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>,
);
