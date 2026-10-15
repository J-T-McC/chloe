// The user (MySQL) or role (PostgreSQL) list of a connection.
import { KeyRound, Pencil, Plus, RefreshCw, ShieldCheck, Trash2, UserRound, UsersRound } from "lucide-react";
import { useCallback, useEffect, useMemo, useState } from "react";
import { P, Users, arr, errorText, copyText } from "../../api";
import { Tab, useApp } from "../../store";
import { openMenu } from "../ContextMenu";
import { alert, confirm } from "../Dialogs";
import { accountLabel, isPGConn, openUser } from "./actions";

export function UsersPane({ tab }: { tab: Tab }) {
  const connId = tab.connId!;
  const pg = isPGConn(connId);
  const [users, setUsers] = useState<P.UserInfo[]>([]);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const [picked, setPicked] = useState<Set<string>>(new Set());
  const [filter, setFilter] = useState("");
  const version = useApp((s) => s.objectsVersion);

  const load = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      setUsers(arr(await Users.List(connId)));
    } catch (e) {
      setError(errorText(e));
    } finally {
      setLoading(false);
    }
  }, [connId]);

  useEffect(() => {
    load();
  }, [load, version]);

  const shown = useMemo(() => users.filter((u) => !filter || accountLabel(u).toLowerCase().includes(filter.toLowerCase())), [users, filter]);
  const sel = shown.filter((u) => picked.has(accountLabel(u)));
  const noun = pg ? "Role" : "User";

  const drop = async (list: P.UserInfo[]) => {
    if (!list.length) return;
    const names = list.map(accountLabel).join(", ");
    if (!(await confirm(`Delete ${list.length > 1 ? `${list.length} ${noun.toLowerCase()}s` : `${noun.toLowerCase()} ${names}`}?`, { message: list.length > 1 ? names : undefined, okLabel: "Delete" }))) return;
    const res = await Users.Drop(connId, list);
    if (res.error) {
      alert("Delete failed", res.error + (pg && /depend/i.test(res.error) ? "\n\nRevoke the role's privileges (or reassign what it owns) first." : ""));
    }
    setPicked(new Set());
    load();
  };

  const menu = (e: React.MouseEvent, u?: P.UserInfo) => {
    const list = u && !picked.has(accountLabel(u)) ? [u] : sel;
    if (u && !picked.has(accountLabel(u))) setPicked(new Set([accountLabel(u)]));
    openMenu(e, [
      { label: `Edit ${noun}`, disabled: list.length !== 1, onClick: () => openUser(connId, list[0]) },
      { label: `New ${noun}`, onClick: () => openUser(connId) },
      { label: `Delete ${noun}`, disabled: !list.length, onClick: () => drop(list) },
      { separator: true },
      { label: "Copy Name", disabled: !list.length, onClick: () => copyText(list.map(accountLabel).join("\n")) },
      { label: "Refresh", onClick: load },
    ]);
  };

  return (
    <div className="users-pane">
      <div className="toolbar">
        <button onClick={() => openUser(connId)}>
          <Plus size={14} /> New {noun}
        </button>
        <button disabled={sel.length !== 1} onClick={() => openUser(connId, sel[0])}>
          <Pencil size={14} /> Edit {noun}
        </button>
        <button disabled={!sel.length} onClick={() => drop(sel)}>
          <Trash2 size={14} /> Delete {noun}
        </button>
        <button onClick={load} title="Refresh">
          <RefreshCw size={14} className={loading ? "spin" : ""} />
        </button>
        <span className="spacer" />
        <input className="search" placeholder="Filter" value={filter} onChange={(e) => setFilter(e.target.value)} />
      </div>
      {error && <div className="error-banner">{error}</div>}
      <div className="users-table-wrap" onContextMenu={(e) => e.target === e.currentTarget && menu(e)}>
        <table className="redis-table users-table">
          <thead>
            <tr>
              <th>{pg ? "Role" : "User"}</th>
              {!pg && <th>Host</th>}
              {!pg && <th>Plugin</th>}
              <th>Type</th>
              <th>{pg ? "Can Log In" : "Locked"}</th>
            </tr>
          </thead>
          <tbody>
            {shown.map((u) => {
              const k = accountLabel(u);
              return (
                <tr
                  key={k}
                  className={picked.has(k) ? "selected" : ""}
                  onClick={(e) =>
                    setPicked((p) => {
                      if (!(e.metaKey || e.ctrlKey)) return new Set([k]);
                      const n = new Set(p);
                      if (n.has(k)) n.delete(k);
                      else n.add(k);
                      return n;
                    })
                  }
                  onDoubleClick={() => openUser(connId, u)}
                  onContextMenu={(e) => menu(e, u)}
                >
                  <td>
                    {u.super ? <ShieldCheck size={13} className="kind-icon" /> : u.isRole ? <UsersRound size={13} className="kind-icon" /> : <UserRound size={13} className="kind-icon" />} {u.name}
                  </td>
                  {!pg && <td className="mono">{u.host}</td>}
                  {!pg && <td className="muted">{u.plugin}</td>}
                  <td className="muted">{u.super ? "Superuser" : u.isRole ? "Role" : pg ? "Login role" : "User"}</td>
                  <td>{pg ? (u.canLogin ? "Yes" : "No") : u.locked ? <KeyRound size={12} /> : ""}</td>
                </tr>
              );
            })}
          </tbody>
        </table>
        {!loading && !error && !shown.length && <div className="center-hint">No {noun.toLowerCase()}s.</div>}
      </div>
      <div className="statusbar">
        <span>
          {shown.length} {noun.toLowerCase()}
          {shown.length === 1 ? "" : "s"}
        </span>
      </div>
    </div>
  );
}
