import { Transfer, errorText } from "../../api";
import { ColumnMode, ErdModel, Positions, svgToPNG, toSVG } from "../../erd/model";
import { alert } from "../Dialogs";

/** Save a diagram as SVG or PNG, styled with the current theme. */
export async function exportDiagram(model: ErdModel, positions: Positions, mode: ColumnMode, baseName: string, kind: "svg" | "png") {
  const css = getComputedStyle(document.documentElement);
  const v = (n: string) => css.getPropertyValue(n).trim();
  const svg = toSVG(model, positions, mode, {
    background: v("--bg"), header: v("--bg-3"), headerText: v("--text"), body: v("--bg"), text: v("--text"), muted: v("--text-3"), border: v("--border-strong"), line: v("--text-2"), key: "#c49a2c",
  });
  const path = await Transfer.SaveFileDialog(`${baseName}.${kind}`, kind.toUpperCase(), `*.${kind}`);
  if (!path) return;
  try {
    if (kind === "svg") await Transfer.WriteFile(path, svg, false);
    else await Transfer.WriteFile(path, (await svgToPNG(svg)).split(",")[1], true);
  } catch (e) {
    alert("Export failed", errorText(e));
  }
}
