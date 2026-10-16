import { Copy, Download, Pencil, Plus, Trash2, Upload } from "lucide-react";
import { useMemo, useState } from "react";
import { Connections } from "../api";
import { useApp } from "../store";
import { useTree } from "../tree";
import {
  ExportConnectionsDialog,
  ImportConnectionsDialog,
} from "./ConnectionTransfer";
import { confirm } from "./Dialogs";
import { ConnIcon } from "./icons";
import { Toolbar } from "./Toolbar";

export function ManageConnections() {
  const managing = useApp((s) => s.managing);
  const connections = useApp((s) => s.connections);
  const groups = useApp((s) => s.groups);
  const open = useApp((s) => s.open);
  const [filter, setFilter] = useState("");
  const [picked, setPicked] = useState<Set<string>>(new Set());
  const [transfer, setTransfer] = useState<"export" | "import" | null>(null);
  const [msg, setMsg] = useState("");

  const shown = useMemo(() => {
    const f = filter.toLowerCase();
    return connections.filter(
      (c) =>
        !f ||
        [c.name, c.host, c.user, c.group, c.type].some((x) =>
          x?.toLowerCase().includes(f),
        ),
    );
  }, [connections, filter]);

  if (!managing) return null;
  const app = useApp.getState();
  const close = () => app.setManaging(false);
  const reload = () => app.loadConnections();
  const ids = [...picked];
  const one =
    ids.length === 1 ? connections.find((c) => c.id === ids[0]) : undefined;

  const remove = async () => {
    if (
      !(await confirm(
        `Delete ${ids.length} connection${ids.length > 1 ? "s" : ""}?`,
        {
          message: "Saved passwords are removed from the keychain too.",
          danger: true,
          okLabel: "Delete",
        },
      ))
    )
      return;
    for (const id of ids) {
      app.disconnect(id);
      useTree.getState().collapseConnection(id);
      await Connections.Delete(id);
    }
    setPicked(new Set());
    reload();
  };

  const done = (m: string) => {
    setTransfer(null);
    setMsg(m);
    reload();
  };

  return (
    <>
      <div className="modal-backdrop" onMouseDown={close}>
        <div
          className="modal manage"
          onMouseDown={(e) => e.stopPropagation()}
          onKeyDown={(e) => e.key === "Escape" && close()}
        >
          <div className="modal-title">Manage Connections</div>
          <Toolbar>
            <button onClick={() => app.editConnection("new")}>
              <Plus size={14} /> New
            </button>
            <button
              disabled={!one}
              onClick={() => one && app.editConnection(one)}
            >
              <Pencil size={14} /> Edit
            </button>
            <button
              disabled={!one}
              onClick={async () =>
                one && (await Connections.Duplicate(one.id), reload())
              }
            >
              <Copy size={14} /> Duplicate
            </button>
            <button disabled={!ids.length} onClick={remove}>
              <Trash2 size={14} /> Delete
            </button>
            <select
              disabled={!ids.length}
              value=""
              onChange={async (e) => {
                const g = e.target.value === "\u0000none" ? "" : e.target.value;
                for (const id of ids) await Connections.MoveToGroup(id, g);
                reload();
              }}
            >
              <option value="" disabled>
                Move to group…
              </option>
              <option value={"\u0000none"}>(no group)</option>
              {groups.map((g) => (
                <option key={g}>{g}</option>
              ))}
            </select>
            <span className="spacer" />
            <input
              className="search"
              placeholder="Filter"
              value={filter}
              onChange={(e) => setFilter(e.target.value)}
            />
          </Toolbar>
          <div className="manage-list">
            <table>
              <thead>
                <tr>
                  <th>
                    <input
                      type="checkbox"
                      checked={
                        shown.length > 0 && shown.every((c) => picked.has(c.id))
                      }
                      onChange={(e) =>
                        setPicked(
                          e.target.checked
                            ? new Set(shown.map((c) => c.id))
                            : new Set(),
                        )
                      }
                    />
                  </th>
                  <th>Name</th>
                  <th>Group</th>
                  <th>Host</th>
                  <th>User</th>
                  <th>Status</th>
                </tr>
              </thead>
              <tbody>
                {shown.map((c) => (
                  <tr
                    key={c.id}
                    className={picked.has(c.id) ? "selected" : ""}
                    onClick={(e) => {
                      const n = new Set(e.metaKey || e.shiftKey ? picked : []);
                      if (n.has(c.id) && (e.metaKey || e.shiftKey))
                        n.delete(c.id);
                      else n.add(c.id);
                      setPicked(n);
                    }}
                    onDoubleClick={() => app.editConnection(c)}
                  >
                    <td>
                      <input
                        type="checkbox"
                        checked={picked.has(c.id)}
                        onChange={() => {}}
                      />
                    </td>
                    <td>
                      <ConnIcon type={c.type} preset={c.preset} /> {c.name}
                    </td>
                    <td className="muted">{c.group}</td>
                    <td>
                      {c.host}:{c.port}
                      {c.ssh?.enabled && <span className="badge">SSH</span>}
                      {c.ssl?.enabled && <span className="badge">SSL</span>}
                    </td>
                    <td>{c.user}</td>
                    <td>
                      {open[c.id] ? (
                        <span className="ok">Open</span>
                      ) : (
                        <span className="muted">Closed</span>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          {msg && <div className="status-line ok">{msg}</div>}
          <div className="modal-footer">
            <button onClick={() => setTransfer("import")}>
              <Download size={14} /> Import…
            </button>
            <button
              onClick={() => setTransfer("export")}
              title={
                ids.length
                  ? "Export the selected connections"
                  : "Export all connections"
              }
            >
              <Upload size={14} /> Export{" "}
              {ids.length ? `${ids.length} selected` : "all"}…
            </button>
            <span className="spacer" />
            <button className="primary" onClick={close}>
              Done
            </button>
          </div>
        </div>
      </div>
      {transfer === "export" && (
        <ExportConnectionsDialog
          ids={ids}
          count={ids.length || connections.length}
          onClose={() => setTransfer(null)}
          onDone={done}
        />
      )}
      {transfer === "import" && (
        <ImportConnectionsDialog
          onClose={() => setTransfer(null)}
          onDone={done}
        />
      )}
    </>
  );
}
