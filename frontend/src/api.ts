// Single import point for the generated Go bindings.
export * as Connections from "../bindings/chloe/services/connectionservice";
export * as Explorer from "../bindings/chloe/services/explorerservice";
export * as Query from "../bindings/chloe/services/queryservice";
export * as Monitor from "../bindings/chloe/services/monitorservice";
export * as Design from "../bindings/chloe/services/designservice";
export * as Transfer from "../bindings/chloe/services/transferservice";
export * as Layouts from "../bindings/chloe/services/layoutservice";
export * as Models from "../bindings/chloe/services/modelservice";
export * as BI from "../bindings/chloe/services/biservice";
export type * as B from "../bindings/chloe/internal/bi/models";
export type { Catalog as BICatalog, NewChart as BINewChart } from "../bindings/chloe/services/models";
export type { BIPreview } from "../bindings/chloe/internal/core/models";
export * as Redis from "../bindings/chloe/services/redisservice";
export * as Mongo from "../bindings/chloe/services/mongoservice";
export type * as MG from "../bindings/chloe/internal/mdb/models";
export type { SavedQuery, HistoryEntry, Draft } from "../bindings/chloe/internal/store/models";
export * as Users from "../bindings/chloe/services/userservice";
export * as MCP from "../bindings/chloe/services/mcpservice";
export { MCPLevel, type MCPSettings, type MCPEndpoint } from "../bindings/chloe/internal/store/models";
export type { Activity as MCPActivity, Status as MCPStatus, ApprovalRequest as MCPApproval, QueryTab as MCPQueryTab } from "../bindings/chloe/internal/mcpserver/models";
export type * as P from "../bindings/chloe/internal/privs/models";
export type * as R from "../bindings/chloe/internal/rdb/models";
export type * as DM from "../bindings/chloe/internal/datamodel/models";
export { Format, type ExportOptions, type ImportOptions } from "../bindings/chloe/internal/transfer/models";
export type * as D from "../bindings/chloe/internal/design/models";
export type { Preset } from "../bindings/chloe/internal/connurl/models";
export { DBType, ObjectKind, SSHAuth, type Connection, type ServerInfo, type Process, type ServerVar, type ObjectInfo, type ObjectRef, type StatementResult, type ResultColumn, type RowPage, type TableDetail, type Catalog } from "../bindings/chloe/internal/model/models";
export { ChangeKind, TableAction, ImportMode, MaintainOp, JobStatus, type Job, type DataTransferRequest, type Endpoint, type TransferOptions, type StructItem, type DataSyncPage, type DataSyncResult, type DataSyncTable, type ImportPreview, type ImportRequest, type FieldMap, type MaintainResult, type DesignPreview, type ApplyResult as DesignApplyResult, type RowChange, type ResultEdit, type ResultPatch, type BrowseRequest, type SortSpec } from "../bindings/chloe/internal/core/models";
export type { Dialect } from "../bindings/chloe/internal/driver/models";
export { Events } from "@wailsio/runtime";
import { Clipboard } from "@wailsio/runtime";

/** Copy text to the clipboard. The desktop webview may refuse navigator.clipboard,
 *  so Wails' native clipboard takes over there (in the browser build it is a no-op). */
export async function copyText(text: string): Promise<void> {
  try {
    await navigator.clipboard.writeText(text);
  } catch {
    await Clipboard.SetText(text);
  }
}

/** Go nil slices arrive as null; normalise to an empty array. */
export const arr = <T,>(x: T[] | null | undefined): T[] => x ?? [];

/** Error text from a rejected binding call. */
export function errorText(e: unknown): string {
  if (e instanceof Error) return e.message;
  if (typeof e === "string") return e;
  if (e && typeof e === "object" && "message" in e) return String((e as { message: unknown }).message);
  return String(e);
}
