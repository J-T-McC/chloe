// Chloe's BI library: every workspace it manages, listed in the Objects tab when
// the BI toolbar button is pressed (Navicat's workspace object list).
import { ChartColumn, Copy, FolderOpen, Plus, RefreshCw, Trash2 } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { B, BI, arr, copyText, errorText } from "../../api";
import { useApp } from "../../store";
import { MenuItem, openMenu } from "../ContextMenu";
import { alert, confirm, prompt } from "../Dialogs";
import { createdByLabel } from "../../createdBy";
import { Split } from "../Split";
import { biChanged, listenBI, moveWorkspaceTabs, newWorkspace, openWorkspace, useBIVersion } from "./actions";
import { Toolbar } from "../Toolbar";
import { useAsync } from "../useAsync";

export function BIList() {
  const version = useBIVersion((s) => s.lib);
  const { data: items, error, loading } = useAsync(async () => arr(await BI.List()), [version], [] as B.Entry[], { keep: true });
  const [picked, setPicked] = useState("");
  const [filter, setFilter] = useState("");

  useEffect(() => listenBI(), []);

  const shown = useMemo(() => {
    const f = filter.toLowerCase();
    return items.filter((m) => !f || m.name.toLowerCase().includes(f));
  }, [items, filter]);
  const sel = items.find((m) => m.path === picked) ?? null;

  const run = async (what: string, fn: () => Promise<unknown>) => {
    try {
      await fn();
    } catch (e) {
      alert(what, errorText(e));
    }
    biChanged();
  };
  const rename = async (m: B.Entry) => {
    const name = await prompt("Rename Workspace", { message: "New name", defaultValue: m.name });
    if (name === null || name.trim() === m.name) return;
    run("Couldn't rename the workspace", async () => {
      const np = await BI.Rename(m.path, name);
      moveWorkspaceTabs(m.path, np, name.trim());
      setPicked(np);
    });
  };
  const remove = async (m: B.Entry) => {
    if (!(await confirm("Delete Workspace", { message: `Delete the workspace "${m.name}" with its data sources and charts? This can't be undone.`, okLabel: "Delete" }))) return;
    run("Couldn't delete the workspace", async () => {
      await BI.Delete(m.path);
      const app = useApp.getState();
      app.closeTabs(app.tabs.filter((t) => t.biPath === m.path).map((t) => t.id));
    });
  };
  const importFile = () =>
    run("Couldn't open the workspace", async () => {
      const p = await BI.Import();
      if (p) openWorkspace(p);
    });
  const menu = (m: B.Entry): MenuItem[] => [
    { label: "Open Workspace", onClick: () => openWorkspace(m.path) },
    { label: "Rename…", onClick: () => rename(m) },
    { label: "Duplicate", onClick: () => run("Couldn't duplicate the workspace", async () => setPicked(await BI.Duplicate(m.path))) },
    { label: "Delete…", onClick: () => remove(m) },
    { separator: true },
    { label: "Save As External File…", onClick: () => run("Couldn't save the file", () => BI.SaveExternal(m.path)) },
    { label: "Show in Finder", onClick: () => run("Couldn't show the file", () => BI.Reveal(m.path)) },
  ];

  return (
    <div className="objects-pane">
      <Toolbar>
        <button disabled={!sel} onClick={() => sel && openWorkspace(sel.path)}>
          <ChartColumn size={14} /> Open Workspace
        </button>
        <button onClick={newWorkspace}>
          <Plus size={14} /> New Workspace
        </button>
        <button disabled={!sel} onClick={() => sel && remove(sel)}>
          <Trash2 size={14} /> Delete Workspace
        </button>
        <button onClick={importFile} title="Copy a workspace file kept outside Chloe into the library">
          <FolderOpen size={14} /> Open External…
        </button>
        <button onClick={() => biChanged()} title="Refresh">
          <RefreshCw size={14} className={loading ? "spin" : ""} />
        </button>
        <span className="tb-sep" />
        <span className="crumbs">BI Workspaces</span>
        <span className="spacer" />
        <input className="search" placeholder="Filter" value={filter} onChange={(e) => setFilter(e.target.value)} />
      </Toolbar>
      <Split
        id="bi-library-info"
        className="objects-split"
        secondSize={320}
        minFirst={240}
        minSecond={180}
        maxSecond="70"
        first={
          <div
            className="object-list"
            tabIndex={0}
            onMouseDown={(e) => e.target === e.currentTarget && setPicked("")}
            onContextMenu={(e) =>
              e.target === e.currentTarget &&
              openMenu(e, [
                { label: "New Workspace", onClick: newWorkspace },
                { label: "Open External…", onClick: importFile },
                { separator: true },
                { label: "Refresh", onClick: () => biChanged() },
              ])
            }
            onKeyDown={(e) => {
              if (e.key === "Enter" && sel) openWorkspace(sel.path);
            }}
          >
            {error && <div className="error-banner">{error}</div>}
            <table>
              <thead>
                <tr>
                  <th>Name</th>
                  <th className="num">Data Sources</th>
                  <th className="num">Charts</th>
                  <th className="num">Dashboards</th>
                  <th>Created By</th>
                  <th>Modified</th>
                </tr>
              </thead>
              <tbody>
                {shown.map((m) => (
                  <tr
                    key={m.path}
                    className={m.path === picked ? "selected" : ""}
                    onClick={() => setPicked(m.path)}
                    onDoubleClick={() => openWorkspace(m.path)}
                    onContextMenu={(e) => (setPicked(m.path), openMenu(e, menu(m)))}
                  >
                    <td>
                      <ChartColumn size={13} /> {m.name}
                      {m.error && <span className="err"> (can't be read)</span>}
                    </td>
                    <td className="num">{m.dataSources}</td>
                    <td className="num">{m.charts}</td>
                    <td className="num">{m.dashboards}</td>
                    <td className="muted">{createdByLabel(m.createdBy)}</td>
                    <td>{m.modified ? new Date(m.modified).toLocaleString() : ""}</td>
                  </tr>
                ))}
              </tbody>
            </table>
            {!loading && !shown.length && !error && (
              <div className="center-hint">{items.length ? "No workspaces match the filter." : "No BI workspaces yet. Create one, or use Create Chart on a table or query result."}</div>
            )}
          </div>
        }
        second={
          sel ? (
            <div className="info-panel">
              <div className="info-head">
                <ChartColumn size={16} /> {sel.name}
              </div>
              <dl>
                <dt>Data sources</dt>
                <dd>{sel.dataSources}</dd>
                <dt>Charts</dt>
                <dd>{sel.charts}</dd>
                <dt>Dashboards</dt>
                <dd>{sel.dashboards}</dd>
                {sel.createdBy && (
                  <>
                    <dt>Created by</dt>
                    <dd>{createdByLabel(sel.createdBy)}</dd>
                  </>
                )}
                <dt>Modified</dt>
                <dd>{sel.modified ? new Date(sel.modified).toLocaleString() : ""}</dd>
                <dt>File</dt>
                <dd>
                  {sel.path}{" "}
                  <button className="link-button" title="Copy path" onClick={() => copyText(sel.path)}>
                    <Copy size={11} />
                  </button>
                </dd>
                {sel.error && (
                  <>
                    <dt>Error</dt>
                    <dd className="err">{sel.error}</dd>
                  </>
                )}
              </dl>
            </div>
          ) : (
            <div className="info-panel empty">Select a workspace to see its details.</div>
          )
        }
      />
    </div>
  );
}
