// Small per-user UI conveniences kept in localStorage (panel visibility, sizes, last
// view and the like). Storage can throw (private mode, blocked storage), so every read
// falls back and every write may be lost.
export const safeStorage = {
  getItem(key: string): string | null {
    try {
      return localStorage.getItem(key);
    } catch {
      return null;
    }
  },
  setItem(key: string, value: string) {
    try {
      localStorage.setItem(key, value);
    } catch {
      /* a convenience only */
    }
  },
};

export function readFlag(name: string, def = false): boolean {
  const v = safeStorage.getItem("ui:" + name);
  return v === null ? def : v === "1";
}

export function writeFlag(name: string, on: boolean) {
  safeStorage.setItem("ui:" + name, on ? "1" : "0");
}
