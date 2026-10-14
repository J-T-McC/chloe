// SQL dialect of a connection, for the editor, autocomplete and designers.
import { DBType } from "./api";
import { connById } from "./store";

export type SqlDialect = "mysql" | "postgres" | "sqlite";

export function dialectOf(type?: DBType): SqlDialect {
  if (type === DBType.PostgreSQL) return "postgres";
  if (type === DBType.SQLite) return "sqlite";
  return "mysql";
}

export const connDialect = (connId?: string): SqlDialect => dialectOf(connById(connId)?.type);

/** Monaco language for a dialect (SQLite uses Monaco's generic SQL). */
export const editorLanguage = (d: SqlDialect) => (d === "postgres" ? "pgsql" : d === "sqlite" ? "sql" : "mysql");

/** Databases act as name qualifiers (db.table) rather than schemas. */
export const databasesAsSchemas = (d: SqlDialect) => d !== "postgres";
