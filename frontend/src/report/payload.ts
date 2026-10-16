// What an HTML report carries: a dashboard's pages, the charts on them and each
// chart's data as read at export. No connection, query or credential goes in.
import type { B } from "../api";

export interface ReportPayload {
  version: 1;
  name: string;
  workspace: string;
  theme: "light" | "dark";
  /** When the export ran (ISO). */
  exported: string;
  /** When the data was read: the oldest chart read (ISO). */
  asOf: string;
  interaction: string;
  pages: B.Page[];
  charts: B.Chart[];
  /** Data sources by id with their fields only (Controls need them). */
  sources: B.DataSource[];
  /** Parameter values the report was drawn with, by lower-case name. */
  values: Record<string, string>;
  /** Parameters shown in the header, with their values. */
  params: { name: string; value: string }[];
  /** Chart data by chart id, or the error reading it. */
  data: Record<string, B.Data | { error: string }>;
}

/** Where the payload sits in the report page. */
export const PAYLOAD_ID = "chloe-report";
