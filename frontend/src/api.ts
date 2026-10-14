// Single import point for the generated Go bindings.
export * as Connections from "../bindings/chloe/services/connectionservice";
export * as Explorer from "../bindings/chloe/services/explorerservice";
export * as Query from "../bindings/chloe/services/queryservice";
export * as Monitor from "../bindings/chloe/services/monitorservice";
export * as Design from "../bindings/chloe/services/designservice";
export * as Transfer from "../bindings/chloe/services/transferservice";
export * as Layouts from "../bindings/chloe/services/layoutservice";
export * as Models from "../bindings/chloe/services/modelservice";
export * as Redis from "../bindings/chloe/services/redisservice";
export type * as R from "../bindings/chloe/internal/rdb/models";
export type * as DM from "../bindings/chloe/internal/datamodel/models";
export { Format, type ExportOptions, type ImportOptions } from "../bindings/chloe/internal/transfer/models";
export type * as D from "../bindings/chloe/internal/design/models";
export type { Preset } from "../bindings/chloe/internal/connurl/models";
export { DBType, ObjectKind, SSHAuth, type Connection, type ServerInfo, type Process, type ServerVar, type ObjectInfo, type ObjectRef, type StatementResult, type ResultColumn, type RowPage, type TableDetail, type Catalog } from "../bindings/chloe/internal/model/models";
export { ChangeKind, TableAction, ImportMode, MaintainOp, JobStatus, type Job, type ImportPreview, type ImportRequest, type FieldMap, type MaintainResult, type DesignPreview, type ApplyResult as DesignApplyResult, type RowChange, type BrowseRequest, type SortSpec } from "../bindings/chloe/internal/core/models";
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
