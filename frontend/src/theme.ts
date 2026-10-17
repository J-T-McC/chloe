// Appearance: follow the system, or force light or dark. The choice is cached in
// localStorage for the first paint and stored with the app's settings.
import { Layouts } from "./api";
import { safeStorage } from "./prefs";

export type ThemePref = "system" | "light" | "dark";

const KEY = "prefs:theme";
const media = window.matchMedia("(prefers-color-scheme: dark)");
const listeners = new Set<() => void>();
let pref: ThemePref = readCache();

function readCache(): ThemePref {
  const v = safeStorage.getItem(KEY);
  return v === "light" || v === "dark" ? v : "system";
}

export const themePref = () => pref;
export const resolvedTheme = (): "light" | "dark" => (pref === "system" ? (media.matches ? "dark" : "light") : pref);

function apply() {
  const root = document.documentElement;
  if (pref === "system") delete root.dataset.theme;
  else root.dataset.theme = pref;
  for (const l of listeners) l();
}

export function setThemePref(p: ThemePref) {
  pref = p;
  safeStorage.setItem(KEY, p);
  Layouts.Set(KEY, p).catch(() => {});
  apply();
}

/** Calls fn whenever the effective theme may have changed. */
export function onThemeChange(fn: () => void): () => void {
  listeners.add(fn);
  return () => listeners.delete(fn);
}

media.addEventListener("change", () => pref === "system" && apply());

/** Apply the cached choice now, then reconcile with the stored setting. */
export function initTheme() {
  apply();
  Layouts.Get(KEY)
    .then((v) => {
      if ((v === "system" || v === "light" || v === "dark") && v !== pref) setThemePref(v);
    })
    .catch(() => {});
}
