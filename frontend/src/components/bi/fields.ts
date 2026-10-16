import type { B } from "../../api";
import { arr } from "../../arr";

/** The fields charts can use: the source's columns after projection, then its
 *  custom fields (typed when the source was saved). */
export function sourceFields(src: B.DataSource | null | undefined): B.Field[] {
  const all = arr(src?.fields);
  const p = src?.projection;
  const base = !p || !arr(p.fields).length ? all : all.filter((f) => arr(p.fields).includes(f.name) !== !!p.exclude);
  return [...base, ...arr(src?.customFields).map((cf) => ({ name: cf.name, type: cf.type || "string", dbType: "custom" }) as B.Field)];
}
