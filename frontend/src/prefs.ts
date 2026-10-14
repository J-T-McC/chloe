// Small per-user UI flags kept in localStorage (panel visibility and the like).
export function readFlag(name: string, def = false): boolean {
  try {
    const v = localStorage.getItem("ui:" + name);
    return v === null ? def : v === "1";
  } catch {
    return def;
  }
}

export function writeFlag(name: string, on: boolean) {
  try {
    localStorage.setItem("ui:" + name, on ? "1" : "0");
  } catch {
    /* a convenience only */
  }
}
