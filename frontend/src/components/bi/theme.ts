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
