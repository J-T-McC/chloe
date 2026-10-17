import { ObjectKind, P } from "../../api";
import { connById, useApp } from "../../store";
import { isPG } from "../../dialect";


/** Display name of an account: user@host (MySQL) or the role name. */
export const accountLabel = (u: { name: string; host?: string }) => (u.host ? `${u.name}@${u.host}` : u.name);

/** Open the user list of a connection. */
export function openUsers(connId: string) {
  const pg = isPG(connId);
  useApp.getState().openTab({ kind: "users", title: `${pg ? "Roles" : "Users"} — ${connById(connId)?.name ?? ""}`, connId });
}

/** Open the user editor for an account, or a new one. */
export function openUser(connId: string, u?: P.UserInfo) {
  const pg = isPG(connId);
  useApp.getState().openTab({
    kind: "user",
    title: u ? `${accountLabel(u)} (${pg ? "role" : "user"})` : `Untitled (${pg ? "role" : "user"})`,
    connId,
    // name "" = new; the host rides in schema (only MySQL accounts have one).
    ref: { database: "", schema: u?.host, name: u?.name ?? "", kind: ObjectKind.$zero, signature: u ? "user" : String(Date.now()) },
  });
}
