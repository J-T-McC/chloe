import { connById, useApp } from "../../store";

/** Open a new Redis console tab for a connection, starting in database db. */
export function openRedisConsole(connId: string, db = 0) {
  useApp.getState().openTab({
    kind: "redisconsole",
    title: `Console — ${connById(connId)?.name ?? ""}`,
    connId,
    database: String(db),
  });
}
