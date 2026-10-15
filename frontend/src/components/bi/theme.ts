// Chart themes. A workspace has its own Light or Dark theme, independent of
// Chloe's appearance, so a report looks the same wherever it's shown. The default
// palette is a colour-blind-checked categorical order (stepped separately for each
// surface); hues are assigned in that fixed order, never cycled.

export type ChartTheme = "light" | "dark";

export interface ThemeTokens {
  surface: string;
  plane: string;
  ink: string;
  ink2: string;
  muted: string;
  grid: string;
  axis: string;
  border: string;
}

export const TOKENS: Record<ChartTheme, ThemeTokens> = {
  light: { surface: "#fcfcfb", plane: "#f9f9f7", ink: "#0b0b0b", ink2: "#52514e", muted: "#898781", grid: "#e1e0d9", axis: "#c3c2b7", border: "rgba(11,11,11,0.10)" },
  dark: { surface: "#1a1a19", plane: "#0d0d0d", ink: "#ffffff", ink2: "#c3c2b7", muted: "#898781", grid: "#2c2c2a", axis: "#383835", border: "rgba(255,255,255,0.10)" },
};

/** Categorical palettes by name, per theme. "chloe" is the default. */
export const PALETTES: Record<string, { label: string; light: string[]; dark: string[] }> = {
  chloe: {
    label: "Chloe",
    light: ["#2a78d6", "#eb6834", "#1baf7a", "#eda100", "#e87ba4", "#008300", "#4a3aa7", "#e34948"],
    dark: ["#3987e5", "#d95926", "#199e70", "#c98500", "#d55181", "#008300", "#9085e9", "#e66767"],
  },
  blue: {
    label: "Blues",
    light: ["#1c5cab", "#3987e5", "#86b6ef", "#104281", "#5598e7", "#b7d3f6", "#256abf", "#6da7ec"],
    dark: ["#5598e7", "#9ec5f4", "#2a78d6", "#cde2fb", "#3987e5", "#86b6ef", "#6da7ec", "#b7d3f6"],
  },
};

/** Colours for a chart's series, in order: its own colours first, then the palette. */
export function seriesColors(theme: ChartTheme, palette?: string, own?: string[] | null): string[] {
  const p = (PALETTES[palette ?? ""] ?? PALETTES.chloe)[theme];
  const mine = (own ?? []).filter((c) => /^#[0-9a-f]{3,8}$/i.test(c));
  return [...mine, ...p.slice(mine.length)];
}

/** "Others" (Top N's remainder) is neutral, so it never reads as one more series. */
export const OTHERS_COLOR: Record<ChartTheme, string> = { light: "#b5b3ab", dark: "#5c5b56" };

export const FONT = 'system-ui, -apple-system, "Segoe UI", sans-serif';

/** Polarity (waterfall rises and falls): the diverging pair's poles, and a
 *  neutral for totals. */
export const POLARITY: Record<ChartTheme, { up: string; down: string; total: string }> = {
  light: { up: "#2a78d6", down: "#e34948", total: "#898781" },
  dark: { up: "#3987e5", down: "#e66767", total: "#898781" },
};

/** Magnitude (heatmap cells): one hue, near the surface for small values. */
export const SEQUENTIAL: Record<ChartTheme, string[]> = {
  light: ["#cde2fb", "#9ec5f4", "#6da7ec", "#3987e5", "#256abf", "#104281"],
  dark: ["#0d366b", "#184f95", "#256abf", "#3987e5", "#6da7ec", "#b7d3f6"],
};

/** A meter's fill and its track: a lighter step of the same ramp. */
export const METER: Record<ChartTheme, { fill: string; track: string }> = {
  light: { fill: "#2a78d6", track: "#cde2fb" },
  dark: { fill: "#3987e5", track: "#184f95" },
};

/** Status colours are reserved for good and bad, and always come with an arrow
 *  or icon and a sign, never colour alone. */
export const STATUS = { good: "#0ca30c", critical: "#d03b3b" };

/** Text that reads on a fill: dark ink on light fills, white on dark ones. */
export function inkOn(hex: string): string {
  const m = /^#?([0-9a-f]{6})$/i.exec(hex);
  if (!m) return "#ffffff";
  const n = parseInt(m[1], 16);
  const lum = (c: number) => {
    const x = c / 255;
    return x <= 0.03928 ? x / 12.92 : ((x + 0.055) / 1.055) ** 2.4;
  };
  const l = 0.2126 * lum((n >> 16) & 255) + 0.7152 * lum((n >> 8) & 255) + 0.0722 * lum(n & 255);
  return l > 0.4 ? "#0b0b0b" : "#ffffff";
}
