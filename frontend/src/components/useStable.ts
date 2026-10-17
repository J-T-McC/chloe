import { useCallback, useRef } from "react";

/** A function that keeps its identity across renders and calls the latest fn, so
 *  passing it to a memoised child doesn't redraw the child. */
export function useStable<A extends unknown[], R>(fn: (...args: A) => R): (...args: A) => R {
  const ref = useRef(fn);
  ref.current = fn;
  return useCallback((...args: A) => ref.current(...args), []);
}
