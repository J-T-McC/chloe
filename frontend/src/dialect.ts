// SQL dialect of a connection, for the editor, autocomplete and designers.
import { DBType } from "./api";
import { quoteIdent } from "./sql/complete";
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

export const isPG = (connId?: string) => connById(connId)?.type === DBType.PostgreSQL;

const ENGINE_LABEL: Record<string, string> = {
  [DBType.MySQL]: "MySQL",
  [DBType.MariaDB]: "MariaDB",
  [DBType.PostgreSQL]: "PostgreSQL",
  [DBType.MongoDB]: "MongoDB",
  [DBType.Redis]: "Redis",
  [DBType.SQLite]: "SQLite",
  [DBType.SQLServer]: "SQL Server",
  [DBType.Oracle]: "Oracle",
};

/** Display name of an engine (a DBType, or a model's dialect: mysql, mariadb, postgres). */
export const engineLabel = (t?: string) => ENGINE_LABEL[t ?? ""] ?? t ?? "";

/**
 * A table or view name for SQL on its connection, quoted where needed. Qualified with
 * its schema (Postgres) or database only when that isn't the default, or always with full.
 */
export function qualifiedName(connId: string, ref: { database: string; schema?: string; name: string }, full = false) {
  const d = connDialect(connId);
  const qualifier = d === "postgres" ? (full || (ref.schema && ref.schema !== "public") ? ref.schema || "public" : "") : full || (d === "sqlite" && ref.database !== "main") ? ref.database : "";
  return qualifier ? `${quoteIdent(qualifier, d)}.${quoteIdent(ref.name, d)}` : quoteIdent(ref.name, d);
}
