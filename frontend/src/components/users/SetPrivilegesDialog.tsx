// Set Privileges: every account's privileges on one object, as a grid of users ×
// privileges. Saving grants and revokes per changed account.
import { Plus, X } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { create } from "zustand";
import { P, Users, arr, errorText } from "../../api";
import { useApp } from "../../store";
import { Combobox } from "../Combobox";
import { accountLabel } from "./actions";
import { Toolbar } from "../Toolbar";
import { Modal, ModalFooter } from "../Modal";
import { isPG } from "../../dialect";

type Target = Omit<P.Grant, "privilege" | "grantable">;

const useSetPrivs = create<{ open?: { connId: string; target: Target; title: string } }>(() => ({}));

export const openSetPrivileges = (connId: string, target: Target, title: string) => useSetPrivs.setState({ open: { connId, target, title } });

interface Row {
  name: string;
  host?: string;
  privs: Record<string, boolean>; // privilege -> granted
  grantable: boolean;
}

export function SetPrivilegesDialog() {
  const open = useSetPrivs((s) => s.open);
  if (!open) return null;
  return <Inner key={`${open.connId}:${JSON.stringify(open.target)}`} {...open} />;
}

const rowKey = (r: { name: string; host?: string }) => accountLabel(r);

function Inner({ connId, target, title }: { connId: string; target: Target; title: string }) {
  const pg = isPG(connId);
  const [privList, setPrivList] = useState<string[]>([]);
  const [accounts, setAccounts] = useState<P.UserInfo[]>([]);
  const [original, setOriginal] = useState<Row[]>([]);
  const [rows, setRows] = useState<Row[]>([]);
  const [adding, setAdding] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const close = () => useSetPrivs.setState({ open: undefined });

  // Escape closes the dialog wherever the focus is.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && !document.querySelector(".combo-list") && close();
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  useEffect(() => {
    (async () => {
      try {
        const [meta, users, grants] = await Promise.all([Users.Meta(connId), Users.List(connId), Users.ObjectGrants(connId, { ...target, privilege: "" })]);
        setPrivList(arr(meta.levels?.[target.level]));
        setAccounts(arr(users));
        const byUser = new Map<string, Row>();
        for (const g of arr(grants)) {
          const k = rowKey({ name: g.user, host: g.host });
          const r = byUser.get(k) ?? { name: g.user, host: g.host || undefined, privs: {}, grantable: false };
          r.privs[g.privilege] = true;
          r.grantable ||= !!g.grantable;
          byUser.set(k, r);
        }
        const list = [...byUser.values()].sort((a, b) => rowKey(a).localeCompare(rowKey(b)));
        setOriginal(list);
        setRows(structuredClone(list));
      } catch (e) {
        setError(errorText(e));
      }
    })();
  }, [connId, target]);

  const options = useMemo(() => accounts.map(rowKey).filter((k) => !rows.some((r) => rowKey(r) === k)), [accounts, rows]);

  const toGrants = (r: Row): P.Grant[] => Object.entries(r.privs).filter(([, on]) => on).map(([privilege]) => ({ ...target, privilege, grantable: r.grantable }));

  const save = async () => {
    setBusy(true);
    setError("");
    const errors: string[] = [];
    const all = new Map<string, { before?: Row; after?: Row }>();
    for (const r of original) all.set(rowKey(r), { before: r });
    for (const r of rows) all.set(rowKey(r), { ...all.get(rowKey(r)), after: r });
    for (const [k, { before, after }] of all) {
      const who = (after ?? before)!;
      // The same account skeleton on both sides, so only the grants differ.
      const base: P.User = { name: who.name, host: who.host, connLimit: -1, memberOf: [], privileges: [] };
      const old = { ...base, origName: who.name, origHost: who.host, privileges: before ? toGrants(before) : [] };
      const next = { ...base, privileges: after ? toGrants(after) : [] };
      if (JSON.stringify(old.privileges) === JSON.stringify(next.privileges)) continue;
      const res = await Users.Apply(connId, old, next).catch((e) => ({ error: errorText(e), executed: 0 }));
      if (res.error) errors.push(`${k}: ${res.error}`);
    }
    setBusy(false);
    if (errors.length) return setError(errors.join("\n"));
    useApp.setState((s) => ({ objectsVersion: s.objectsVersion + 1 }));
    close();
  };

  const set = (i: number, patch: Partial<Row>) => setRows((rs) => rs.map((r, j) => (j === i ? { ...r, ...patch } : r)));

  return (
    <Modal title={`Set Privileges — ${title}`} className="wizard wide set-privs" onClose={close}>
      <Toolbar>
        <div style={{ width: 260 }}>
          <Combobox value={adding} options={options} placeholder={`Add ${pg ? "role" : "user"}…`} strict onChange={setAdding} />
        </div>
        <button
          disabled={!adding}
          onClick={() => {
            const a = accounts.find((x) => rowKey(x) === adding);
            if (a) setRows((rs) => [...rs, { name: a.name, host: a.host || undefined, privs: {}, grantable: false }]);
            setAdding("");
          }}
        >
          <Plus size={14} /> Add
        </button>
      </Toolbar>
      <div className="modal-body set-privs-body">
        <table className="redis-table privs-table">
          <thead>
            <tr>
              <th>{pg ? "Role" : "User"}</th>
              {privList.map((p) => (
                <th key={p}>{p}</th>
              ))}
              <th>Grant Option</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {rows.map((r, i) => (
              <tr key={rowKey(r)}>
                <td>{rowKey(r)}</td>
                {privList.map((p) => (
                  <td key={p}>
                    <input type="checkbox" checked={!!r.privs[p]} onChange={(e) => set(i, { privs: { ...r.privs, [p]: e.target.checked } })} />
                  </td>
                ))}
                <td>
                  <input type="checkbox" checked={r.grantable} onChange={(e) => set(i, { grantable: e.target.checked })} />
                </td>
                <td>
                  <button className="icon" title="Revoke everything" onClick={() => setRows((rs) => rs.filter((_, j) => j !== i))}>
                    <X size={13} />
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
        {!rows.length && <div className="center-hint">No {pg ? "role" : "user"} has privileges on this object. Add one above.</div>}
      </div>
      {error && <div className="status-line err">{error}</div>}
      <ModalFooter onCancel={close} onOk={save} okLabel={busy ? "Saving…" : "OK"} okDisabled={busy} />
    </Modal>
  );
}
