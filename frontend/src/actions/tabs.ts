// Opening tabs for tree objects, queries and the server monitor: used by the tree, the
// object list, menus and toolbars.
import { DBType, ObjectKind, ObjectRef } from "../api";
import { newMongoQuery, openCollection } from "../components/mongo/actions";
import { qualifiedName } from "../dialect";
import { openSavedQuery } from "../queries";
import { connById, useApp } from "../store";
import type { NodeOf } from "../tree";

export function refOf(n: NodeOf<"object">): ObjectRef {
  return { database: n.database, schema: n.schema, name: n.info.name, kind: n.kind, signature: n.info.signature };
}

export function openObject(n: NodeOf<"object">) {
  const app = useApp.getState();
  if (connById(n.connId)?.type === DBType.MongoDB) return openCollection(n.connId, n.database, n.info.name, n.kind);
  if (n.kind === ObjectKind.KindQuery) return openSavedQuery(n.connId, n.database, n.schema, n.info.name);
  const ref = refOf(n);
  if (n.kind === ObjectKind.KindTable || n.kind === ObjectKind.KindView || n.kind === ObjectKind.KindMatView) {
    app.openTab({ kind: "table", title: n.info.name, connId: n.connId, database: n.database, schema: n.schema, ref });
  } else {
    openDDL(n.connId, ref);
  }
}

export function newQuery(connId: string, database?: string, schema?: string, sql = "") {
  const app = useApp.getState();
  const conn = connById(connId);
  if (conn?.type === DBType.MongoDB) return newMongoQuery(connId, database, sql);
  const db = database ?? (conn?.initialDatabase || undefined);
  // A query tab works in its database, so that database counts as open.
  if (db) app.openDatabase(connId, db);
  app.openTab({ kind: "query", title: `Query — ${db ?? conn?.name ?? ""}`, connId, database: db, schema, sql, savedSql: sql });
}

/** "SELECT * FROM table;" with the table quoted for the connection's dialect. */
export function selectAllSQL(connId: string, ref: ObjectRef) {
  const type = connById(connId)?.type;
  if (type === DBType.MongoDB) return `db.getCollection(${JSON.stringify(ref.name)}).find({})`;
  return `SELECT * FROM ${qualifiedName(connId, ref)};`;
}

export function openMonitor(connId: string) {
  const conn = connById(connId);
  useApp.getState().openTab({ kind: "monitor", title: `Server Monitor — ${conn?.name ?? ""}`, connId, ref: { database: "", name: "monitor", kind: ObjectKind.$zero } });
}

export function openDDL(connId: string, ref: ObjectRef) {
  useApp.getState().openTab({ kind: "ddl", title: ref.name, connId, database: ref.database, schema: ref.schema, ref });
}
