// Single import point for the generated Go bindings.
export * as Connections from "../bindings/chloe/services/connectionservice";
export * as Explorer from "../bindings/chloe/services/explorerservice";
export * as Query from "../bindings/chloe/services/queryservice";
export { DBType, ObjectKind, SSHAuth, type Connection, type ObjectInfo, type ObjectRef, type StatementResult, type ResultColumn, type RowPage, type TableDetail, type Catalog } from "../bindings/chloe/internal/model/models";
export { ChangeKind, type RowChange, type BrowseRequest, type SortSpec } from "../bindings/chloe/internal/core/models";
export type { Dialect } from "../bindings/chloe/internal/driver/models";
export { Events } from "@wailsio/runtime";

/** Go nil slices arrive as null; normalise to an empty array. */
export const arr = <T,>(x: T[] | null | undefined): T[] => x ?? [];

/** Error text from a rejected binding call. */
export function errorText(e: unknown): string {
  if (e instanceof Error) return e.message;
  if (typeof e === "string") return e;
  if (e && typeof e === "object" && "message" in e) return String((e as { message: unknown }).message);
  return String(e);
}
