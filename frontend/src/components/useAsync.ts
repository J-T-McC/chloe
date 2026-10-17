import { DependencyList, useCallback, useEffect, useRef, useState } from "react";
import { errorText } from "../api";

export type Async<T> = { data: T; error: string; loading: boolean; reload: () => void };

/** Runs fn whenever deps change (after delay ms, for debounced previews) and keeps
 *  its latest result. A response that arrives after deps changed again, or after
 *  unmount, is dropped, so a quick switch never shows the previous connection's
 *  data. A null fn clears to initial.
 *  keep: true keeps the last data while the next run loads; a string keeps it only
 *  while the string stays the same (a refresh of one listing, not a switch to another). */
export function useAsync<T>(fn: (() => Promise<T>) | null, deps: DependencyList, initial: T, opts: { delay?: number; keep?: boolean | string } = {}): Async<T> {
  const [state, setState] = useState({ data: initial, error: "", loading: false });
  const [tick, setTick] = useState(0);
  const lastKey = useRef<string | undefined>(undefined);
  useEffect(() => {
    const { delay, keep } = opts;
    const kept = keep === true || (typeof keep === "string" && keep === lastKey.current);
    lastKey.current = typeof keep === "string" ? keep : undefined;
    if (!fn) {
      setState({ data: initial, error: "", loading: false });
      return;
    }
    let live = true;
    setState((s) => ({ data: kept ? s.data : initial, error: "", loading: true }));
    const run = () =>
      fn().then(
        (data) => live && setState({ data, error: "", loading: false }),
        (e) => live && setState((s) => ({ data: kept ? s.data : initial, error: errorText(e), loading: false })),
      );
    const t = delay ? setTimeout(run, delay) : (run(), undefined);
    return () => {
      live = false;
      clearTimeout(t);
    };
  }, [...deps, tick]); // eslint-disable-line react-hooks/exhaustive-deps
  const reload = useCallback(() => setTick((n) => n + 1), []);
  return { ...state, reload };
}
