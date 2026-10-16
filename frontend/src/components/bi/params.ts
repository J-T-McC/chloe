// Parameter values and [$name] references, shared by the app and the report
// viewer (no bindings here).
import type { B } from "../../api";
import { arr } from "../../arr";

const REF = /\[\$([A-Za-z_][A-Za-z0-9_]*)\]/g;

export type ParamValues = Record<string, string>;

/** Parameter values by lower-case name: the saved values, with overrides on top. */
export function paramValues(params: B.Parameter[] | null | undefined, overrides: ParamValues = {}): ParamValues {
  const out: ParamValues = {};
  for (const p of arr(params)) out[p.name.toLowerCase()] = p.value;
  for (const [k, v] of Object.entries(overrides)) out[k.toLowerCase()] = v;
  return out;
}

/** Replace [$name] with its value; unknown names stay as written. */
export function expandParams(text: string, values: Record<string, string | undefined> | null | undefined): string {
  if (!values || !text.includes("[$")) return text;
  return text.replace(REF, (m, name: string) => values[name.toLowerCase()] ?? m);
}

/** The parameter names text refers to. */
export function paramRefs(text: string | null | undefined): string[] {
  return [...(text ?? "").matchAll(REF)].map((m) => m[1].toLowerCase());
}
